package web

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/plugin"
)

// newCreatorUnderTest builds the one create path over a scratch projects
// root, with a subscriber on the bus so a test can see what was announced.
func newCreatorUnderTest(t *testing.T) (func(context.Context, string) (string, error), string, <-chan eventbus.Event) {
	t.Helper()
	root := t.TempDir()
	bus := eventbus.New()
	events, cancel := bus.Subscribe()
	t.Cleanup(cancel)
	repo := project.NewRepository(root, recent.New(filepath.Join(t.TempDir(), "recent.json")))
	return NewProjectCreator(repo, bus), root, events
}

// sawProjectsEvent reports whether a projects event is waiting. Publish
// writes into the subscriber's buffer synchronously, so no waiting is needed.
func sawProjectsEvent(events <-chan eventbus.Event) bool {
	select {
	case ev := <-events:
		return ev.Type == "projects"
	default:
		return false
	}
}

func TestProjectCreatorCreatesAMissingDirectory(t *testing.T) {
	create, root, events := newCreatorUnderTest(t)
	path, err := create(context.Background(), "fresh")
	if err != nil {
		t.Fatalf("create = %v", err)
	}
	if filepath.Base(path) != "fresh" {
		t.Fatalf("create answered %q, want the fresh directory", path)
	}
	info, err := os.Stat(filepath.Join(root, "fresh"))
	if err != nil || !info.IsDir() {
		t.Fatalf("the directory was not made: %v", err)
	}
	if !sawProjectsEvent(events) {
		t.Fatal("the creation was not announced")
	}
}

func TestProjectCreatorAdoptsAnEmptyDirectory(t *testing.T) {
	create, root, events := newCreatorUnderTest(t)
	if err := os.Mkdir(filepath.Join(root, "kept"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := create(context.Background(), "kept")
	if err != nil {
		t.Fatalf("create = %v, want the empty leftover adopted", err)
	}
	if filepath.Base(path) != "kept" {
		t.Fatalf("create answered %q, want the existing directory", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("the adopted directory moved: %v entries, %v", len(entries), err)
	}
	if !sawProjectsEvent(events) {
		t.Fatal("the adoption was not announced")
	}
}

func TestProjectCreatorRefusesADirectoryWithContent(t *testing.T) {
	create, root, events := newCreatorUnderTest(t)
	dir := filepath.Join(root, "taken")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := create(context.Background(), "taken")
	if !errors.Is(err, plugin.ErrProjectExists) {
		t.Fatalf("create = %v, want plugin.ErrProjectExists", err)
	}
	if content, err := os.ReadFile(filepath.Join(dir, "data.txt")); err != nil || string(content) != "kept" {
		t.Fatalf("the refused directory moved: %q %v", content, err)
	}
	if sawProjectsEvent(events) {
		t.Fatal("a refusal was announced")
	}
}
