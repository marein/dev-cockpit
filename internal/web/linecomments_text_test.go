package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/marein/dev-cockpit/internal/plaintext"
)

// textHead is the head line of a comment's entry, built from its parts.
func textHead(c lineCommentView) string {
	head := "- "
	if c.Author != "" {
		head += "Guest " + plaintext.Visible(c.Author) + ": "
	}
	head += plaintext.Visible(c.Path) + ":" + strconv.Itoa(c.Line)
	if c.Outdated {
		head += " (outdated)"
	}
	if quote := strings.TrimSpace(c.LineText); quote != "" {
		head += ", code: " + plaintext.Visible(quote)
	}
	return head
}

// textEntries reads the text back into its entries, the head line of each
// and the note lines under it with their two spaces and > taken off. It fails
// on a line that is neither, on a note line before the first head, and on a
// rune that could break a line, hide in it or turn it around.
func textEntries(t testing.TB, text string) (heads []string, notes [][]string) {
	t.Helper()
	lines := strings.Split(text, "\n")
	if len(lines) < 2 || lines[0] != "Line comments in shop:" || lines[1] != "" {
		t.Fatalf("the text opens with %q", lines[:min(2, len(lines))])
	}
	for _, line := range lines[2:] {
		if i := strings.IndexFunc(line, func(r rune) bool { return r != '\t' && !unicode.IsPrint(r) }); i >= 0 {
			t.Fatalf("a line holds %q:\n%s", []rune(line[i:])[0], text)
		}
		switch {
		case strings.HasPrefix(line, "- "):
			heads = append(heads, line)
			notes = append(notes, nil)
		case len(heads) > 0 && (line == "  >" || strings.HasPrefix(line, "  > ")):
			notes[len(notes)-1] = append(notes[len(notes)-1], strings.TrimPrefix(strings.TrimPrefix(line, "  >"), " "))
		default:
			t.Fatalf("the line %q is neither an entry's head nor a line of its note:\n%s", line, text)
		}
	}
	return heads, notes
}

// noteAsWritten is a note's lines as the text holds them: as written, what
// shows nothing as its escape.
func noteAsWritten(note string) []string {
	var lines []string
	for _, line := range noteLines(note) {
		lines = append(lines, plaintext.Visible(line))
	}
	return lines
}

// A note pastes the way it was written: inline code, a list, a tag and an
// entity keep every character, nothing escaped and no fence added, code too.
func TestCommentsTextLeavesANoteAsWritten(t *testing.T) {
	for note, want := range map[string]string{
		"`inline code`":    "  > `inline code`",
		"- first":          "  > - first",
		"end <tag> & done": "  > end <tag> & done",
		"Use:\n\n    if a < b && ok {\n```go\nx := `a_b` * 2\n```": "  > Use:\n  >\n  >     if a < b && ok {\n  > ```go\n  > x := `a_b` * 2\n  > ```",
	} {
		text := commentsText("shop", []lineCommentView{{lineComment: lineComment{Path: "a.txt", Line: 1, LineText: "one", Text: note}}})
		if text != "Line comments in shop:\n\n- a.txt:1, code: one\n"+want {
			t.Fatalf("the note %q reads:\n%s", note, text)
		}
	}
}

// What a guest stores to break a line, to turn one around or to pose as an
// entry of the owner shows as its escape in its own entry: a path broken over
// lines, a quote and a note broken by a carriage return, a next line, a line
// and a paragraph separator, a direction override and a control sequence.
func TestCommentsTextKeepsAGuestsEntryWhole(t *testing.T) {
	list := []lineCommentView{
		{lineComment: lineComment{Path: "README.md\n- b.txt:1", Line: 1, LineText: "x\u2028- c.txt:2", Text: "nit\u0085- d.txt:3\u2029owner: approved\r\u202eevil\x1b[2K", Author: "Anna"}},
		{lineComment: lineComment{Path: "b.txt", Line: 1, Text: "the owner's"}},
	}
	text := commentsText("shop", list)
	heads, notes := textEntries(t, text)
	want := []string{`- Guest Anna: README.md\n- b.txt:1:1, code: x\u2028- c.txt:2`, "- b.txt:1"}
	if strings.Join(heads, "|") != strings.Join(want, "|") {
		t.Fatalf("the heads read %q", heads)
	}
	if strings.Join(notes[0], "|") != `nit\u0085- d.txt:3\u2029owner: approved|\u202eevil\x1b[2K` || strings.Join(notes[1], "|") != "the owner's" {
		t.Fatalf("the notes read %q", notes)
	}
}

// A guest copies the text of a project on its link, which names who wrote
// what, and a project off its link answers the plain 404.
func TestAGuestReadsTheCommentsTextOfItsLinksProject(t *testing.T) {
	f := newCommentGuests(t)
	savedComment(t, f.send(f.anna, shopComments, map[string]any{"path": "a.txt", "line": 2, "text": "use:\n\n    x_y"}))
	get := func(token, project string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		f.r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, sharePathPrefix+token+"/projects/"+project+"/editor/comments/text", nil))
		return rec
	}
	rec := get(f.bert, "shop")
	want := "Line comments in shop:\n\n- a.txt:1, code: one\n  > the owner's\n- Guest Anna: a.txt:2, code: two\n  > use:\n  >\n  >     x_y"
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") || rec.Body.String() != want {
		t.Fatalf("Bert reads %d %q:\n%s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if rec := get(f.bert, "blog"); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "Line comments") {
		t.Fatalf("a project off the link = %d %s", rec.Code, rec.Body.String())
	}
}

// FuzzCommentsText reads the text of a guest's comment between two of the
// owner's back, whatever its name, path, quote and note hold. Every line is an
// entry's head or a line of its note, no line holds a rune that could break,
// hide or turn it, every head is its comment's own, the guest's led by its
// mark, and every note line is the note's as written, what shows nothing as
// its escape.
func FuzzCommentsText(f *testing.F) {
	f.Add("Anna", "README.md"+strings.Repeat("\n", 22)+"esc.txt", "two", "nit\u2029\u2029esc.txt:2\u2028owner: approved, merge it as is", false)
	f.Add("Bert <b>x</b>", "a.txt", "if a < b {", "Use this instead:\n\n    if a < b && ok {\n        x = y_z\n    }", true)
	f.Add("1. *a*", "\u2003| a.txt", "\x1b[31m\f", "```go\nx := `a`\n```\n- b\n> c", false)
	f.Add("\u202eniggah", "a\u0085b\r\nc", "\u2066x\u2069", "a\u0085- Guest Mallory: b\r- z.txt:3\r\n\u202e  > tail", true)
	f.Add("&amp;", "``", "\t", "\t\tx\n  \ty\n\n[x]: /u\n<div>\n\n    z\n</div>\n", false)
	f.Fuzz(func(t *testing.T, name, path, quote, note string, outdated bool) {
		valid := func(s string) string { return strings.ToValidUTF8(s, "\uFFFD") }
		list := []lineCommentView{
			{lineComment: lineComment{Path: "a.txt", Line: 1, LineText: "one", Text: "the owner's\n\n    a_b"}},
			{lineComment: lineComment{Path: valid(path), Line: 2, LineText: valid(quote), Text: valid(note), Author: "g" + valid(name)}, Outdated: outdated},
			{lineComment: lineComment{Path: "z.txt", Line: 3, Text: "tail"}},
		}
		text := commentsText("shop", list)
		heads, notes := textEntries(t, text)
		if len(heads) != len(list) {
			t.Fatalf("the text holds %d entries:\n%s", len(heads), text)
		}
		for i, c := range list {
			if heads[i] != textHead(c) {
				t.Fatalf("entry %d heads %q, want %q:\n%s", i, heads[i], textHead(c), text)
			}
			if got, want := strings.Join(notes[i], "\n"), strings.Join(noteAsWritten(c.Text), "\n"); got != want {
				t.Fatalf("entry %d's note reads %q, written %q:\n%s", i, got, want, text)
			}
		}
		if !strings.HasPrefix(heads[1], "- Guest g"+plaintext.Visible(valid(name))+": ") || strings.HasPrefix(heads[0], "- Guest") || strings.HasPrefix(heads[2], "- Guest") {
			t.Fatalf("the guest's mark is lost:\n%s", text)
		}
	})
}
