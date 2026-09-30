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

// modelRuntime records what the start was told, so the test reads the model
// off the SessionStart the runtime got and never off a command line.
type modelRuntime struct {
	plainRuntime
	starts  []SessionStart
	started []SessionStart
	resumes []string
}

func (r *modelRuntime) StartCommand(start SessionStart) string {
	r.starts = append(r.starts, start)
	return "true"
}

func (r *modelRuntime) SessionStarted(start SessionStart) {
	r.started = append(r.started, start)
}

func (r *modelRuntime) ResumeCommand(sessionID, _ string, _ bool) string {
	r.resumes = append(r.resumes, sessionID)
	return "true"
}

func withFakeTmux(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
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

type checkingCoder struct {
	trustCoder
	refused string
}

type rememberingCoder struct {
	checkingCoder
	models map[string]string
}

func (c rememberingCoder) SessionModel(sessionID string) string { return c.models[sessionID] }

func TestResumeAsksTheCoderAboutTheRememberedModelBeforeAnythingExists(t *testing.T) {
	withoutTmux(t)
	root := t.TempDir()
	workdir := filepath.Join(root, "fresh-project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	rt := &modelRuntime{}
	sessions := []Session{{SessionID: "launched", Name: "one", CWD: workdir}, {SessionID: "plain", Name: "two", CWD: workdir}}
	c := rememberingCoder{
		checkingCoder: checkingCoder{trustCoder: trustCoder{runtime: rt, sessions: sessions}, refused: "ollama/qwen3.5:cloud"},
		models:        map[string]string{"launched": "ollama/qwen3.5:cloud"},
	}
	m := NewManager(config.Config{}, tmux.New(), c, project.NewRepository(root, nil))
	_, err := m.Resume("launched")
	if err == nil || !strings.Contains(err.Error(), "ollama executable") {
		t.Fatalf("want the coder's own refusal on the resume, got %v", err)
	}
	if len(rt.resumes) != 0 {
		t.Fatalf("a refused resume must never reach the runtime, got %v", rt.resumes)
	}
	if _, err := m.Resume("plain"); err == nil || strings.Contains(err.Error(), "ollama executable") {
		t.Fatalf("want a session without a remembered model past the check and failing at tmux alone, got %v", err)
	}
	if len(rt.resumes) != 1 || rt.resumes[0] != "plain" {
		t.Fatalf("want the unchecked session to reach the runtime, got %v", rt.resumes)
	}
}

func TestAStartIsAnnouncedToTheRuntimeOnlyOnceTmuxHoldsTheSession(t *testing.T) {
	withoutTmux(t)
	root := t.TempDir()
	workdir := filepath.Join(root, "fresh-project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	rt := &modelRuntime{}
	m := NewManager(config.Config{}, tmux.New(), trustCoder{runtime: rt}, project.NewRepository(root, nil))
	if _, err := m.Start("a name", workdir, "", StartOptions{Model: "ollama/qwen3.5:cloud"}); err == nil {
		t.Fatal("want the session start to fail with no tmux on PATH")
	}
	if len(rt.starts) != 1 || len(rt.started) != 0 {
		t.Fatalf("want the command built and the start never announced, got %d builds and %+v", len(rt.starts), rt.started)
	}
	withFakeTmux(t)
	if _, err := m.Start("a name", workdir, "", StartOptions{Model: "ollama/qwen3.5:cloud"}); err != nil {
		t.Fatal(err)
	}
	if len(rt.started) != 1 || rt.started[0].Model != "ollama/qwen3.5:cloud" || rt.started[0].SessionID != rt.starts[1].SessionID {
		t.Fatalf("want the start announced once tmux holds the session, with what the command was built from, got %+v", rt.started)
	}
}

func (c checkingCoder) Launcher(string) string     { return "" }
func (c checkingCoder) SessionModel(string) string { return "" }

func (c checkingCoder) CheckModel(name string) error {
	if name == c.refused {
		return errors.New("The ollama executable was not found on PATH.")
	}
	return nil
}

func TestStartAsksTheCoderAboutTheModelBeforeAnythingExists(t *testing.T) {
	withoutTmux(t)
	root := t.TempDir()
	workdir := filepath.Join(root, "fresh-project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	rt := &modelRuntime{}
	m := NewManager(config.Config{}, tmux.New(), checkingCoder{trustCoder: trustCoder{runtime: rt}, refused: "ollama/qwen3.5:cloud"}, project.NewRepository(root, nil))
	_, err := m.Start("a name", workdir, "", StartOptions{Model: "ollama/qwen3.5:cloud"})
	if err == nil || !strings.Contains(err.Error(), "ollama executable") {
		t.Fatalf("want the coder's own refusal, got %v", err)
	}
	if len(rt.starts) != 0 {
		t.Fatalf("a refused model must never reach the runtime, got %+v", rt.starts)
	}
	if _, err := m.Start("a name", workdir, "", StartOptions{Model: "haiku"}); err == nil {
		t.Fatal("want the session start to fail with no tmux on PATH")
	}
	if len(rt.starts) != 1 || rt.starts[0].Model != "haiku" {
		t.Fatalf("want an accepted model to reach the runtime, got %+v", rt.starts)
	}
}
