package restore

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/statefile"
	"github.com/marein/dev-cockpit/internal/tmux"
)

const (
	plainID    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	launchedID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

type launcherCoder struct {
	coder.Coder
	sessions []coder.Session
}

func (c launcherCoder) ID() string                           { return "fake" }
func (c launcherCoder) SessionRuntime() coder.SessionRuntime { return resumeRuntime{} }
func (c launcherCoder) SessionRepository() coder.SessionRepository {
	return listed{sessions: c.sessions}
}
func (c launcherCoder) CheckModel(string) error { return errors.New("the server does not answer") }

func (c launcherCoder) Launcher(model string) string {
	if strings.HasPrefix(model, "ollama/") {
		return "ollama"
	}
	return ""
}

func (c launcherCoder) SessionModel(sessionID string) string {
	if sessionID == launchedID {
		return "ollama/qwen3.5:cloud"
	}
	return ""
}

type listed struct {
	coder.SessionRepository
	sessions []coder.Session
}

func (l listed) List() []coder.Session { return l.sessions }

type resumeRuntime struct{}

func (resumeRuntime) UsesProvidedSessionID() bool               { return true }
func (resumeRuntime) StartCommand(coder.SessionStart) string    { return "true" }
func (resumeRuntime) ResumeCommand(string, string, bool) string { return "true" }
func (resumeRuntime) Env() map[string]string                    { return nil }

func withTmuxRecordingSessions(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	state := t.TempDir()
	script := `#!/bin/sh
case "$1" in
new-session)
	while [ $# -gt 0 ]; do
		if [ "$1" = -s ]; then echo "$2" >> "` + state + `/sessions"; fi
		shift
	done ;;
list-panes)
	if [ -f "` + state + `/sessions" ]; then
		while read -r name; do printf '%s\t1\t1700000000\t0\t0\t\t\tfake\t%s\t/tmp\t\t\t\t\t\n' "$name" "$name"; done < "` + state + `/sessions"
	fi ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return filepath.Join(state, "sessions")
}

func resumed(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

func restoring(t *testing.T, ready func(string, time.Duration) bool) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "terminal-restore.json")
	cwd := t.TempDir()
	statefile.Save(path, 0o644, snapshot{Terminals: []Entry{
		{Kind: "coder", Coder: "fake", ID: launchedID, Name: "launched", CWD: cwd},
		{Kind: "coder", Coder: "fake", ID: plainID, Name: "plain", CWD: cwd},
	}})
	c := launcherCoder{sessions: []coder.Session{{SessionID: plainID, Name: "plain", CWD: cwd}, {SessionID: launchedID, Name: "launched", CWD: cwd}}}
	projects := project.NewRepository(t.TempDir(), nil)
	m := coder.NewManager(config.Config{}, tmux.New(), c, projects)
	s := New(path, func() bool { return true }, []*coder.Manager{m}, shell.NewShells(config.Config{}, tmux.New(), projects, nil), tmux.New(),
		notify.NewService(filepath.Join(dir, "notifications.json"), nil), nil, func() []string { return nil })
	s.SetLauncherReady(ready)
	return s, path
}

func settle(t *testing.T, s *Service) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		holding := s.holding
		s.mu.Unlock()
		if !holding {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the restore never let go of the snapshot")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func snapshotIDs(t *testing.T, path string) []string {
	t.Helper()
	var snap snapshot
	statefile.Load(path, &snap)
	var ids []string
	for _, e := range snap.Terminals {
		ids = append(ids, e.ID)
	}
	slices.Sort(ids)
	return ids
}

func TestTheRestoreResumesALaunchedCoderOnceItsServerAnswers(t *testing.T) {
	sessions := withTmuxRecordingSessions(t)
	answer := make(chan struct{})
	asked := make(chan time.Duration, 1)
	s, path := restoring(t, func(launcher string, timeout time.Duration) bool {
		if launcher != "ollama" {
			t.Errorf("want the session's launcher asked, got %q", launcher)
		}
		asked <- timeout
		<-answer
		return true
	})
	s.RunStartup()
	if timeout := <-asked; timeout <= 0 || timeout > launcherWait {
		t.Fatalf("want the wait bounded by the restore's own timeout, got %s", timeout)
	}
	if got := resumed(t, sessions); !slices.Equal(got, []string{plainID}) {
		t.Fatalf("want nothing but the plain coder resumed while the server does not answer, got %v", got)
	}
	s.Write()
	if got := snapshotIDs(t, path); !slices.Equal(got, []string{plainID, launchedID}) {
		t.Fatalf("want the snapshot left alone while the launched coder waits, got %v", got)
	}
	close(answer)
	settle(t, s)
	if got := resumed(t, sessions); !slices.Equal(got, []string{plainID, launchedID}) {
		t.Fatalf("want the launched coder resumed after the server answered, got %v", got)
	}
	if got := snapshotIDs(t, path); !slices.Equal(got, []string{plainID, launchedID}) {
		t.Fatalf("want both coders in the snapshot written after the wait, got %v", got)
	}
}

func TestTheRestoreResumesALaunchedCoderAfterTheWaitAnyway(t *testing.T) {
	sessions := withTmuxRecordingSessions(t)
	var waited time.Duration
	s, path := restoring(t, func(_ string, timeout time.Duration) bool {
		waited = timeout
		return false
	})
	s.RunStartup()
	settle(t, s)
	if waited <= 0 || waited > launcherWait {
		t.Fatalf("want the launcher given the restore's timeout, got %s", waited)
	}
	if got := resumed(t, sessions); !slices.Equal(got, []string{plainID, launchedID}) {
		t.Fatalf("want the launched coder resumed after the timeout, got %v", got)
	}
	if got := snapshotIDs(t, path); !slices.Equal(got, []string{plainID, launchedID}) {
		t.Fatalf("want the launched coder kept in the snapshot, got %v", got)
	}
}

func TestTheRestoreResumesPlainCodersBeforeTheWait(t *testing.T) {
	sessions := withTmuxRecordingSessions(t)
	var atTheWait []string
	s, _ := restoring(t, func(string, time.Duration) bool {
		atTheWait = resumed(t, sessions)
		return true
	})
	s.RunStartup()
	settle(t, s)
	if !slices.Equal(atTheWait, []string{plainID}) {
		t.Fatalf("want the plain coder resumed before the wait began, got %v", atTheWait)
	}
	if got := resumed(t, sessions); !slices.Equal(got, []string{plainID, launchedID}) {
		t.Fatalf("want the plain coder first and the launched one after the server answered, got %v", got)
	}
}
