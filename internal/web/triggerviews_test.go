package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/tmux"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// A trigger waiting for one coder leads to it the way a steered coder's row
// does: a running one opens its page, a stopped one resumes it. A guest only
// looks, its row keeps the link either way, and a trigger on any coder has
// no coder to lead to.
func TestAStoppedCodersTriggerRowResumesIt(t *testing.T) {
	const running, stopped = "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"
	dir := t.TempDir()
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"[ \"$1\" = list-panes ] && printf '" + running + "\\t123\\t1700000000\\t0\\t0\\t\\t\\tclaude\\trunner\\t" + dir + "\\t\\t\\t\\t\\t\\n'\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	sessions := &storedSessions{list: []coder.Session{
		{SessionID: running, Name: "runner", CWD: dir},
		{SessionID: stopped, Name: "stopper", CWD: dir},
	}}

	stateDir := t.TempDir()
	conversations, _, err := assistant.New(stateDir, noCoders{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	owner := "11111111-1111-4111-8111-111111111111"
	assistant.NewStore(stateDir).Save(assistant.Instance{Summary: assistant.Summary{ID: owner, Title: "One", CoderID: "claude"}})
	triggers := map[string]string{}
	for _, terminal := range []string{running, stopped, ""} {
		spec := assistant.TriggerSpec{Owner: owner, Event: "coder-news", Task: "say so"}
		if terminal != "" {
			spec.Targets, spec.TargetsSet = []assistant.TriggerTarget{{Terminal: terminal}}, true
		}
		trigger, err := conversations.Events().Add(spec)
		if err != nil {
			t.Fatalf("trigger on %q: %v", terminal, err)
		}
		triggers[terminal] = trigger.ID
	}
	s := &Server{
		coders:     []*coder.Manager{coder.NewManager(config.Config{}, tmux.New(), storedCoder{sessions: sessions}, nil)},
		assistants: conversations,
	}

	tmpl := render.HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	row := func(access render.Access, terminal string) string {
		t.Helper()
		var out strings.Builder
		if err := tmpl.ExecuteTemplate(&out, "assistant_triggers_list.gohtml", s.assistantTriggersData(render.Page{Access: access}, owner, false)); err != nil {
			t.Fatalf("render: %v", err)
		}
		html := out.String()
		at := strings.Index(html, `data-assistant-trigger="`+triggers[terminal]+`"`)
		if at < 0 {
			t.Fatalf("no row for %q", terminal)
		}
		rest := html[at+1:]
		if next := strings.Index(rest, `data-assistant-trigger="`); next >= 0 {
			rest = rest[:next]
		}
		return rest
	}

	if got := row(render.Access{}, stopped); !strings.Contains(got, `action="/coders/`+stopped+`/resume"`) ||
		!strings.Contains(got, "Resume coder") || strings.Contains(got, "data-assistant-trigger-open") ||
		!strings.Contains(got, "data-assistant-trigger-edit") || !strings.Contains(got, "data-assistant-trigger-remove") {
		t.Fatalf("a stopped coder's trigger row does not resume it: %s", got)
	}
	if got := row(render.Access{}, running); !strings.Contains(got, `href="/coders/`+running+`" class="btn btn-sm flex-fill" data-assistant-trigger-open`) ||
		strings.Contains(got, "/resume") {
		t.Fatalf("a running coder's trigger row does not open it: %s", got)
	}
	if got := row(render.Access{}, ""); strings.Contains(got, "data-assistant-trigger-open") || strings.Contains(got, "/resume") {
		t.Fatalf("a trigger on any coder leads to one: %s", got)
	}
	if got := row(render.Access{Guest: true}, stopped); !strings.Contains(got, "data-assistant-trigger-open") || strings.Contains(got, "/resume") {
		t.Fatalf("a guest's row of a stopped coder changed: %s", got)
	}
}
