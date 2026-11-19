package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/plugin"
)

//go:embed assets memory
var pulseFiles embed.FS

// pulse is optional. While on, a goroutine counts beats into its state
// directory, its page keeps lines and the events it heard and starts
// assistants on a listed coder and reads them, its button shows the beats,
// and it adds a setting and a memory file. Everything it starts ends with
// its Serve's context, so nothing runs while it is off.
type pulse struct {
	mu sync.Mutex
}

const (
	maxLines  = 200
	maxEvents = 20
)

func (p *pulse) ConfigureServe(s plugin.Serve) error {
	dir := s.PluginStateDir()
	go beat(s.Context(), filepath.Join(dir, "heartbeat"))
	assets, err := fs.Sub(pulseFiles, "assets")
	if err != nil {
		return err
	}
	memory, err := fs.Sub(pulseFiles, "memory")
	if err != nil {
		return err
	}
	s.AddAssets(assets)
	board := s.AddCustomElement("board", "board.js")
	button := s.AddCustomElement("button", "button.js")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /lines", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"lines": p.read(dir, "lines.json")})
	})
	mux.HandleFunc("POST /lines", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" || len(body.Text) > 500 {
			http.Error(w, "A line is one to 500 bytes of text.", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"lines": p.add(dir, "lines.json", strings.TrimSpace(body.Text), maxLines)})
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"events": p.read(dir, "events.json")})
	})
	mux.HandleFunc("GET /beats", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := os.ReadFile(filepath.Join(dir, "heartbeat"))
		beats, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		writeJSON(w, map[string]int{"beats": beats})
	})
	assistants := s.Assistants()
	mux.HandleFunc("GET /assistants", func(w http.ResponseWriter, r *http.Request) {
		list, err := assistants.List(r.Context(), plugin.AssistantQuery{})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err)
			return
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("GET /assistants/coders", func(w http.ResponseWriter, r *http.Request) {
		list, err := assistants.Coders(r.Context(), plugin.AssistantCoderQuery{})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err)
			return
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("POST /assistants", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name    string `json:"name"`
			Message string `json:"message"`
			CoderID string `json:"coder_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, errors.New("Send a name and a message."))
			return
		}
		made, err := assistants.Create(r.Context(), plugin.AssistantSpec{Name: body.Name, Message: body.Message, CoderID: body.CoderID})
		if made.ID == "" {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		answer := map[string]any{"assistant": made}
		if err != nil {
			answer["error"] = err.Error()
		}
		writeJSON(w, answer)
	})
	mux.HandleFunc("GET /assistants/{id}/last-message", func(w http.ResponseWriter, r *http.Request) {
		last, err := assistants.LastMessage(r.Context(), plugin.AssistantMessageQuery{AssistantID: r.PathValue("id")})
		switch {
		case errors.Is(err, plugin.ErrAssistantNotFound):
			writeError(w, http.StatusNotFound, err)
		case err != nil:
			writeError(w, http.StatusServiceUnavailable, err)
		default:
			writeJSON(w, last)
		}
	})
	routes := s.AddRoutes(mux)
	s.AddSlotHTML(plugin.SlotProjectsActions, fmt.Sprintf(
		`<%[1]s data-beats-url="%[2]s"><a class="btn btn-icon btn-ghost-secondary" href="%[3]s" title="Pulse" aria-label="Pulse" data-pulse-button><i class="ti ti-activity" aria-hidden="true"></i></a></%[1]s>`,
		button.Name(), html.EscapeString(routes.Path("/beats")), html.EscapeString(routes.Path("/"))))
	s.AddPage(plugin.Page{Title: "Pulse", Icon: "ti-activity", HTML: fmt.Sprintf(`<%[1]s class="d-block dc-narrow" data-lines-url="%[2]s" data-events-url="%[3]s" data-beats-url="%[4]s" data-assistants-url="%[5]s">
  <p class="text-secondary" data-pulse-beats>Beating while the plugin is on.</p>
  <form class="d-flex gap-2 mb-3" data-pulse-form data-no-pe autocomplete="off">
    <input type="text" class="form-control" name="text" maxlength="500" required placeholder="Write a line" aria-label="Line">
    <button type="submit" class="btn btn-primary">Add</button>
  </form>
  <div class="list-group" data-pulse-lines></div>
  <h3 class="mt-4">Assistants</h3>
  <form class="d-flex flex-column gap-2 mb-3" data-pulse-assistant-form data-no-pe autocomplete="off">
    <input type="text" class="form-control" name="name" maxlength="120" placeholder="Name" aria-label="Assistant name">
    <select class="form-select" name="coder_id" aria-label="Coder" data-pulse-coders></select>
    <textarea class="form-control" name="message" rows="2" required placeholder="First message" aria-label="First message"></textarea>
    <div><button type="submit" class="btn btn-primary">Start</button></div>
  </form>
  <div class="list-group" data-pulse-assistants></div>
  <h3 class="mt-4">Events heard</h3>
  <div class="list-group" data-pulse-events></div>
</%[1]s>`, board.Name(), html.EscapeString(routes.Path("/lines")), html.EscapeString(routes.Path("/events")), html.EscapeString(routes.Path("/beats")), html.EscapeString(routes.Path("/assistants")))})
	s.AddSettings(map[string]string{"pulse-greeting": "Hello from pulse"}, false)
	s.AddAssistantMemory(memory, false)
	s.AddEventHandler([]string{plugin.EventJob, plugin.EventCoder, plugin.EventCompose}, func(ev plugin.Event) {
		p.add(dir, "events.json", strings.TrimSpace(ev.Source+": "+ev.Headline+" "+ev.Target), maxEvents)
	})
	return nil
}

// beat counts up into path every quarter second until ctx ends.
func beat(ctx context.Context, path string) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	raw, _ := os.ReadFile(path)
	beats, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			beats++
			_ = os.WriteFile(path, []byte(strconv.Itoa(beats)), 0o600)
		}
	}
}

func (p *pulse) read(dir, name string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return readList(filepath.Join(dir, name))
}

// add puts one entry first into a list file and keeps the newest max.
func (p *pulse) add(dir, name, entry string, max int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	path := filepath.Join(dir, name)
	list := append([]string{entry}, readList(path)...)
	if len(list) > max {
		list = list[:max]
	}
	if raw, err := json.Marshal(list); err == nil {
		_ = os.WriteFile(path, raw, 0o600)
	}
	return list
}

func readList(path string) []string {
	list := []string{}
	raw, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(raw, &list)
	}
	return list
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
