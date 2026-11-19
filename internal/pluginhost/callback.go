package pluginhost

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/plugin"
)

// maxCallbackBody bounds what a plugin may send the cockpit in one call. It
// holds an assistant's first message at its limit with every byte escaped.
const maxCallbackBody = 256 << 10

// callbackHandler serves one external plugin's callback socket: the readers
// of plugin.Serve, its Projects and Assistants surfaces and its events,
// scoped to this plugin. The socket's directory is the owner's alone, the bearer token tells
// this plugin from other processes of the same user. It changes with every
// start and is compared in constant time.
func (s *supervisor) callbackHandler(pluginStateDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/plugin", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"id":               s.id,
			"base":             "/plugins/" + s.id,
			"state_dir":        s.host.cfg.StateDir,
			"plugin_state_dir": pluginStateDir,
			"projects_dir":     s.host.cfg.ProjectsDir,
		})
	})
	mux.HandleFunc("POST /v1/projects", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			writeError(w, http.StatusBadRequest, "Send the project as {\"name\": \"...\"}.")
			return
		}
		project, err := s.host.projects.Create(r.Context(), body.Name)
		switch {
		case errors.Is(err, plugin.ErrProjectExists):
			writeError(w, http.StatusConflict, err.Error())
		case err != nil:
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeJSON(w, http.StatusCreated, map[string]string{"name": project.Name(), "dir": project.Dir()})
		}
	})
	mux.HandleFunc("POST /v1/projects/{name}/changed", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !s.host.projectExists(name) {
			writeError(w, http.StatusNotFound, "There is no project with that name.")
			return
		}
		if err := s.host.projects.changed(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	assistants := s.host.cfg.Assistants
	mux.HandleFunc("POST /v1/assistants", func(w http.ResponseWriter, r *http.Request) {
		var in plugin.AssistantSpec
		if err := decodeBody(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "Send the assistant as {\"name\": \"...\", \"message\": \"...\", \"coder_id\": \"...\"}, no other field.")
			return
		}
		made, err := assistants.Create(r.Context(), in)
		if made.ID == "" {
			if err == nil {
				err = errors.New("No assistant was made.")
			}
			writeError(w, assistantsStatus(err, http.StatusBadRequest), err.Error())
			return
		}
		answer := createdAssistant{Assistant: made}
		if err != nil {
			answer.Error = errorMessage(err.Error())
		}
		writeJSON(w, http.StatusCreated, answer)
	})
	mux.HandleFunc("GET /v1/assistants", func(w http.ResponseWriter, r *http.Request) {
		var in plugin.AssistantQuery
		if err := decodeQuery(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "Narrow the list with ?contains=<word>, once, and nothing else.")
			return
		}
		list, err := assistants.List(r.Context(), in)
		if err != nil {
			writeError(w, assistantsStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		if list.Assistants == nil {
			list.Assistants = []plugin.Assistant{}
		}
		writeJSON(w, http.StatusOK, list)
	})
	mux.HandleFunc("GET /v1/assistants/{assistant_id}/last-message", func(w http.ResponseWriter, r *http.Request) {
		var in plugin.AssistantMessageQuery
		if err := decodeQuery(r, &in, "assistant_id"); err != nil {
			writeError(w, http.StatusBadRequest, "The last message takes no query parameter.")
			return
		}
		message, err := assistants.LastMessage(r.Context(), in)
		if err != nil {
			writeError(w, assistantsStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, message)
	})
	mux.HandleFunc("GET /v1/assistants/coders", func(w http.ResponseWriter, r *http.Request) {
		var in plugin.AssistantCoderQuery
		if err := decodeQuery(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "The coder list takes no query parameter.")
			return
		}
		list, err := assistants.Coders(r.Context(), in)
		if err != nil {
			writeError(w, assistantsStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		if list.Coders == nil {
			list.Coders = []plugin.AssistantCoder{}
		}
		writeJSON(w, http.StatusOK, list)
	})
	mux.HandleFunc("GET /v1/events", s.streamEvents)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		token := s.token
		s.mu.Unlock()
		got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeError(w, http.StatusUnauthorized, "The token is missing or wrong.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxCallbackBody)
		mux.ServeHTTP(w, r)
	})
}

// streamEvents streams the events of the manifest's sources as server sent
// events, one JSON object per data line. A ping comment every 15 seconds
// reveals a dead stream.
func (s *supervisor) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "The socket cannot stream.")
		return
	}
	stream, ok := s.openStream()
	if !ok {
		writeError(w, http.StatusTooManyRequests, "At most 8 event streams per plugin.")
		return
	}
	defer s.closeStream(stream)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-stream:
			raw, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		}
		flusher.Flush()
	}
}

// assistantsStatus is the status of an error of the assistant surface,
// otherwise for one it does not know.
func assistantsStatus(err error, otherwise int) int {
	switch {
	case errors.Is(err, plugin.ErrAssistantNotFound):
		return http.StatusNotFound
	case errors.Is(err, plugin.ErrAssistantsNotReady):
		return http.StatusServiceUnavailable
	}
	return otherwise
}

// projectExists answers whether name is a project directory directly under
// the projects root.
func (h *Host) projectExists(name string) bool {
	if name == "" || name != filepath.Base(name) || !filepath.IsLocal(name) || hasControl(name) {
		return false
	}
	info, err := os.Stat(filepath.Join(h.cfg.ProjectsDir, name))
	return err == nil && info.IsDir()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// maxErrorBytes bounds every error message the callback socket answers. The
// cockpit's sentences fit whole, a value a sentence quotes is cut where it is
// quoted.
const maxErrorBytes = 1 << 10

func errorMessage(message string) string { return cutTo(message, maxErrorBytes) }

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": errorMessage(message)})
}

// createdAssistant is the answer of POST /v1/assistants: the assistant Create
// made, and beside it the error of a first message that could not go out.
type createdAssistant struct {
	Assistant plugin.Assistant `json:"assistant"`
	Error     string           `json:"error,omitempty"`
}

// decodeBody reads a request's JSON body into in. A field that in does not
// name is refused, so a plugin learns that this cockpit does not know it yet.
func decodeBody(r *http.Request, in any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(in)
}

// decodeQuery reads a request's query parameters and the named path values
// into the string fields of in, by their JSON names. A parameter that in does
// not name, one given twice or one that repeats a path value is refused, like
// an unknown field of a body.
func decodeQuery(r *http.Request, in any, path ...string) error {
	values := map[string]string{}
	for key, given := range r.URL.Query() {
		if len(given) != 1 {
			return errors.New("a query parameter is given twice")
		}
		values[key] = given[0]
	}
	for _, key := range path {
		if _, ok := values[key]; ok {
			return errors.New("a query parameter repeats a path value")
		}
		values[key] = r.PathValue(key)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(in)
}
