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
		while read -r name; do printf '%s\t1\t1700000000\t0\t0\t\t\tfake\t%s\t/tmp\t\t\t\t\t\t\n' "$name" "$name"; done < "` + state + `/sessions"
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
		notify.NewService(filepath.Join(dir, "notifications.json"), nil), nil, func() []string { return nil }, func(string) bool { return false })
	s.SetLauncherReady(ready)
	return s, path
}

// settle waits for the startup goroutine's last step, the snapshot written
// again once it let go of it. Returning on the release alone let that write
// land in the test's directory while its cleanup removed it.
func settle(t *testing.T, s *Service) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		written := !s.holding && s.last != nil
		s.mu.Unlock()
		if written {
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

// A project's news that is no terminal survives the startup prune while the
// project is there, a failed pipeline's among it, and goes with the project.
func TestThePruneKeepsTheNewsOfProjectsThatAreThere(t *testing.T) {
	withTmuxRecordingSessions(t)
	dir := t.TempDir()
	notifier := notify.NewService(filepath.Join(dir, "notifications.json"), nil)
	for _, target := range []string{notify.PipelineTarget("shop"), notify.DockerTarget("shop"), notify.PipelineTarget("gone")} {
		notifier.AddResolved(target, notify.TargetInfo{Name: target})
	}
	projects := project.NewRepository(t.TempDir(), nil)
	s := New(filepath.Join(dir, "terminal-restore.json"), func() bool { return false }, nil, shell.NewShells(config.Config{}, tmux.New(), projects, nil), tmux.New(),
		notifier, nil, func() []string { return []string{"shop"} }, func(string) bool { return false })
	s.pruneDeadTargets()
	var kept []string
	for _, n := range notifier.List(0) {
		kept = append(kept, n.TargetID)
	}
	slices.Sort(kept)
	if want := []string{notify.DockerTarget("shop"), notify.PipelineTarget("shop")}; !slices.Equal(kept, want) {
		t.Fatalf("want %v kept, got %v", want, kept)
	}
}

// withTmuxListing stands a tmux in the path that lists exactly these panes.
func withTmuxListing(t *testing.T, panes ...string) {
	t.Helper()
	bin := t.TempDir()
	listing := filepath.Join(bin, "panes")
	if err := os.WriteFile(listing, []byte(strings.Join(panes, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = list-panes ]; then cat '" + listing + "'; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
}

// pruned runs the startup prune over one entry per target, the ones in read
// marked read first, and answers what is left, newest first.
func pruned(t *testing.T, s *Service, targets []string, read map[string]bool) []notify.Notification {
	t.Helper()
	for _, target := range targets {
		s.notifier.AddResolved(target, notify.TargetInfo{Name: target})
		if read[target] {
			s.notifier.MarkTargetRead(target)
		}
	}
	s.pruneDeadTargets()
	return s.notifier.List(0)
}

// The startup prune asks every kind of target whether it still lives, and a
// restart deletes the news of none that does: a terminal lives while tmux
// runs it or its coder can resume it, the backup always, what names a project
// while the project is there, and an assistant until it is deleted. The
// question of an approval or of the ssh agent stood in the process that is
// gone, so it is dead at every start. What stays keeps its read state.
func TestThePruneKeepsEveryTargetThatStillLives(t *testing.T) {
	const (
		running   = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
		resumable = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
		shellID   = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
		dead      = "ffffffff-ffff-4fff-8fff-ffffffffffff"
		helper    = "11111111-1111-4111-8111-111111111111"
		deleted   = "22222222-2222-4222-8222-222222222222"
	)
	withTmuxListing(t,
		running+"\t1\t1700000000\t0\t0\t\t\tfake\tcoder\t/tmp\t\t\t\t\t\t",
		shellID+"\t1\t1700000000\t0\t0\tbash\t/tmp\t\t\t\t\t\t\t\t\t",
	)
	dir := t.TempDir()
	projects := project.NewRepository(t.TempDir(), nil)
	c := launcherCoder{sessions: []coder.Session{{SessionID: resumable, Name: "resumable", CWD: dir}}}
	m := coder.NewManager(config.Config{}, tmux.New(), c, projects)
	s := New(filepath.Join(dir, "terminal-restore.json"), func() bool { return false }, []*coder.Manager{m},
		shell.NewShells(config.Config{}, tmux.New(), projects, nil), tmux.New(),
		notify.NewService(filepath.Join(dir, "notifications.json"), nil), nil,
		func() []string { return []string{"shop"} }, func(id string) bool { return id == helper })

	lives := []string{
		running, resumable, shellID, notify.BackupTarget, helper,
		notify.DockerTarget("shop"), notify.GitPromptTarget("shop"), notify.WorktreeTarget("shop"),
		notify.PipelineTarget("shop"), notify.CommentsTarget("shop"),
	}
	gone := []string{
		dead, deleted,
		notify.DockerTarget("gone"), notify.GitPromptTarget("gone"), notify.WorktreeTarget("gone"),
		notify.PipelineTarget("gone"), notify.CommentsTarget("gone"),
		notify.ApprovalTarget("3f2a"), notify.SSHKeyTarget("9b1c"),
	}
	read := map[string]bool{}
	for i, target := range lives {
		read[target] = i%2 == 0
	}
	left := pruned(t, s, append(slices.Clone(lives), gone...), read)

	var kept []string
	for _, n := range left {
		kept = append(kept, n.TargetID)
		if n.Read != read[n.TargetID] {
			t.Errorf("want %s kept with its read state %v, got %v", n.TargetID, read[n.TargetID], n.Read)
		}
	}
	slices.Reverse(kept)
	if !slices.Equal(kept, lives) {
		t.Fatalf("want the news of every target that lives and of no other:\n%v\ngot\n%v", lives, kept)
	}
	if s.pruneDeadTargets(); len(s.notifier.List(0)) != len(lives) {
		t.Fatalf("want a second start to take nothing, got %+v", s.notifier.List(0))
	}
}

// An assistant's news survives every restart with its read state, the line
// that was read and the one that still waits, and goes with the assistant.
func TestAnAssistantsNewsSurvivesARestartUntilItIsDeleted(t *testing.T) {
	const helper = "11111111-1111-4111-8111-111111111111"
	withTmuxListing(t)
	dir := t.TempDir()
	projects := project.NewRepository(t.TempDir(), nil)
	path := filepath.Join(dir, "notifications.json")
	lives := true
	start := func() *notify.Service {
		notifier := notify.NewService(path, nil)
		New(filepath.Join(dir, "terminal-restore.json"), func() bool { return false }, nil,
			shell.NewShells(config.Config{}, tmux.New(), projects, nil), tmux.New(), notifier, nil,
			func() []string { return nil }, func(id string) bool { return lives && id == helper }).RunStartup()
		return notifier
	}
	notifier := start()
	notifier.AddResolved(helper, notify.TargetInfo{Title: "Job done."})
	notifier.MarkTargetRead(helper)
	notifier.AddResolved(helper, notify.TargetInfo{Title: "Answer ready."})
	before := notifier.List(0)
	if len(before) != 2 || before[0].Read || !before[1].Read {
		t.Fatalf("want an unread line over a read one, got %+v", before)
	}
	for restart := range 2 {
		notifier = start()
		if got := notifier.List(0); !slices.Equal(got, before) || notifier.UnreadCount() != 1 {
			t.Fatalf("restart %d: want both lines as they stood, got %+v", restart, got)
		}
	}
	lives = false
	if got := start().List(0); len(got) != 0 {
		t.Fatalf("want the news of a deleted assistant gone at the next start, got %+v", got)
	}
}
