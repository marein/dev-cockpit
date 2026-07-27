package coder

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/tmux"
)

// withoutTmux makes the tmux call these tests rely on fail, whatever the
// machine has installed. The client runs `tmux` through PATH at the moment it
// starts a session, so an empty PATH is a tmux that cannot be found: the test
// produces its own failure instead of assuming the host has no tmux, which is
// false on every machine the cockpit actually runs on.
func withoutTmux(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// A coder that comes up in a directory its CLI has never seen asks whether the
// files there are trusted, before it reads the task out of its argv. So the
// directory is trusted before the session exists, not after: these two check
// that the trust is already written when the session start is reached, by
// letting the start itself fail and looking at what the runtime was told
// anyway.
func TestANewSessionTrustsItsWorkdirBeforeItStarts(t *testing.T) {
	withoutTmux(t)
	root := t.TempDir()
	workdir := filepath.Join(root, "fresh-project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	rt := &recordingRuntime{}
	m := NewManager(config.Config{}, tmux.New(), trustCoder{runtime: rt}, project.NewRepository(root, nil))

	if _, err := m.Start("a name", workdir, "", StartOptions{Task: "do the thing"}); err == nil {
		t.Fatal("want the session start to fail with no tmux on PATH")
	}
	if len(rt.trusted) != 1 || rt.trusted[0] != workdir {
		t.Fatalf("trusted = %v, want [%s] before the session is started", rt.trusted, workdir)
	}
}

func TestAResumedSessionTrustsItsWorkdirToo(t *testing.T) {
	withoutTmux(t)
	workdir := t.TempDir()
	id := "11111111-1111-4111-8111-111111111111"
	rt := &recordingRuntime{}
	stored := Session{SessionID: id, Name: "a name", CWD: workdir}
	m := NewManager(config.Config{}, tmux.New(), trustCoder{runtime: rt, sessions: []Session{stored}}, nil)

	if _, err := m.Resume(id); err == nil {
		t.Fatal("want the resume to fail with no tmux on PATH")
	}
	if len(rt.trusted) != 1 || rt.trusted[0] != workdir {
		t.Fatalf("trusted = %v, want [%s] before the session is resumed", rt.trusted, workdir)
	}
}

// A runtime whose CLI has no trust state is left out of the path entirely.
func TestARuntimeWithoutTrustIsSkipped(t *testing.T) {
	withoutTmux(t)
	workdir := t.TempDir()
	id := "22222222-2222-4222-8222-222222222222"
	m := NewManager(config.Config{}, tmux.New(),
		trustCoder{runtime: plainRuntime{}, sessions: []Session{{SessionID: id, Name: "n", CWD: workdir}}}, nil)

	if _, err := m.Resume(id); err == nil {
		t.Fatal("want the resume to fail with no tmux on PATH")
	}
}

type recordingRuntime struct {
	plainRuntime
	trusted []string
}

func (r *recordingRuntime) TrustWorkdir(workdir string) error {
	r.trusted = append(r.trusted, workdir)
	return nil
}

type plainRuntime struct{}

func (plainRuntime) UsesProvidedSessionID() bool               { return true }
func (plainRuntime) StartCommand(SessionStart) string          { return "true" }
func (plainRuntime) ResumeCommand(string, string, bool) string { return "true" }
func (plainRuntime) Env() map[string]string                    { return nil }

type trustCoder struct {
	Coder
	runtime  SessionRuntime
	sessions []Session
}

func (c trustCoder) ID() string                           { return "trust" }
func (c trustCoder) SessionRuntime() SessionRuntime       { return c.runtime }
func (c trustCoder) SessionRepository() SessionRepository { return listSessions{sessions: c.sessions} }
func (c trustCoder) AgentRepository() AgentRepository     { return noAgents{} }

type listSessions struct {
	SessionRepository
	sessions []Session
}

func (s listSessions) List() []Session { return s.sessions }

type noAgents struct{ AgentRepository }

func (noAgents) ValidateSelected(raw string) (string, error) { return raw, nil }
