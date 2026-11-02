package linecomment

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A project comment is a thread without a place: it keeps no path, line or
// quote whatever the save brought, leads the list before every file, takes
// replies and resolves like a line comment and counts against the one cap.
func TestAProjectCommentIsAThreadWithoutAPlace(t *testing.T) {
	l := New(t.TempDir())
	l.Save("p", Comment{Path: "a.txt", Line: 1, Text: "on a line"}, Writer{})
	whole, changed, err := l.Save("p", Comment{Project: true, Path: "b.txt", Line: 4, LineText: "x", Text: "on the project"}, Writer{})
	if err != nil || !changed || !whole.Project || whole.Path != "" || whole.Line != 0 || whole.LineText != "" {
		t.Fatalf("the project comment saved as %+v, changed=%v err=%v", whole, changed, err)
	}
	if list := l.List("p"); len(list) != 2 || list[0].ID != whole.ID || list[1].Path != "a.txt" {
		t.Fatalf("the project comment does not lead the list: %+v", list)
	}
	replied, changed, err := l.Reply("p", whole.ID, Reply{Text: "agreed"}, Writer{Link: "l1", Name: "Anna"})
	if err != nil || !changed || len(replied.Replies) != 1 || !replied.Project {
		t.Fatalf("the reply gave %+v changed=%v err=%v", replied, changed, err)
	}
	if resolved, changed, err := l.Resolve("p", whole.ID, true, Writer{}); err != nil || !changed || !resolved.Resolved {
		t.Fatalf("the resolve gave %+v changed=%v err=%v", resolved, changed, err)
	}
	edited := whole
	edited.Text = "on the project, edited"
	if got, changed, err := l.Save("p", edited, Writer{}); err != nil || !changed || !got.Project || !got.Resolved || len(got.Replies) != 1 {
		t.Fatalf("the edit gave %+v changed=%v err=%v", got, changed, err)
	}
}

// What a thread stands on is fixed: an edit that would move a line comment
// onto the project, or a project comment onto a line, is refused and leaves
// the thread as it was.
func TestAThreadKeepsWhatItStandsOn(t *testing.T) {
	l := New(t.TempDir())
	line, _, _ := l.Save("p", Comment{Path: "a.txt", Line: 1, Text: "on a line"}, Writer{})
	whole, _, _ := l.Save("p", Comment{Project: true, Text: "on the project"}, Writer{})
	onto := line
	onto.Project, onto.Path, onto.Line = true, "", 0
	if _, changed, err := l.Save("p", onto, Writer{}); !errors.Is(err, errStandsFor) || changed {
		t.Fatalf("a line comment moved onto the project: changed=%v err=%v", changed, err)
	}
	off := whole
	off.Project, off.Path, off.Line = false, "a.txt", 2
	if _, changed, err := l.Save("p", off, Writer{}); !errors.Is(err, errStandsFor) || changed {
		t.Fatalf("a project comment moved onto a line: changed=%v err=%v", changed, err)
	}
	list := l.List("p")
	if len(list) != 2 || !list[0].Project || list[0].Path != "" || list[1].Project || list[1].Path != "a.txt" {
		t.Fatalf("a refused edit changed the threads: %+v", list)
	}
}

// A project comment is never outdated, reads no file and is never rebound,
// a path filter never matches it, whatever glob, and the delete of the
// outdated ones or of every file leaves it. Only its id takes it.
func TestAProjectCommentStandsApartFromTheFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := New(t.TempDir())
	whole, _, _ := l.Save("p", Comment{Project: true, Text: "on the project"}, Writer{})
	l.Save("p", Comment{Path: "gone.txt", Line: 3, LineText: "lost", Text: "orphan"}, Writer{})
	views, changed := l.Reconciled("p", root)
	if changed || len(views) != 2 || views[0].ID != whole.ID || views[0].Outdated || !views[1].Outdated {
		t.Fatalf("the read judged %+v changed=%v", views, changed)
	}
	for _, pattern := range []string{"**", "*", "", "/", "."} {
		if whole.OnPath([]string{pattern}) {
			t.Fatalf("the path filter %q matches the project comment", pattern)
		}
	}
	if n := l.Remove("p", pickPaths("**")); n != 1 {
		t.Fatalf("removing every file took %d threads", n)
	}
	if list := l.List("p"); len(list) != 1 || list[0].ID != whole.ID {
		t.Fatalf("the project comment did not stay: %+v", list)
	}
	if n := l.Remove("p", pickIDs(whole.ID)); n != 1 || len(l.List("p")) != 0 {
		t.Fatalf("the id did not take the project comment: %d", n)
	}
}

// A store a released build wrote holds no project mark, it reads as the line
// comments it holds and a read leaves the file as it stands; a line comment
// written now stores no mark either, so the shape only grew.
func TestAReleasedStoreReadsUnchanged(t *testing.T) {
	state := t.TempDir()
	l := New(state)
	file := filepath.Join(state, "line-comments", "p.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	released := `[{"id":"old1","path":"a.txt","line":2,"lineText":"two","text":"keep","updatedAt":"2026-10-01T08:00:00Z"}]`
	if err := os.WriteFile(file, []byte(released), 0o644); err != nil {
		t.Fatal(err)
	}
	if list := l.List("p"); len(list) != 1 || list[0].Project || list[0].Path != "a.txt" || list[0].Line != 2 {
		t.Fatalf("the released store reads %+v", list)
	}
	if raw, _ := os.ReadFile(file); string(raw) != released {
		t.Fatalf("the read rewrote the file: %s", raw)
	}
	l.Save("p", Comment{Path: "b.txt", Line: 1, Text: "new"}, Writer{})
	l.Save("p", Comment{Project: true, Text: "whole"}, Writer{})
	raw, _ := os.ReadFile(file)
	var stored []map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	marks := 0
	for _, c := range stored {
		if _, ok := c["project"]; ok {
			marks++
		}
	}
	if marks != 1 || !strings.Contains(string(raw), `"project": true`) && !strings.Contains(string(raw), `"project":true`) {
		t.Fatalf("the store marks %d threads: %s", marks, raw)
	}
}

// The owner's project comment publishes like a line comment, as a general
// one; a guest's and one with replies do not.
func TestAProjectCommentPublishesLikeALineComment(t *testing.T) {
	if !Publishable(Comment{Project: true, Text: "whole"}) || !Publishable(Comment{Path: "a.txt", Line: 1, Text: "line"}) {
		t.Fatal("a comment of the owner does not publish")
	}
	if Publishable(Comment{Project: true, Text: "whole", Link: "l1"}) || Publishable(Comment{Project: true, Text: "whole", Replies: []Reply{{ID: "r"}}}) {
		t.Fatal("a guest's project comment or one with replies publishes")
	}
}
