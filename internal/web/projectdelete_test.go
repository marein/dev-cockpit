package web

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/restore"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/statefile"
	"github.com/marein/dev-cockpit/internal/tmux"
)

func TestStacksToStopTakesOnlyStacksWithContainers(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	if err := os.MkdirAll(filepath.Join(app, "ops"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A compose file with nothing running under it: there is nothing to bring
	// down, so the deletion stays the immediate one.
	if err := os.WriteFile(filepath.Join(app, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := docker.State{Available: true, Containers: []docker.Container{
		{Name: "ops-web-1", Project: "ops", WorkingDir: filepath.Join(app, "ops")},
		{Name: "other-1", WorkingDir: filepath.Join(root, "other")},
	}}
	got := stacksToStop(state, app)
	if len(got) != 1 || got[0].Dir != filepath.Join(app, "ops") {
		t.Fatalf("stacksToStop answered %v", got)
	}
	// The stack carries the compose project name the down has to say, which
	// comes off the containers and not off the directory.
	if got[0].Project != "ops" {
		t.Fatalf("the stack lost its compose project name: %+v", got[0])
	}
	if got := stacksToStop(docker.State{}, app); len(got) != 0 {
		t.Fatalf("a cockpit without containers answered %v", got)
	}
}

func TestProjectDeletesClaimAndFailure(t *testing.T) {
	deletes := newProjectDeletes(t.TempDir())
	projects := []project.Project{{Name: "app"}, {Name: "other"}}
	if deletes.view(projects) != nil {
		t.Fatal("an idle tracker rendered something")
	}
	if !deletes.start("app", "/projects/app") {
		t.Fatal("the first claim was refused")
	}
	if deletes.start("app", "/projects/app") {
		t.Fatal("a second claim was handed out while one ran")
	}
	view := deletes.view(projects)
	if !view["app"].Running || view["app"].Failed != "" {
		t.Fatalf("the row does not read as working: %+v", view)
	}
	if _, ok := view["other"]; ok {
		t.Fatal("an untouched project rendered a deletion")
	}

	deletes.finish("app", "permission denied")
	view = deletes.view(projects)
	if view["app"].Running || view["app"].Failed != "permission denied" {
		t.Fatalf("the failure does not stand on the row: %+v", view)
	}
	// A retry clears the previous attempt's word before it starts.
	if !deletes.start("app", "/projects/app") {
		t.Fatal("a finished deletion still held its claim")
	}
	if view := deletes.view(projects); !view["app"].Running || view["app"].Failed != "" {
		t.Fatalf("the retry kept the old failure: %+v", view)
	}
	deletes.finish("app", "")
	if deletes.view(projects) != nil {
		t.Fatal("a clean deletion left state behind")
	}
}

// A deletion the last process was in the middle of is on disk, so the row of
// the next one says it is working before anything else happens, and the work
// itself is there to be taken up again.
func TestAProjectDeletionSurvivesARestart(t *testing.T) {
	stateDir := t.TempDir()
	gone := newProjectDeletes(stateDir)
	gone.start("app", "/projects/app")

	restarted := newProjectDeletes(stateDir)
	if view := restarted.view([]project.Project{{Name: "app"}}); !view["app"].Running {
		t.Fatalf("the row came back without its deletion: %+v", view)
	}
	if pending := restarted.pending(); pending["app"] != "/projects/app" {
		t.Fatalf("pending answered %v", pending)
	}
}

// deletionServer is a server with just what deleteProjectWithCompose walks
// through: the purge over coders and shells, the docker service, the restore
// snapshot the terminals event rewrites, and the notification store the
// assertions read.
func deletionServer(t *testing.T, stateDir string, projects *project.Repository) *Server {
	t.Helper()
	shells := shell.NewShells(config.Config{}, tmux.New(), projects, nil)
	notifier := notify.NewService(filepath.Join(stateDir, "notifications.json"), nil)
	s := &Server{
		cfg:          config.Config{StateDir: stateDir},
		projects:     projects,
		shells:       shells,
		notifier:     notifier,
		docker:       docker.NewService(stateDir, func() string { return "" }),
		bus:          eventbus.New(),
		quickOpen:    filesystem.NewQuickOpenCache(),
		commitDrafts: newCommitDrafts(stateDir),
		lineComments: newLineComments(stateDir),
		deletes:      newProjectDeletes(stateDir),
	}
	s.restorer = restore.New(filepath.Join(stateDir, "terminal-restore.json"), func() bool { return false },
		nil, shells, tmux.New(), notifier, nil, func() []string { return nil })
	return s
}

// fakeTmux puts a tmux stand-in on the PATH that lists one shell session in
// dir until kill-session takes it, which is all the purge asks of tmux.
func fakeTmux(t *testing.T, id, dir string) {
	t.Helper()
	bin := t.TempDir()
	marker := filepath.Join(bin, "alive")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"list-panes)\n" +
		"  [ -f '" + marker + "' ] && printf '" + id + "\\t123\\t1700000000\\t0\\t0\\twork\\t" + dir + "\\t\\t\\t\\t\\t\\t\\t\\t\\n'\n" +
		"  ;;\n" +
		"kill-session)\n" +
		"  rm -f '" + marker + "'\n" +
		"  ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A successful deletion takes the project's news with it: the purge marks
// every terminal it stops read the way the single handlers do, and after the
// directory is gone the project's compose and askpass targets read themselves
// too. Another project's news stays untouched.
func TestASuccessfulDeletionMarksTheProjectsNewsRead(t *testing.T) {
	root := t.TempDir()
	appPath := filepath.Join(root, "app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatal(err)
	}
	const shellID = "11111111-1111-4111-8111-111111111111"
	fakeTmux(t, shellID, appPath)
	projects := project.NewRepository(root, nil)
	s := deletionServer(t, t.TempDir(), projects)

	s.notifier.Add(shellID)
	s.notifier.Add(notify.DockerTarget("app"))
	s.notifier.Add(notify.GitPromptTarget("app"))
	s.notifier.Add(notify.DockerTarget("other"))

	p, err := projects.FindByName("app")
	if err != nil {
		t.Fatalf("find project: %v", err)
	}
	s.deletes.start(p.Name, p.Path)
	s.deleteProjectWithCompose(p)

	if _, err := os.Stat(appPath); !os.IsNotExist(err) {
		t.Fatalf("the project directory is still there: %v", err)
	}
	unread := s.notifier.UnreadTargets()
	for _, target := range []string{shellID, notify.DockerTarget("app"), notify.GitPromptTarget("app")} {
		if unread[target] {
			t.Fatalf("%q is still unread after the deletion", target)
		}
	}
	if !unread[notify.DockerTarget("other")] {
		t.Fatal("the deletion read another project's news")
	}
}

// An aborted deletion leaves the compose failure notification standing
// unread: it is the one word about why nothing was removed. The stuck run
// here is one the register still names while something holds its lock, whose
// own deadline has long passed, so the wait gives it up and the deletion
// refuses to work over it.
func TestAnAbortedDeletionKeepsTheFailureNewsUnread(t *testing.T) {
	root := t.TempDir()
	appPath := filepath.Join(root, "app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	runsDir := filepath.Join(stateDir, "docker", "runs")
	if err := os.MkdirAll(runsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	statefile.Save(filepath.Join(stateDir, "docker", "runs.json"), 0o600, []docker.ComposeRecord{{
		ID:        "stuck",
		Dir:       appPath,
		Label:     "app",
		Action:    "Compose up",
		Timeout:   time.Second,
		StartedAt: time.Now().Add(-time.Hour).UTC(),
	}})
	lock, err := os.OpenFile(filepath.Join(runsDir, "stuck.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}

	projects := project.NewRepository(root, nil)
	s := deletionServer(t, stateDir, projects)
	finished := make(chan struct{})
	s.docker.OnComposeDone(func(docker.ComposeRun, error, string) { close(finished) })
	s.docker.Recover()
	if !s.docker.ComposeBusyUnder(appPath) {
		t.Fatal("the stuck run was not adopted")
	}

	s.notifier.Add(notify.DockerTarget("app"))
	p, err := projects.FindByName("app")
	if err != nil {
		t.Fatalf("find project: %v", err)
	}
	s.deletes.start(p.Name, p.Path)
	s.deleteProjectWithCompose(p)

	if _, err := os.Stat(appPath); err != nil {
		t.Fatalf("the aborted deletion touched the directory: %v", err)
	}
	if !s.notifier.UnreadTargets()[notify.DockerTarget("app")] {
		t.Fatal("the aborted deletion read the failure notification away")
	}

	// Let the adopted run end and report before the test's directories go, so
	// its bookkeeping cannot race the cleanup.
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the adopted run never reported its end")
	}
}

// The file carries what is going on and never history: a deletion that ended,
// however it ended, leaves nothing for the next process to take up, and a
// failure is this server's to show and nobody else's to inherit.
func TestAFinishedDeletionLeavesNothingOnDisk(t *testing.T) {
	stateDir := t.TempDir()
	deletes := newProjectDeletes(stateDir)

	deletes.start("app", "/projects/app")
	deletes.finish("app", "")
	if pending := newProjectDeletes(stateDir).pending(); len(pending) != 0 {
		t.Fatalf("a clean deletion stayed pending: %v", pending)
	}

	deletes.start("app", "/projects/app")
	deletes.finish("app", "permission denied")
	restarted := newProjectDeletes(stateDir)
	if pending := restarted.pending(); len(pending) != 0 {
		t.Fatalf("a failed deletion stayed pending: %v", pending)
	}
	if view := restarted.view([]project.Project{{Name: "app"}}); len(view) != 0 {
		t.Fatalf("the failure of a former process turned up again: %+v", view)
	}
}
