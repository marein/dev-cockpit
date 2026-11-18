package pluginhost

import (
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

// maxCallbackBody bounds what a plugin may send the cockpit in one call.
const maxCallbackBody = 64 << 10

// callbackHandler serves one external plugin's callback socket: the readers
// of plugin.Serve, its Projects surface and its events, scoped to this
// plugin. The socket's directory is the owner's alone, the bearer token tells
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
			writeError(w, http.StatusBadRequest, cut(err.Error()))
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
			writeError(w, http.StatusInternalServerError, cut(err.Error()))
			return
		}
		w.WriteHeader(http.StatusNoContent)
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

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
