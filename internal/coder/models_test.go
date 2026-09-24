package coder

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/settings"
)

func modelNames(models []Model) string {
	var parts []string
	for _, m := range models {
		parts = append(parts, m.Source()+":"+m.Name)
	}
	return strings.Join(parts, ",")
}

// The repository lists the CLI's own names first and the added ones after
// them, each marked with its source. Add checks the name by the shared rule,
// changes nothing for a name that is listed already, and persists in the
// settings store under the coder's own key, so a second repository over the
// same store lists it too and another coder's does not. Delete refuses a CLI
// name, forgets an added one and does nothing for one nobody added, and an
// emptied list takes its key out of the store.
func TestTheModelRepositoryListsAddsAndDeletes(t *testing.T) {
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	cli := func() []string { return []string{"opus", "haiku"} }
	repo := NewModelRepository(store, "claude", "the note", cli)
	if got := modelNames(repo.List()); got != "cli:opus,cli:haiku" {
		t.Fatalf("want the CLI's names alone at first, got %s", got)
	}
	if repo.Note() != "the note" {
		t.Fatalf("want the note handed through, got %q", repo.Note())
	}
	if err := repo.Add("two words"); err == nil || !strings.Contains(err.Error(), "no spaces") {
		t.Fatalf("want the shared rule's refusal, got %v", err)
	}
	if err := repo.Add(" "); err == nil {
		t.Fatal("want an empty name refused")
	}
	for _, name := range []string{" claude-haiku-4-5 ", "claude-haiku-4-5", "opus"} {
		if err := repo.Add(name); err != nil {
			t.Fatalf("Add(%q): %v", name, err)
		}
	}
	if got := modelNames(repo.List()); got != "cli:opus,cli:haiku,added:claude-haiku-4-5" {
		t.Fatalf("want the added name once, after the CLI's, got %s", got)
	}
	if !repo.Exists("claude-haiku-4-5") || !repo.Exists("opus") || repo.Exists("sonnet") {
		t.Fatal("Exists does not read the list")
	}
	again := NewModelRepository(store, "claude", "", cli)
	if got := modelNames(again.List()); got != "cli:opus,cli:haiku,added:claude-haiku-4-5" {
		t.Fatalf("want the added name persisted in the store, got %s", got)
	}
	if got := modelNames(NewModelRepository(store, "copilot", "", nil).List()); got != "" {
		t.Fatalf("want another coder's list untouched, got %s", got)
	}
	if err := repo.Delete("opus"); err == nil || !strings.Contains(err.Error(), "CLI's own") {
		t.Fatalf("want a CLI name refused, got %v", err)
	}
	if err := repo.Delete("nobody-added"); err != nil {
		t.Fatalf("want a name nobody added to be nothing to do, got %v", err)
	}
	if err := repo.Delete("claude-haiku-4-5"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := modelNames(again.List()); got != "cli:opus,cli:haiku" {
		t.Fatalf("want the added name gone from the store, got %s", got)
	}
	if _, ok := store.Lookup(assistant.ModelAddedKey("claude")); ok {
		t.Fatal("want an emptied list to take its key out of the store")
	}
}

type keeperCoder struct {
	Coder
	repo ModelRepository
}

func (c keeperCoder) ModelRepository() ModelRepository { return c.repo }

// A repository without a store keeps the added names in memory, which is
// what a test coder gets. A coder that keeps one is asked for it, and a coder
// without answers the empty one, which lists nothing and remembers nothing
// without refusing.
func TestARepositoryWithoutAStoreAndACoderWithoutOne(t *testing.T) {
	repo := NewModelRepository(nil, "claude", "", nil)
	if err := repo.Add("haiku"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := modelNames(repo.List()); got != "added:haiku" {
		t.Fatalf("want the name kept in memory, got %s", got)
	}
	if got := ModelRepositoryFor(keeperCoder{repo: repo}); got != repo {
		t.Fatal("want the coder's own repository")
	}
	none := ModelRepositoryFor(nil)
	if none.List() != nil || none.Note() != "" || none.Exists("haiku") {
		t.Fatal("want the empty repository to list nothing")
	}
	if err := none.Add("haiku"); err != nil {
		t.Fatalf("want the empty repository to take an Add without refusing, got %v", err)
	}
	if err := none.Delete("haiku"); err != nil {
		t.Fatalf("want the empty repository to take a Delete without refusing, got %v", err)
	}
	if none.Exists("haiku") {
		t.Fatal("the empty repository must not remember")
	}
}
