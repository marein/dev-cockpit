package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// modelRuntime records what the start was told, so the test reads the model
// off the SessionStart the runtime got and never off a command line.
type modelRuntime struct {
	plainRuntime
	starts []SessionStart
}

func (r *modelRuntime) StartCommand(start SessionStart) string {
	r.starts = append(r.starts, start)
	return "true"
}

// The model is checked with the shared rule before anything exists: a name no
// CLI takes is refused with that rule's own sentence and the runtime never
// hears of the start, while a name that passes reaches the runtime cleaned,
// and no model reaches it as none.
func TestStartChecksTheModelBeforeAnythingExists(t *testing.T) {
	withoutTmux(t)
	root := t.TempDir()
	workdir := filepath.Join(root, "fresh-project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	rt := &modelRuntime{}
	m := NewManager(config.Config{}, tmux.New(), trustCoder{runtime: rt}, project.NewRepository(root, nil))

	_, err := m.Start("a name", workdir, "", StartOptions{Model: "two words"})
	if err == nil || !strings.Contains(err.Error(), "no spaces") {
		t.Fatalf("want the shared rule's own refusal, got %v", err)
	}
	if len(rt.starts) != 0 {
		t.Fatalf("a refused model must never reach the runtime, got %+v", rt.starts)
	}

	if _, err := m.Start("a name", workdir, "", StartOptions{Model: " haiku "}); err == nil {
		t.Fatal("want the session start to fail with no tmux on PATH")
	}
	if _, err := m.Start("a name", workdir, "", StartOptions{}); err == nil {
		t.Fatal("want the session start to fail with no tmux on PATH")
	}
	if len(rt.starts) != 2 || rt.starts[0].Model != "haiku" || rt.starts[1].Model != "" {
		t.Fatalf("want the model cleaned on the start and empty without one, got %+v", rt.starts)
	}
}

// A start without a pick takes the coder's stored start default, read on
// every start so a change applies to the next session, a pick stands above
// it, and no other purpose's default reaches a session.
func TestStartTakesTheStartDefaultBehindAnEmptyPick(t *testing.T) {
	withoutTmux(t)
	root := t.TempDir()
	workdir := filepath.Join(root, "fresh-project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	rt := &modelRuntime{}
	m := NewManager(config.Config{}, tmux.New(), trustCoder{runtime: rt}, project.NewRepository(root, nil))
	start := "fable"
	m.SetModelDefaults(func() assistant.ModelDefaults {
		return assistant.ModelDefaults{Start: start, Chat: "never-a-session"}
	})
	m.Start("a name", workdir, "", StartOptions{})
	m.Start("a name", workdir, "", StartOptions{Model: "haiku"})
	start = "opus"
	m.Start("a name", workdir, "", StartOptions{})
	start = ""
	m.Start("a name", workdir, "", StartOptions{})
	if len(rt.starts) != 4 || rt.starts[0].Model != "fable" || rt.starts[1].Model != "haiku" || rt.starts[2].Model != "opus" || rt.starts[3].Model != "" {
		t.Fatalf("want the start default behind an empty pick, read fresh, got %+v", rt.starts)
	}
}
