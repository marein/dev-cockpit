package web

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLineCommentsRoundtrip(t *testing.T) {
	l := newLineComments(t.TempDir())
	saved, changed, err := l.Save("p", lineComment{Path: "b.txt", Line: 3, LineText: "code", Text: "a note"})
	if err != nil || !changed {
		t.Fatalf("first save: changed=%v err=%v", changed, err)
	}
	if saved.ID == "" || saved.UpdatedAt.IsZero() {
		t.Fatalf("saved comment carries no id or timestamp: %+v", saved)
	}
	l.Save("p", lineComment{Path: "a.txt", Line: 9, Text: "later file, listed first"})
	l.Save("p", lineComment{Path: "b.txt", Line: 1, Text: "earlier line"})
	list := l.List("p")
	if len(list) != 3 {
		t.Fatalf("list holds %d comments", len(list))
	}
	if list[0].Path != "a.txt" || list[1].Path != "b.txt" || list[1].Line != 1 || list[2].Line != 3 {
		t.Fatalf("list is not ordered by file then line: %+v", list)
	}
	if got := l.List("other"); len(got) != 0 {
		t.Fatalf("another project reads %d comments", len(got))
	}
}

func TestLineCommentsSaveUpdatesById(t *testing.T) {
	l := newLineComments(t.TempDir())
	saved, _, _ := l.Save("p", lineComment{Path: "a.txt", Line: 3, Text: "note"})
	if _, changed, _ := l.Save("p", saved); changed {
		t.Fatal("same comment reported as changed")
	}
	moved := saved
	moved.Path = "renamed.txt"
	moved.Line = 5
	if _, changed, _ := l.Save("p", moved); !changed {
		t.Fatal("a moved comment reported nothing changed")
	}
	list := l.List("p")
	if len(list) != 1 || list[0].Path != "renamed.txt" || list[0].Line != 5 {
		t.Fatalf("update did not land: %+v", list)
	}
	gone, changed, err := l.Save("p", lineComment{ID: "missing", Path: "a.txt", Line: 1, Text: "x"})
	if err != nil || changed || gone.ID != "" {
		t.Fatalf("an unknown id was resurrected: %+v changed=%v err=%v", gone, changed, err)
	}
}

func TestLineCommentsCap(t *testing.T) {
	l := newLineComments(t.TempDir())
	for i := 0; i < maxLineCommentsPerProject; i++ {
		if _, _, err := l.Save("p", lineComment{Path: "a.txt", Line: i + 1, Text: "n"}); err != nil {
			t.Fatalf("save %d refused: %v", i, err)
		}
	}
	if _, _, err := l.Save("p", lineComment{Path: "a.txt", Line: 999, Text: "over"}); err == nil {
		t.Fatal("the cap did not refuse")
	}
	existing := l.List("p")[0]
	existing.Text = "edited at the cap"
	if _, changed, err := l.Save("p", existing); err != nil || !changed {
		t.Fatalf("editing at the cap refused: changed=%v err=%v", changed, err)
	}
}

func TestLineCommentsMove(t *testing.T) {
	l := newLineComments(t.TempDir())
	a, _, _ := l.Save("p", lineComment{Path: "a.txt", Line: 3, LineText: "old", Text: "note"})
	b, _, _ := l.Save("p", lineComment{Path: "b.txt", Line: 7, LineText: "keep", Text: "other file"})
	if changed := l.Move("p", "a.txt", []lineComment{{ID: a.ID, Line: 3, LineText: "old"}}); changed {
		t.Fatal("an unmoved position reported as changed")
	}
	if changed := l.Move("p", "a.txt", []lineComment{
		{ID: a.ID, Line: 5, LineText: "new"},
		// b lives in another file, the move must not reach it.
		{ID: b.ID, Line: 1, LineText: "hijack"},
	}); !changed {
		t.Fatal("a moved position reported nothing changed")
	}
	list := l.List("p")
	if list[0].Line != 5 || list[0].LineText != "new" {
		t.Fatalf("a.txt did not move: %+v", list[0])
	}
	if list[1].Line != 7 || list[1].LineText != "keep" {
		t.Fatalf("b.txt was moved across its file: %+v", list[1])
	}
}

func TestLineCommentsRename(t *testing.T) {
	l := newLineComments(t.TempDir())
	a, _, _ := l.Save("p", lineComment{Path: "a.txt", Line: 1, Text: "file note"})
	b, _, _ := l.Save("p", lineComment{Path: "dir/deep/b.txt", Line: 2, Text: "folder note"})
	c, _, _ := l.Save("p", lineComment{Path: "director/c.txt", Line: 3, Text: "prefix trap"})
	if l.Rename("p", "a.txt", "a.txt") {
		t.Fatal("a rename onto itself reported a change")
	}
	if !l.Rename("p", "a.txt", "sub/renamed.txt") {
		t.Fatal("the file rename reported nothing changed")
	}
	if !l.Rename("p", "dir", "moved") {
		t.Fatal("the folder rename reported nothing changed")
	}
	if l.Rename("p", "missing.txt", "elsewhere.txt") {
		t.Fatal("a rename without notes reported a change")
	}
	byID := map[string]string{}
	for _, comment := range l.List("p") {
		byID[comment.ID] = comment.Path
	}
	if byID[a.ID] != "sub/renamed.txt" {
		t.Fatalf("the file note did not move: %v", byID[a.ID])
	}
	if byID[b.ID] != "moved/deep/b.txt" {
		t.Fatalf("the folder note did not move: %v", byID[b.ID])
	}
	if byID[c.ID] != "director/c.txt" {
		t.Fatalf("a sibling with the folder's prefix moved: %v", byID[c.ID])
	}
}

func TestLineCommentsRemoveAndClear(t *testing.T) {
	l := newLineComments(t.TempDir())
	a, _, _ := l.Save("p", lineComment{Path: "a.txt", Line: 1, Text: "one"})
	l.Save("p", lineComment{Path: "a.txt", Line: 2, Text: "two"})
	if n := l.Remove("p", nil); n != 0 {
		t.Fatalf("removing nothing reported %d", n)
	}
	if n := l.Remove("p", []string{a.ID, "missing"}); n != 1 {
		t.Fatalf("removing a standing comment reported %d", n)
	}
	if list := l.List("p"); len(list) != 1 || list[0].Text != "two" {
		t.Fatalf("remove took the wrong comment: %+v", list)
	}
	if n := l.Clear("p"); n != 1 {
		t.Fatalf("clearing a standing list reported %d", n)
	}
	if n := l.Clear("p"); n != 0 {
		t.Fatalf("clearing an empty list reported %d", n)
	}
	if len(l.List("p")) != 0 {
		t.Fatal("clear left comments behind")
	}
}

func TestLineCommentsOneFilePerProject(t *testing.T) {
	dir := t.TempDir()
	l := newLineComments(dir)
	p, _, _ := l.Save("p", lineComment{Path: "a.txt", Line: 1, Text: "one"})
	l.Save("q", lineComment{Path: "b.txt", Line: 2, Text: "other project"})
	for _, name := range []string{"p.json", "q.json"} {
		if _, err := os.Stat(filepath.Join(dir, "line-comments", name)); err != nil {
			t.Fatalf("project file %s: %v", name, err)
		}
	}
	if l.Clear("p") != 1 {
		t.Fatal("clearing p reported nothing")
	}
	if _, err := os.Stat(filepath.Join(dir, "line-comments", "p.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("clear left p.json behind: %v", err)
	}
	if list := l.List("q"); len(list) != 1 || list[0].Text != "other project" {
		t.Fatalf("clearing p touched q: %+v", list)
	}
	// Removing the last comment takes the file with it, like clear does.
	q := l.List("q")[0]
	if n := l.Remove("q", []string{q.ID}); n != 1 {
		t.Fatalf("removing q's comment reported %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "line-comments", "q.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the emptied q.json stands: %v", err)
	}
	_ = p
}

func TestLineCommentsRemoveByPaths(t *testing.T) {
	l := newLineComments(t.TempDir())
	l.Save("p", lineComment{Path: "internal/a.go", Line: 1, Text: "in the folder"})
	l.Save("p", lineComment{Path: "internal/deep/b.go", Line: 2, Text: "deeper"})
	l.Save("p", lineComment{Path: "internals/x.go", Line: 3, Text: "similar name"})
	l.Save("p", lineComment{Path: "main.go", Line: 4, Text: "a file"})
	if n := l.RemoveByPaths("p", []string{"internal/", "main.go"}); n != 3 {
		t.Fatalf("the ored filters removed %d", n)
	}
	list := l.List("p")
	if len(list) != 1 || list[0].Path != "internals/x.go" {
		t.Fatalf("the folder prefix reached past the folder: %+v", list)
	}
	if n := l.RemoveByPaths("p", []string{"gone"}); n != 0 {
		t.Fatalf("an unknown path removed %d", n)
	}
	if n := l.RemoveByPaths("p", []string{"**/x.go"}); n != 1 {
		t.Fatalf("the glob removed %d", n)
	}
}

func TestMatchCommentPath(t *testing.T) {
	cases := []struct {
		pattern, target string
		want            bool
	}{
		{"internal/a.go", "internal/a.go", true},
		{"internal", "internal/a.go", true},
		{"internal/", "internal/deep/b.go", true},
		{"internal", "internals/x.go", false},
		{"internal/a", "internal/a.go", false},
		{"", "internal/a.go", false},
		{"*.go", "main.go", true},
		{"*.go", "internal/a.go", false},
		{"internal/*", "internal/a.go", true},
		{"internal/*", "internal/deep/b.go", false},
		{"**/assistant.go", "internal/assistant/assistant.go", true},
		{"**/assistant.go", "assistant.go", true},
		{"**/assistant.go", "internal/assistant/memory.go", false},
		{"internal/**/b.go", "internal/deep/down/b.go", true},
		{"internal/**", "internal/a.go", true},
		{"[", "anything", false},
	}
	for _, tc := range cases {
		if got := matchCommentPath(tc.pattern, tc.target); got != tc.want {
			t.Fatalf("matchCommentPath(%q, %q) = %v, want %v", tc.pattern, tc.target, got, tc.want)
		}
	}
}

func TestReconcileLineComments(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "one\ntwo\nthree\n")
	write("twice.txt", "dup\nx\ndup\n")
	list := []lineComment{
		{ID: "ok", Path: "a.txt", Line: 2, LineText: "two", Text: "fine"},
		{ID: "moved", Path: "a.txt", Line: 1, LineText: "three", Text: "rebinds"},
		{ID: "gone", Path: "a.txt", Line: 3, LineText: "vanished", Text: "orphan"},
		{ID: "ambiguous", Path: "twice.txt", Line: 2, LineText: "dup", Text: "twice in the file"},
		{ID: "missing", Path: "missing.txt", Line: 1, LineText: "x", Text: "file gone"},
		{ID: "empty-ok", Path: "a.txt", Line: 4, LineText: "", Text: "an empty line"},
		{ID: "empty-bad", Path: "a.txt", Line: 1, LineText: "", Text: "line no longer empty"},
	}
	views, rebinds := reconcileLineComments(root, list)
	byID := map[string]lineCommentView{}
	for _, v := range views {
		byID[v.ID] = v
	}
	if v := byID["ok"]; v.Outdated || v.Line != 2 {
		t.Fatalf("a standing quote moved: %+v", v)
	}
	if v := byID["moved"]; v.Outdated || v.Line != 3 {
		t.Fatalf("a unique quote did not rebind in the view: %+v", v)
	}
	if v := byID["gone"]; !v.Outdated || v.Line != 3 {
		t.Fatalf("a vanished quote is not outdated at its last line: %+v", v)
	}
	if v := byID["ambiguous"]; !v.Outdated || v.Line != 2 {
		t.Fatalf("an ambiguous quote was touched: %+v", v)
	}
	if v := byID["missing"]; !v.Outdated {
		t.Fatalf("a missing file is not outdated: %+v", v)
	}
	if v := byID["empty-ok"]; v.Outdated {
		t.Fatalf("an empty quote on an empty line is outdated: %+v", v)
	}
	if v := byID["empty-bad"]; !v.Outdated {
		t.Fatalf("an empty quote never rebinds and has to read outdated: %+v", v)
	}
	if len(rebinds) != 1 || len(rebinds["a.txt"]) != 1 {
		t.Fatalf("rebinds are not exactly the one unique quote: %+v", rebinds)
	}
	if move := rebinds["a.txt"][0]; move.ID != "moved" || move.Line != 3 || move.LineText != "three" {
		t.Fatalf("the rebind carries the wrong position: %+v", move)
	}
}

func TestClampLineQuote(t *testing.T) {
	if got := clampLineQuote("short"); got != "short" {
		t.Fatalf("short quote was cut: %q", got)
	}
	long := strings.Repeat("ä", maxLineCommentQuote)
	cut := clampLineQuote(long)
	if len(cut) > maxLineCommentQuote {
		t.Fatalf("quote not cut: %d bytes", len(cut))
	}
	if !strings.HasSuffix(cut, "ä") {
		t.Fatalf("quote cut inside a rune: %q", cut[len(cut)-4:])
	}
}
