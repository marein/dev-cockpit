package coder

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/tmux"
)

type effortCoder struct {
	trustCoder
	levels []string
}

func (c effortCoder) Efforts() []string { return c.levels }

func (c effortCoder) CheckEffort(model, level string) error {
	if model == "lean" {
		return errors.New("lean takes no effort level.")
	}
	_, err := assistant.CleanEffortIn(level, c.levels)
	return err
}

// preparingRuntime records what a preparation was handed and refuses where
// told to, so a test reads whether a refused start ever reached the command.
type preparingRuntime struct {
	modelRuntime
	prepared []SessionStart
	refuse   error
}

func (r *preparingRuntime) PrepareStart(start SessionStart) (SessionStart, error) {
	r.prepared = append(r.prepared, start)
	start.AgentID = "settled"
	return start, r.refuse
}

func effortManager(t *testing.T, c Coder) (*Manager, string) {
	t.Helper()
	withoutTmux(t)
	root := t.TempDir()
	workdir := filepath.Join(root, "fresh-project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	return NewManager(config.Config{}, tmux.New(), c, project.NewRepository(root, nil)), workdir
}

// A level is checked by the coder's own rule before anything exists, a coder
// without levels refuses every one, and the stored start effort stands behind
// an empty pick only beside the start model it goes with: a session started
// on a model of its own runs at its own level or the CLI's. A start default
// the model does not take is refused with the place that fixes it.
func TestStartChecksTheEffortAndPairsTheStartDefaultWithItsModel(t *testing.T) {
	rt := &modelRuntime{}
	m, workdir := effortManager(t, effortCoder{trustCoder: trustCoder{runtime: rt}, levels: []string{"low", "high"}})
	if _, err := m.Start("a name", workdir, "", StartOptions{Effort: "max"}); err == nil || !strings.Contains(err.Error(), "low, high") {
		t.Fatalf("want a level the coder does not take refused with its levels, got %v", err)
	}
	if len(rt.starts) != 0 {
		t.Fatalf("a refused level reached the runtime: %+v", rt.starts)
	}
	start := assistant.ModelDefaults{Start: "opus", StartEffort: "low", ChatEffort: "never-a-session"}
	m.SetModelDefaults(func() assistant.ModelDefaults { return start })
	m.Start("a name", workdir, "", StartOptions{})
	m.Start("a name", workdir, "", StartOptions{Effort: " high "})
	m.Start("a name", workdir, "", StartOptions{Model: "sonnet"})
	start = assistant.ModelDefaults{StartEffort: "low"}
	m.Start("a name", workdir, "", StartOptions{})
	want := []SessionStart{{Model: "opus", Effort: "low"}, {Model: "opus", Effort: "high"}, {Model: "sonnet"}, {Effort: "low"}}
	if len(rt.starts) != len(want) {
		t.Fatalf("want %d starts, got %+v", len(want), rt.starts)
	}
	for i, w := range want {
		if rt.starts[i].Model != w.Model || rt.starts[i].Effort != w.Effort {
			t.Fatalf("start %d: want %s at %q, got %+v", i, w.Model, w.Effort, rt.starts[i])
		}
	}
	start = assistant.ModelDefaults{Start: "lean", StartEffort: "low"}
	if _, err := m.Start("a name", workdir, "", StartOptions{}); err == nil || err.Error() != "lean takes no effort level. Change the start effort under Settings, Coder, Models." {
		t.Fatalf("want the start default refused with its place, got %v", err)
	}

	plain, workdir := effortManager(t, trustCoder{runtime: &modelRuntime{}})
	if _, err := plain.Start("a name", workdir, "", StartOptions{Effort: "high"}); err == nil || !strings.Contains(err.Error(), "takes no effort level") {
		t.Fatalf("want a coder without levels to refuse one, got %v", err)
	}
}

// A runtime that has to put the level in place does it before its command is
// built, and a refusal there ends the start before anything exists.
func TestAPreparationRunsBeforeTheCommandAndItsRefusalStopsTheStart(t *testing.T) {
	rt := &preparingRuntime{refuse: errors.New("that model takes no effort level")}
	m, workdir := effortManager(t, effortCoder{trustCoder: trustCoder{runtime: rt}, levels: []string{"high"}})
	if _, err := m.Start("a name", workdir, "", StartOptions{Model: "x/y", Effort: "high"}); err == nil || err.Error() != "that model takes no effort level" {
		t.Fatalf("want the preparation's refusal, got %v", err)
	}
	if len(rt.prepared) != 1 || rt.prepared[0].Model != "x/y" || rt.prepared[0].Effort != "high" || len(rt.starts) != 0 {
		t.Fatalf("want the start prepared and never built, got %+v and %+v", rt.prepared, rt.starts)
	}
	rt.refuse = nil
	m.Start("a name", workdir, "", StartOptions{Model: "x/y", Effort: "high"})
	if len(rt.prepared) != 2 || len(rt.starts) != 1 || rt.starts[0].AgentID != "settled" {
		t.Fatalf("want the start built from what the preparation settled, got %+v and %+v", rt.prepared, rt.starts)
	}
}
