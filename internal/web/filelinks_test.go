package web

import (
	"fmt"
	"html/template"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
)

func writeFiles(t testing.TB, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// testLinker links the files of a project p holding a.go and src/app.go.
func testLinker(t *testing.T) *fileLinker {
	root := t.TempDir()
	writeFiles(t, root, "a.go", "src/app.go")
	return &fileLinker{refs: filesystem.NewFileRefs(root), project: "p", root: root}
}

func TestEditorFileURL(t *testing.T) {
	cases := []struct {
		ref  filesystem.FileRef
		want string
	}{
		{filesystem.FileRef{Path: "main.go"}, "/projects/my%20app/editor?file=main.go"},
		{filesystem.FileRef{Path: "internal/a b.go", Line: 12}, "/projects/my%20app/editor?file=internal%2Fa+b.go&line=12"},
		{filesystem.FileRef{Path: "x.go", Line: 3, Col: 7}, "/projects/my%20app/editor?col=7&file=x.go&line=3"},
	}
	for _, tc := range cases {
		if got := editorFileURL("my app", tc.ref); got != tc.want {
			t.Errorf("editorFileURL(%+v) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

func TestLinkedHTML(t *testing.T) {
	l := testLinker(t)
	cases := []struct {
		name   string
		in     string
		budget int
		want   string
	}{
		{"text", "<p>see a.go:1</p>", 100, `<p>see <a href="/projects/p/editor?file=a.go&amp;line=1" data-file-link>a.go:1</a></p>`},
		{"entity and wide glyph before", "<p>&lt;界&gt; 😀 a.go</p>", 100, `<p>&lt;界&gt; 😀 <a href="/projects/p/editor?file=a.go" data-file-link>a.go</a></p>`},
		{"code", "<pre><code>x a.go\n</code></pre>", 100, `<pre><code>x <a href="/projects/p/editor?file=a.go" data-file-link>a.go</a>
</code></pre>`},
		{"inside a link", `<a href="https://x">a.go</a> a.go`, 100, `<a href="https://x">a.go</a> <a href="/projects/p/editor?file=a.go" data-file-link>a.go</a>`},
		{"over the budget", "<p>a.go</p><p>x a.go</p>", 5, `<p><a href="/projects/p/editor?file=a.go" data-file-link>a.go</a></p><p>x a.go</p>`},
		{"a text that fits after one that does not", "<p>x a.go</p><p>a.go</p>", 5, `<p>x a.go</p><p>a.go</p>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			budget := tc.budget
			if got := string(l.linkedHTML(template.HTML(tc.in), &budget)); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// A page whose newest answer outgrows the budget links nothing older than it,
// a tool line links like the words do.
func TestALongConversationPageLinksItsNewestMessagesOnly(t *testing.T) {
	views := copyViews("Claude", []coder.Message{
		words("m1", coder.RoleUser, "see a.go"),
		words("m2", coder.RoleCoder, "see a.go"),
		words("m3", coder.RoleCoder, strings.Repeat("x ", copyLinkBudget/2+1)+"see a.go"),
		words("m4", coder.RoleUser, "see a.go"),
		{ID: "m5", Role: coder.RoleCoder, Parts: []coder.Part{{Kind: coder.PartTool, Tool: "Read", Text: "a.go"}}},
	})
	testLinker(t).linkCopyViews(views)
	var linked []string
	for _, v := range views {
		html := string(v.HTML)
		for _, p := range v.Parts {
			html += string(p.HTML)
		}
		if strings.Contains(html, "data-file-link") {
			linked = append(linked, v.ID)
		}
	}
	if !slices.Equal(linked, []string{"m4", "m5"}) {
		t.Fatalf("linked %q, want only the newest messages", linked)
	}
}

// The copy view's text links as the live terminal does, over a row the
// terminal wrapped too and up to its last line, at UTF-16 offsets.
func TestCopyViewLinks(t *testing.T) {
	text := "😀 x src/a\npp.go:3\nsee a.go"
	got := testLinker(t).newestFileLinks(text, 10)
	want := []fileLink{
		{Start: 5, End: 10, Href: "/projects/p/editor?file=src%2Fapp.go&line=3"},
		{Start: 11, End: 18, Href: "/projects/p/editor?file=src%2Fapp.go&line=3"},
		{Start: 23, End: 27, Href: "/projects/p/editor?file=a.go"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("links %+v, want %+v", got, want)
	}
}

// A history longer than the budget links its newest lines only, at their place
// in the whole text.
func TestALongHistoryLinksItsNewestLinesOnly(t *testing.T) {
	old := "😀 a.go\n" + strings.Repeat("x\n", copyLinkBudget/2)
	links := testLinker(t).newestFileLinks(old+"see a.go\n", 80)
	at := filesystem.UTF16Len(old) + 4
	if len(links) != 1 || links[0].Start != at || links[0].End != at+4 {
		t.Fatalf("links %+v, want one at %d", links, at)
	}
}

// The copy view and the conversation view lose their links to a bug in the
// linking, never their answer.
func TestFileLinksSurviveAPanic(t *testing.T) {
	broken := &fileLinker{project: "p", root: t.TempDir()}
	if links := broken.newestFileLinks("see a.go\n", 80); links != nil {
		t.Fatalf("links %+v", links)
	}
	views := copyViews("Claude", []coder.Message{words("m1", coder.RoleCoder, "see a.go")})
	html := views[0].HTML
	broken.linkCopyViews(views)
	if views[0].HTML != html {
		t.Fatalf("the bubble became %s", views[0].HTML)
	}
}

// wrapLinker links the files of a project p holding the files programs wrap
// and lists in the cases below.
func wrapLinker(t *testing.T) *fileLinker {
	root := t.TempDir()
	writeFiles(t, root, "src/app.py", "src/deep/app.go", "tests/e2e/probe.js", "notes.txt", "build/notes.txt", "dist/app.css", "dist/app.css.map", "src/😀.txt", "internal/web/static/js/components/terminal-copy.js")
	return &fileLinker{refs: filesystem.NewFileRefs(root), project: "p", root: root}
}

// A word the terminal or a program wrapped links from every row it stands on,
// rows that only end and start with words stay as they read, and no join
// appends a following number to a mention.
func TestWrappedRows(t *testing.T) {
	const copyJS = "internal/web/static/js/components/terminal-copy.js"
	cases := []struct {
		name string
		cols int
		text string
		want []string
	}{
		{"claude's hanging indent", 80, "  ⎿  Updated internal/web/static/js/components/te\n     rminal-copy.js (+5 -5)",
			[]string{"internal/web/static/js/components/te " + copyJS, "rminal-copy.js " + copyJS}},
		{"a line and a column over an indent", 80, "  ⎿  Read src/deep/a\n     pp.go:3:7: x", []string{"src/deep/a src/deep/app.go:3:7", "pp.go:3:7 src/deep/app.go:3:7"}},
		{"over three rows at the margin", 80, "  ⎿  Read internal/web/stati\n     c/js/components/termina\n     l-copy.js (+1)",
			[]string{"internal/web/stati " + copyJS, "c/js/components/termina " + copyJS, "l-copy.js " + copyJS}},
		{"a row short of the margin ends the path", 80, "  ⎿  Error at src/deep/ap\n     p.go:12\n     45 lines checked.", []string{"src/deep/ap src/deep/app.go:12", "p.go:12 src/deep/app.go:12"}},
		{"a count below a wrapped line", 80, "  ⎿  Warning in src/deep/ap\n     p.go:2\n     3 more warnings", []string{"src/deep/ap src/deep/app.go:2", "p.go:2 src/deep/app.go:2"}},
		{"a number below a colon", 80, "  ⎿  Wrote tests/e2e/pro\n     be.js:\n     4 lines", []string{"tests/e2e/pro tests/e2e/probe.js", "be.js tests/e2e/probe.js"}},
		{"a numbered list folded narrower than the pane", 80, "1. internal/web/static/js/components/ter\nminal-copy.js:705\n2. next", nil},
		{"a numbered list folded at the pane's width", 40, "1. internal/web/static/js/components/ter\nminal-copy.js:705\n2. next",
			[]string{"internal/web/static/js/components/ter " + copyJS + ":705", "minal-copy.js:705 " + copyJS + ":705"}},
		{"a line the terminal wrapped after a name", 17, "x src/deep/app.go\n:3 ok", []string{"src/deep/app.go src/deep/app.go:3", ":3 src/deep/app.go:3"}},
		{"a wrapped join naming no file", 12, "x src/app.py\nmore text", []string{"src/app.py src/app.py"}},
		{"a line above an indented count", 80, "  see src/app.py:2\n   3 more warnings", []string{"src/app.py:2 src/app.py:2"}},
		{"a line above a count at its indent", 80, "  see src/app.py:2\n  3 more warnings", []string{"src/app.py:2 src/app.py:2"}},
		{"a git status list", 80, "Untracked files:\n\tbuild/\n\tnotes.txt\n", []string{"notes.txt notes.txt"}},
		{"the longest join", 80, "  dist/a\n  pp.css\n  .map", []string{"dist/a dist/app.css.map", "pp.css dist/app.css.map", ".map dist/app.css.map"}},
		{"a join naming no file", 80, "  src/a\n     bc.py", nil},
		{"rows apart by a blank row", 80, "src/de\n\nep/app.go", nil},
		{"a word before the end of its row", 80, "src/de x\nep/app.go", nil},
		{"a word after the start of its row", 80, "src/de\nx ep/app.go", nil},
		{"over a scroll bar", 80, "   under internal/web/static/js/compo ┃\n   nents/terminal-copy.js and   ┃",
			[]string{"internal/web/static/js/compo " + copyJS, "nents/terminal-copy.js " + copyJS}},
		{"a break after a slash", 27, "for internal/web/static/js/\ncomponents/terminal-copy.js and", []string{"internal/web/static/js/ " + copyJS, "components/terminal-copy.js " + copyJS}},
		{"a list of names", 80, "notes.txt\nsrc/app.py\nnotes.txt", []string{"notes.txt notes.txt", "src/app.py src/app.py", "notes.txt notes.txt"}},
		{"beyond four rows", 2, "in\nte\nrn\nal\n/web/static/js/components/terminal-copy.js", nil},
	}
	l := wrapLinker(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, span := range l.rowSpans(tc.text, tc.cols) {
				target := span.ref.Path
				if span.ref.Line > 0 {
					target += fmt.Sprintf(":%d", span.ref.Line)
				}
				if span.ref.Col > 0 {
					target += fmt.Sprintf(":%d", span.ref.Col)
				}
				got = append(got, tc.text[span.start:span.end]+" "+target)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("links %q, want %q", got, tc.want)
			}
		})
	}
}

// The live terminal's words read as the copy view's rows, a wrapped one as
// the words of its rows, apart by line breaks.
func TestWordLinks(t *testing.T) {
	l := wrapLinker(t)
	got := l.wordLinks([]string{"src/app.py:3:7.", "internal/web/static/js/components/te\nrminal-copy.js", "src/app.py:2\n3", "build/\nnotes.txt", "src/😀.txt", "in\nte\nrn\nal\n/web/static/js/components/terminal-copy.js"})
	want := map[string]fileWordLink{
		"src/app.py:3:7.": {URI: "file://" + l.root + "/src/app.py#3:7", End: 14},
		"internal/web/static/js/components/te\nrminal-copy.js": {URI: "file://" + l.root + "/internal/web/static/js/components/terminal-copy.js", Part: 1, End: 14},
		"src/app.py:2\n3": {URI: "file://" + l.root + "/src/app.py#2", End: 12},
		"src/😀.txt":       {URI: "file://" + l.root + "/src/%F0%9F%98%80.txt", End: 10},
	}
	if !maps.Equal(got, want) {
		t.Fatalf("links %+v, want %+v", got, want)
	}
	if links := (*fileLinker)(nil).wordLinks([]string{"notes.txt"}); len(links) != 0 {
		t.Fatalf("a terminal outside every project links %+v", links)
	}
}
