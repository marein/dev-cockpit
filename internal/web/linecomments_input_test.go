package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A comment stored by a released build loads, edits and moves as before
// whatever its quote holds, an escape and a form feed of the code line
// included, and its state file is read as it stands.
func TestAReleasedCommentWithAnEscapeInItsQuoteEditsAndMoves(t *testing.T) {
	f := newCommentGuests(t)
	p, err := f.s.projects.FindByName("shop")
	if err != nil {
		t.Fatal(err)
	}
	quote := "\x1b[31mred\x1b[0m\f"
	if err := os.WriteFile(filepath.Join(p.Path, "esc.txt"), []byte("plain\n"+quote+"\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	released := `[{"id":"old1","path":"esc.txt","line":2,"lineText":"\u001b[31mred\u001b[0m\f","text":"keep the color","updatedAt":"2026-10-01T08:00:00Z"}]`
	state := f.s.lineComments.file("shop")
	if err := os.WriteFile(state, []byte(released), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, views := f.read(t, f.anna); len(views) != 1 || views[0].Outdated || views[0].LineText != quote {
		t.Fatalf("the released comment reads %+v", views)
	}
	if raw, _ := os.ReadFile(state); string(raw) != released {
		t.Fatalf("the read rewrote the state file: %s", raw)
	}
	edited := savedComment(t, f.send("", shopComments, map[string]any{"id": "old1", "path": "esc.txt", "line": 2, "lineText": quote, "text": "keep the color, edited"}))
	if edited.Text != "keep the color, edited" || edited.LineText != quote {
		t.Fatalf("the edit stored %+v", edited)
	}
	if err := os.WriteFile(filepath.Join(p.Path, "esc.txt"), []byte("plain\nnew\n"+quote+"\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := f.send("", shopComments+"/move", map[string]any{"path": "esc.txt", "comments": []map[string]any{{"id": "old1", "line": 3, "lineText": quote}}}); rec.Code != http.StatusOK {
		t.Fatalf("the move = %d %s", rec.Code, rec.Body.String())
	}
	if moved, _ := f.note(t, "old1"); moved.Line != 3 || moved.LineText != quote {
		t.Fatalf("the move left %+v", moved)
	}
	if _, views := f.read(t, f.anna); views[0].Outdated || views[0].Line != 3 {
		t.Fatalf("the moved comment reads %+v", views[0])
	}
}

// What a value can hold is stored as it came, for the owner and a guest
// alike: a path with leading Unicode spaces, markup in the note and control
// characters in the quote. Only a path longer than any file can have is
// refused, and a note keeps a longer one a released build stored.
func TestALineCommentStoresWhatItGetsAndBoundsOnlyThePath(t *testing.T) {
	f := newCommentGuests(t)
	path := "\u2003\u2003| a.txt"
	note := "<b>owner</b> approved\n\n- `a.txt:1`\x1b[2J"
	quote := "\tcode\x1b\u2028more"
	for _, token := range []string{"", f.anna} {
		saved := savedComment(t, f.send(token, shopComments, map[string]any{"path": path, "line": 1, "lineText": quote, "text": note}))
		if stored, _ := f.note(t, saved.ID); stored.Path != path || stored.Text != note || stored.LineText != quote {
			t.Fatalf("the note is stored as %+v", stored)
		}
	}
	long := strings.Repeat("d/", maxLineCommentPath/2) + "a.txt"
	for _, token := range []string{"", f.anna} {
		rec := f.send(token, shopComments, map[string]any{"path": long, "line": 1, "text": "too far"})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "too long") {
			t.Fatalf("a %d byte path = %d %s", len(long), rec.Code, rec.Body.String())
		}
	}
	if rec := f.send("", shopComments, map[string]any{"id": f.owner.ID, "path": long, "line": 1, "text": "moved far"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("moving a note onto a %d byte path = %d %s", len(long), rec.Code, rec.Body.String())
	}
	held, _, err := f.s.lineComments.Save("shop", lineComment{Path: "a.txt", Line: 1, Text: "held"}, commentWriter{})
	if err != nil {
		t.Fatal(err)
	}
	list := f.s.lineComments.List("shop")
	for i := range list {
		if list[i].ID == held.ID {
			list[i].Path = long
		}
	}
	f.s.lineComments.store("shop", list)
	if edited := savedComment(t, f.send("", shopComments, map[string]any{"id": held.ID, "path": long, "line": 1, "text": "held, edited"})); edited.Text != "held, edited" {
		t.Fatalf("the edit of a released long path stored %+v", edited)
	}
}

// A file with Windows line endings keeps taking comments: the carriage return
// ending a quote is cut, whether the editor sends it, the move carries it or
// the CLI's add reads it from the disk, and the read judges such a file's
// quotes as standing.
func TestALineCommentCutsTheCarriageReturnOfAWindowsLine(t *testing.T) {
	f := newCommentGuests(t)
	p, err := f.s.projects.FindByName("shop")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Path, "win.txt"), []byte("one\r\ntwo\r\nthree\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sent := savedComment(t, f.send("", shopComments, map[string]any{"path": "win.txt", "line": 2, "lineText": "two\r", "text": "sent"}))
	read := savedComment(t, f.send(f.anna, shopComments, map[string]any{"path": "win.txt", "line": 3, "text": "read from the disk"}))
	if sent.LineText != "two" || read.LineText != "three" {
		t.Fatalf("the quotes read %q and %q", sent.LineText, read.LineText)
	}
	if rec := f.send("", shopComments+"/move", map[string]any{"path": "win.txt", "comments": []map[string]any{{"id": sent.ID, "line": 1, "lineText": "one\r"}}}); rec.Code != http.StatusOK {
		t.Fatalf("the move = %d %s", rec.Code, rec.Body.String())
	}
	if moved, _ := f.note(t, sent.ID); moved.Line != 1 || moved.LineText != "one" {
		t.Fatalf("the move left %+v", moved)
	}
	_, views := f.read(t, f.anna)
	for _, v := range views {
		if v.Path == "win.txt" && v.Outdated {
			t.Fatalf("a quote of the Windows file reads outdated: %+v", v)
		}
	}
	legacy, _, err := f.s.lineComments.Save("shop", lineComment{Path: "win.txt", Line: 2, LineText: "two\r", Text: "a released build's quote"}, commentWriter{})
	if err != nil {
		t.Fatal(err)
	}
	views, _ = reconcileLineComments(p.Path, []lineComment{legacy})
	if views[0].Outdated || views[0].Line != 2 {
		t.Fatalf("a stored quote with its carriage return reads %+v", views[0])
	}
}
