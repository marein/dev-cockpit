package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dev-cockpit/internal/docker"
	"github.com/local/dev-cockpit/internal/project"
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
