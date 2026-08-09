package web

import (
	"testing"
)

func TestCommitDraftsRoundtrip(t *testing.T) {
	d := newCommitDrafts(t.TempDir())
	draft, changed := d.Save("p", commitDraft{Message: "a message", Paths: []string{"b.txt", "a.txt"}})
	if !changed {
		t.Fatal("first save reported nothing changed")
	}
	if draft.UpdatedAt.IsZero() {
		t.Fatal("saved draft carries no timestamp")
	}
	got := d.Get("p")
	if got.Message != "a message" {
		t.Fatalf("message: %q", got.Message)
	}
	if len(got.Paths) != 2 || got.Paths[0] != "a.txt" || got.Paths[1] != "b.txt" {
		t.Fatalf("paths not sorted: %v", got.Paths)
	}
}

func TestCommitDraftsRepeatedSaveChangesNothing(t *testing.T) {
	d := newCommitDrafts(t.TempDir())
	d.Save("p", commitDraft{Message: "m", Paths: []string{"a.txt", "b.txt"}})
	if _, changed := d.Save("p", commitDraft{Message: "m", Paths: []string{"b.txt", "a.txt"}}); changed {
		t.Fatal("same draft in another order reported as changed")
	}
	if _, changed := d.Save("empty", commitDraft{}); changed {
		t.Fatal("empty save on an absent draft reported as changed")
	}
	if d.Get("empty").UpdatedAt.IsZero() == false {
		t.Fatal("empty save on an absent draft wrote an entry")
	}
}

func TestCommitDraftsCarryTheAmend(t *testing.T) {
	d := newCommitDrafts(t.TempDir())
	if _, changed := d.Save("p", commitDraft{Message: "the stash", Amend: true, AmendMessage: "tip, fixed"}); !changed {
		t.Fatal("an amend draft reported nothing changed")
	}
	got := d.Get("p")
	if !got.Amend || got.AmendMessage != "tip, fixed" || got.Message != "the stash" {
		t.Fatalf("amend draft came back as %+v", got)
	}
	if _, changed := d.Save("p", commitDraft{Message: "the stash", Amend: true, AmendMessage: "tip, fixed"}); changed {
		t.Fatal("same amend draft reported as changed")
	}
	if _, changed := d.Save("p", commitDraft{Message: "the stash"}); !changed {
		t.Fatal("dropping the amend reported nothing changed")
	}
	if got := d.Get("p"); got.Amend || got.AmendMessage != "" {
		t.Fatalf("the dropped amend is still stored: %+v", got)
	}
	d.Save("q", commitDraft{AmendMessage: "stray"})
	if got := d.Get("q"); got.AmendMessage != "" {
		t.Fatalf("a borrowed message without its amend was stored: %+v", got)
	}
}

func TestCommitDraftsClearKeepsTheTimestamp(t *testing.T) {
	d := newCommitDrafts(t.TempDir())
	if d.Clear("p") {
		t.Fatal("clearing an absent draft reported something to clear")
	}
	d.Save("p", commitDraft{Message: "m", Paths: []string{"a.txt"}, Amend: true, AmendMessage: "b"})
	if !d.Clear("p") {
		t.Fatal("clearing a stored draft reported nothing to clear")
	}
	got := d.Get("p")
	if got.Message != "" || len(got.Paths) != 0 || got.Amend || got.AmendMessage != "" {
		t.Fatalf("cleared draft still holds content: %+v", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("cleared draft lost its timestamp")
	}
}

func TestCommitDraftsDelete(t *testing.T) {
	d := newCommitDrafts(t.TempDir())
	d.Save("p", commitDraft{Message: "m"})
	d.Delete("p")
	if !d.Get("p").UpdatedAt.IsZero() {
		t.Fatal("deleted draft still answers")
	}
}
