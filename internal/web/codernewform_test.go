package web

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
	codercodex "github.com/marein/dev-cockpit/internal/coder/codex"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// A coder that starts no session as one of its agents keeps its Agent field
// in the form, saying so instead of offering a pick that would be refused,
// and another coder keeps its select.
func TestTheAgentFieldSaysWhenACoderStartsNoSessionAsAnAgent(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	r, s := modelSettingsServer(t)
	s.coders = append(s.coders, coder.NewManager(config.Config{}, tmux.New(), codercodex.New(s.settings), s.projects))
	r.GET("/coders/new", s.handleCoderNew)

	page := getPage(t, r, "/coders/new?coder=codex&agent=reviewer")
	note := `<div class="form-control-plaintext text-secondary" data-agent-note>Not available for Codex, it runs its agents only as subagents.</div>`
	if !strings.Contains(page, `data-coder-agents="codex" >`) || !strings.Contains(page, note) {
		t.Fatalf("want codex's Agent field shown with its note, got %s", page)
	}
	if strings.Contains(page, `id="agent-codex"`) || strings.Count(page, `name="agent"`) != 1 {
		t.Fatal("want nothing to pick for codex and claude's select alone")
	}
	if !strings.Contains(page, `data-coder-agents="claude" hidden>`) || !strings.Contains(page, `id="agent-claude" name="agent" disabled>`) {
		t.Fatal("want claude's agent select kept, hidden and off while codex is picked")
	}

	page = getPage(t, r, "/coders/new?coder=claude")
	if strings.Count(page, "data-agent-note") != 1 || !strings.Contains(page, `id="agent-claude" name="agent" >`) {
		t.Fatal("want claude's select live and codex's note alone when claude is picked")
	}
}
