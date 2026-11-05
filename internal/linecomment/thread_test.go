package linecomment

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A comment stored before threads existed reads as a thread without replies,
// open, and takes its first reply without any migration.
func TestACommentStoredWithoutRepliesReadsAsAThread(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "line-comments"), 0o755); err != nil {
		t.Fatal(err)
	}
	stored := `[{"id":"old","path":"a.txt","line":2,"lineText":"two","text":"from master","updatedAt":"2026-08-26T10:00:00Z"}]`
	if err := os.WriteFile(filepath.Join(state, "line-comments", "p.json"), []byte(stored), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(state)
	list := s.List("p")
	if len(list) != 1 || list[0].Text != "from master" || len(list[0].Replies) != 0 || list[0].Resolved {
		t.Fatalf("the stored comment reads %+v", list)
	}
	thread, changed, err := s.Reply("p", "old", Reply{Text: "an answer"}, Writer{})
	if err != nil || !changed || len(thread.Replies) != 1 || thread.Replies[0].Text != "an answer" || thread.Replies[0].ID == "" || thread.Replies[0].UpdatedAt.IsZero() {
		t.Fatalf("the reply = %+v %v %v", thread, changed, err)
	}
	if got := s.List("p")[0]; got.Text != "from master" || got.UpdatedAt != list[0].UpdatedAt || len(got.Replies) != 1 {
		t.Fatalf("the thread reads %+v", got)
	}
}

// Replies come in order under the guest's or the owner's name, an edit keeps
// who wrote it, a guest edits and removes only its own, the owner every one,
// and a thread or reply gone meanwhile answers the zero comment.
func TestRepliesOfAThread(t *testing.T) {
	s := New(t.TempDir())
	anna := Writer{Link: "l1", Name: "Anna"}
	bert := Writer{Link: "l2", Name: "Bert"}
	c, _, _ := s.Save("p", Comment{Path: "a.txt", Line: 1, Text: "the owner's"}, Writer{})
	first, _, err := s.Reply("p", c.ID, Reply{Text: "anna's"}, anna)
	if err != nil {
		t.Fatal(err)
	}
	second, _, _ := s.Reply("p", c.ID, Reply{Text: "the owner's answer", Author: "forged", Link: "l1"}, Writer{})
	if len(second.Replies) != 2 || second.Replies[0].Author != "Anna" || second.Replies[0].Link != "l1" || second.Replies[1].Author != "" || second.Replies[1].Link != "" {
		t.Fatalf("the replies read %+v", second.Replies)
	}
	annas, owners := first.Replies[0].ID, second.Replies[1].ID
	if _, _, err := s.Reply("p", c.ID, Reply{ID: owners, Text: "taken"}, anna); !errors.Is(err, ErrNotYours) {
		t.Fatalf("Anna's edit of the owner's reply = %v", err)
	}
	if _, _, err := s.Reply("p", c.ID, Reply{ID: annas, Text: "taken"}, bert); !errors.Is(err, ErrNotYours) {
		t.Fatalf("Bert's edit of Anna's reply = %v", err)
	}
	if _, _, err := s.RemoveReply("p", c.ID, annas, bert); !errors.Is(err, ErrNotYours) {
		t.Fatalf("Bert's delete of Anna's reply = %v", err)
	}
	edited, changed, err := s.Reply("p", c.ID, Reply{ID: annas, Text: "anna's, edited"}, anna)
	if err != nil || !changed || edited.Replies[0].Text != "anna's, edited" || edited.Replies[0].Author != "Anna" {
		t.Fatalf("Anna's edit = %+v %v %v", edited.Replies, changed, err)
	}
	if _, changed, _ := s.Reply("p", c.ID, Reply{ID: annas, Text: "anna's, edited"}, anna); changed {
		t.Fatal("the same text reported as changed")
	}
	if byOwner, _, err := s.Reply("p", c.ID, Reply{ID: annas, Text: "the owner fixed it"}, Writer{}); err != nil || byOwner.Replies[0].Author != "Anna" {
		t.Fatalf("the owner's edit of Anna's reply = %+v %v", byOwner.Replies, err)
	}
	left, changed, err := s.RemoveReply("p", c.ID, owners, Writer{})
	if err != nil || !changed || len(left.Replies) != 1 || left.Replies[0].ID != annas {
		t.Fatalf("the owner's delete = %+v %v %v", left.Replies, changed, err)
	}
	for name, got := range map[string]func() (Comment, bool, error){
		"a reply to a thread gone":   func() (Comment, bool, error) { return s.Reply("p", "gone", Reply{Text: "x"}, Writer{}) },
		"an edit of a reply gone":    func() (Comment, bool, error) { return s.Reply("p", c.ID, Reply{ID: owners, Text: "x"}, Writer{}) },
		"a delete of a reply gone":   func() (Comment, bool, error) { return s.RemoveReply("p", c.ID, owners, Writer{}) },
		"a resolve of a thread gone": func() (Comment, bool, error) { return s.Resolve("p", "gone", true, Writer{}) },
	} {
		if thread, changed, err := got(); thread.ID != "" || changed || err != nil {
			t.Fatalf("%s = %+v %v %v", name, thread, changed, err)
		}
	}
}

// An edit of the first note keeps the replies and the resolved state, a move
// of its line keeps the time it was written.
func TestAnEditOrAMoveKeepsTheThread(t *testing.T) {
	s := New(t.TempDir())
	c, _, _ := s.Save("p", Comment{Path: "a.txt", Line: 1, LineText: "one", Text: "note"}, Writer{})
	s.Reply("p", c.ID, Reply{Text: "answer"}, Writer{})
	s.Resolve("p", c.ID, true, Writer{})
	c.Text = "note, edited"
	edited, changed, err := s.Save("p", c, Writer{})
	if err != nil || !changed || len(edited.Replies) != 1 || !edited.Resolved {
		t.Fatalf("the edit = %+v %v %v", edited, changed, err)
	}
	written := s.List("p")[0].UpdatedAt
	time.Sleep(2 * time.Millisecond)
	if !s.Move("p", "a.txt", []Comment{{ID: c.ID, Line: 4, LineText: "four"}}, Writer{}) || !s.Rename("p", "a.txt", "b.txt") {
		t.Fatal("the move or the rename changed nothing")
	}
	if got := s.List("p")[0]; got.Path != "b.txt" || got.Line != 4 || !got.UpdatedAt.Equal(written) || len(got.Replies) != 1 || !got.Resolved {
		t.Fatalf("after the move the thread reads %+v", got)
	}
}

// Who opened a thread resolves it and opens it again, the owner every one, a
// guest no other.
func TestResolveAThread(t *testing.T) {
	s := New(t.TempDir())
	anna := Writer{Link: "l1", Name: "Anna"}
	owner, _, _ := s.Save("p", Comment{Path: "a.txt", Line: 1, Text: "the owner's"}, Writer{})
	annas, _, _ := s.Save("p", Comment{Path: "a.txt", Line: 2, Text: "anna's"}, anna)
	if _, _, err := s.Resolve("p", owner.ID, true, anna); !errors.Is(err, ErrNotYours) {
		t.Fatalf("Anna's resolve of the owner's thread = %v", err)
	}
	resolved, changed, err := s.Resolve("p", annas.ID, true, anna)
	if err != nil || !changed || !resolved.Resolved {
		t.Fatalf("Anna's resolve = %+v %v %v", resolved, changed, err)
	}
	if _, changed, _ := s.Resolve("p", annas.ID, true, Writer{}); changed {
		t.Fatal("a resolved thread resolved again reported a change")
	}
	if open, _, err := s.Resolve("p", annas.ID, false, Writer{}); err != nil || open.Resolved {
		t.Fatalf("the owner's reopen = %+v %v", open, err)
	}
}

// A guest deletes a thread only when it wrote every note of it, the owner
// every thread with its replies, nobody one of the forge.
func TestAThreadGoesWithItsReplies(t *testing.T) {
	s := New(t.TempDir())
	anna := Writer{Link: "l1", Name: "Anna"}
	alone, _, _ := s.Save("p", Comment{Path: "a.txt", Line: 1, Text: "anna's"}, anna)
	s.Reply("p", alone.ID, Reply{Text: "anna again"}, anna)
	answered, _, _ := s.Save("p", Comment{Path: "a.txt", Line: 2, Text: "anna's too"}, anna)
	answered, _, _ = s.Reply("p", answered.ID, Reply{Text: "the owner's"}, Writer{})
	if anna.MayAll(answered) || !anna.May(answered) || !anna.MayAll(s.List("p")[0]) || !(Writer{}).MayAll(answered) {
		t.Fatal("the writer's rights over a thread are off")
	}
	if anna.MayDelete(answered) || !anna.MayDelete(s.List("p")[0]) || !(Writer{}).MayDelete(answered) || (Writer{}).MayDelete(Comment{Forge: &Forge{Kind: "gitlab"}}) {
		t.Fatal("the writer's deletes are off")
	}
	if n := s.Remove("p", func(c Comment) bool { return anna.MayDelete(c) }); n != 1 {
		t.Fatalf("Anna's delete of all took %d threads", n)
	}
	if list := s.List("p"); len(list) != 1 || list[0].ID != answered.ID || len(list[0].Replies) != 1 {
		t.Fatalf("after Anna's delete the list reads %+v", list)
	}
}

// The cap counts every note people write, replies included.
func TestTheCapCountsReplies(t *testing.T) {
	s := New(t.TempDir())
	c, _, _ := s.Save("p", Comment{Path: "a.txt", Line: 1, Text: "n"}, Writer{})
	for i := 1; i < MaxPerProject; i++ {
		if _, _, err := s.Reply("p", c.ID, Reply{Text: "r"}, Writer{}); err != nil {
			t.Fatalf("reply %d refused: %v", i, err)
		}
	}
	if _, _, err := s.Reply("p", c.ID, Reply{Text: "over"}, Writer{}); !errors.Is(err, errTooMany) {
		t.Fatalf("the reply over the cap = %v", err)
	}
	if _, _, err := s.Save("p", Comment{Path: "a.txt", Line: 2, Text: "over"}, Writer{}); !errors.Is(err, errTooMany) {
		t.Fatalf("the thread over the cap = %v", err)
	}
	first := s.List("p")[0].Replies[0]
	if _, changed, err := s.Reply("p", c.ID, Reply{ID: first.ID, Text: "edited at the cap"}, Writer{}); err != nil || !changed {
		t.Fatalf("an edit at the cap = %v %v", changed, err)
	}
}

// A thread with a reply stays the owner's own when a publish names it, and a
// resolved one published takes the forge's state.
func TestAPublishTakesNoThreadWithReplies(t *testing.T) {
	s := New(t.TempDir())
	replied, _, _ := s.Save("p", Comment{Path: "src/a.go", Line: 6, LineText: "name := b", Text: "New name?"}, Writer{})
	s.Reply("p", replied.ID, Reply{Text: "bb"}, Writer{})
	resolved, _, _ := s.Save("p", Comment{Path: "src/a.go", Line: 7, LineText: "c := 3", Text: "Fine"}, Writer{})
	s.Resolve("p", resolved.ID, true, Writer{})
	if s.Published("p", map[string]ForgeThread{replied.ID: forgeThread("39", "root", "New name?", 4, "name := b")}) {
		t.Fatal("a thread with a reply became a thread of the forge")
	}
	if !s.Published("p", map[string]ForgeThread{resolved.ID: forgeThread("40", "root", "Fine", 5, "c := 3")}) {
		t.Fatal("the resolved thread was not published")
	}
	if c := forgeOf(t, s, "40"); c.Resolved || c.Was != resolved.ID {
		t.Fatalf("the published thread reads %+v", c)
	}
	for _, c := range s.List("p") {
		if c.ID == replied.ID && (Publishable(c) || c.Forge != nil) {
			t.Fatalf("the thread with a reply reads %+v", c)
		}
	}
}
