package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/tmux"
)

func writeFiles(t *testing.T, root string, names ...string) {
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

// The live terminal posts the rows around a press, a line break where a
// program may have wrapped, and gets the links of its project's files back.
// tmux is a stub that lists one shell started in the project.
func TestFileLinksAnswerTheFilesOfTheTerminalsProject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(root, "my app")
	writeFiles(t, app, "src/app.go", "notes.txt")
	writeFiles(t, root, "other/secret.go")
	bin := t.TempDir()
	stub := "#!/bin/sh\nprintf 'sh-1\\t42\\t0\\t0\\t0\\tshell\\t%s\\t\\t\\t\\t\\t\\t\\t\\t\\n' '" + app + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := &Server{projects: project.NewRepository(root, nil), shells: shell.NewShells(config.Config{}, tmux.New(), nil, nil)}

	post := func(id, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/shells/"+id+"/file-links", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Params = gin.Params{{Key: "id", Value: id}}
		s.handleFileLinks(c)
		return rec
	}

	text := "see src/a\npp.go:3 and notes.txt, ../other/secret.go " + root + "/other/secret.go"
	body, _ := json.Marshal(map[string]string{"text": text})
	rec := post("sh-1", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var answer struct{ Links []fileLink }
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	want := []fileLink{
		{Start: 4, End: 17, Href: "/projects/my%20app/editor?file=src%2Fapp.go&line=3"},
		{Start: 22, End: 31, Href: "/projects/my%20app/editor?file=notes.txt"},
	}
	if !slices.Equal(answer.Links, want) {
		t.Fatalf("links %+v, want %+v", answer.Links, want)
	}

	if rec := post("sh-2", string(body)); rec.Code != http.StatusNotFound {
		t.Fatalf("a terminal that is not running answered %d", rec.Code)
	}
	if rec := post("sh-1", "texts"); rec.Code != http.StatusBadRequest {
		t.Fatalf("a body that is no JSON answered %d", rec.Code)
	}
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

func findIn(root string) func(string) []fileLink {
	refs := filesystem.FileRefFinder(root)
	return func(text string) []fileLink {
		var links []fileLink
		for _, r := range refs(text) {
			links = append(links, fileLink{Start: r.Start, End: r.End, Href: r.Path})
		}
		return links
	}
}

// linkedTexts answers the text each link covers.
func linkedTexts(text string, links []fileLink) []string {
	units := utf16.Encode([]rune(text))
	var got []string
	for _, link := range links {
		got = append(got, string(utf16.Decode(units[link.Start:link.End])))
	}
	return got
}

func TestWrappedFileLinks(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, "src/deep/app.go", "src/a.go", "src/deep/app.js", "src/deep/app.js.map", "internal/web/static/js/components/terminal-copy.js", "internal/web/static/js/dc/termlinks.js", "notes.txt", "build/notes.txt")
	find := findIn(root)
	pane := func(text string) []fileLink {
		return wrappedFileLinks(text, func(line, next string) bool { return continuesLine(line, next, 10) }, find)
	}
	live := func(text string) []fileLink { return pressedFileLinks(text, find) }
	updated := "  ⎿  Updated internal/web/static/js/components/te"
	cases := []struct {
		name  string
		text  string
		links func(string) []fileLink
		want  []string
	}{
		{"wrapped path", "see src/de\nep/app.go:3 ok", pane, []string{"src/de\nep/app.go:3"}},
		{"wide glyph before", "界 x src/d\neep/app.go", pane, []string{"src/d\neep/app.go"}},
		{"three rows", "xxxxxxxx s\nrc/deep/ap\np.go", pane, []string{"s\nrc/deep/ap\np.go"}},
		{"row not full", "see src/d\neep/app.go", pane, nil},
		{"continuation with space", "s src/a.go\n ep/app.go", pane, []string{"src/a.go"}},
		{"joined names no file, the parts link alone", "x src/a.go\nmore text", pane, []string{"src/a.go"}},
		{"after a bridge", "see src/de\nep/app.go\nand src/a.go", pane, []string{"src/de\nep/app.go", "src/a.go"}},
		{"the live terminal joins where it broke", "see src/d\neep/app.go", live, []string{"src/d\neep/app.go"}},
		{"the live terminal's join names no file", "x src/a.go\nmore", live, []string{"src/a.go"}},
		{"the copy view leaves a hanging indent alone", "⏺ Update(x)\n" + updated + "\n     rminal-copy.js (+5 -5)\n       1 + a", pane, nil},
		{"the live terminal skips a margin and a hanging indent", updated + "   \n     rminal-copy.js (+5 -5)   ", live, []string{"internal/web/static/js/components/te   \n     rminal-copy.js"}},
		{"the live terminal skips claude's no-break space before the first word", "  ⎿ \u00a0Updated internal/web/static/js/dc/ter\n     mlinks.js (+5 -4)", live, []string{"internal/web/static/js/dc/ter\n     mlinks.js"}},
		{"the live terminal skips a margin and an indent of other blanks", "  ⎿ \u00a0Updated src/deep/ap\u3000\n\u00a0\u00a0\u00a0\u00a0\u00a0p.go (+1 -1)", live, []string{"src/deep/ap\u3000\n\u00a0\u00a0\u00a0\u00a0\u00a0p.go"}},
		{"the live terminal ends a mention before an indented row", "  ⎿  Updated src/deep/ap  \n     p.go      \n       1 + a", live, []string{"src/deep/ap  \n     p.go"}},
		{"the live terminal never ends a mention at a row without an indent", "see  src/d\neep/app.go\nx ok", live, nil},
		{"the live terminal's wrong join links nothing", "  ⎿  Updated src/a   \n     bc.go (+1 -1)", live, nil},
		{"the live terminal links the longest join", "  ⎿  Updated src/deep/ap  \n     p.js    \n     .map (+1 -1)", live, []string{"src/deep/ap  \n     p.js    \n     .map"}},
		{"the live terminal joins a line and a column over an indent", "  ⎿  src/de\n     ep/app.go:3:7: x", live, []string{"src/de\n     ep/app.go:3:7"}},
		{"a row's own link stays over an indent below", "  see src/a.go:2\n  3 more warnings", live, []string{"src/a.go:2"}},
		{"a row's own link stays over an indent above", "        build/\n        notes.txt", live, []string{"notes.txt"}},
		{"the live terminal joins over no indent beyond the rows it sends", strings.Repeat("x\n", wrapRows-1) + "  ⎿  src/de\n     ep/app.go", live, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := linkedTexts(tc.text, tc.links(tc.text)); !slices.Equal(got, tc.want) {
				t.Errorf("links %q, want %q", got, tc.want)
			}
		})
	}
}

// The copy view asks once for the links of the whole text and once for those
// of each join of rows filled to the last column: output of one path per line
// joins nothing, indented or not, rows the terminal wrapped over and over, as
// base64 or minified code is, join wrapRows at a time.
func TestTheCopyViewAsksOncePerJoin(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"one path per line", strings.Repeat("./internal/web/static/js/file.js\n", 1000), 1},
		{"an indented path list", strings.Repeat("  ./internal/web/static/js/file.js\n", 1000), 1},
		{"rows filled to the last column", strings.Repeat(strings.Repeat("aB3/+x9Z", 10)+"\n", 100*wrapRows), 100 + 1},
		{"rows filled to the last column with spaces", strings.Repeat(strings.Repeat("aB3/ x9Z", 10)+"\n", 100*wrapRows), 100 + 1},
	}
	for _, tc := range cases {
		asked := 0
		find := func(string) []fileLink {
			asked++
			return nil
		}
		if newestFileLinks(tc.text, 80, find); asked != tc.want {
			t.Errorf("%s: asked %d times, want %d", tc.name, asked, tc.want)
		}
	}
}

// findAGo links the first a.go of a text.
func findAGo(text string) []fileLink {
	var links []fileLink
	if i := strings.Index(text, "a.go"); i >= 0 {
		at := filesystem.UTF16Len(text[:i])
		links = append(links, fileLink{Start: at, End: at + 4, Href: "/e?file=a.go&line=1"})
	}
	return links
}

func TestLinkedHTML(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		budget int
		want   string
	}{
		{"text", "<p>see a.go</p>", 100, `<p>see <a href="/e?file=a.go&amp;line=1" data-file-link>a.go</a></p>`},
		{"entity and wide glyph before", "<p>&lt;界&gt; 😀 a.go</p>", 100, `<p>&lt;界&gt; 😀 <a href="/e?file=a.go&amp;line=1" data-file-link>a.go</a></p>`},
		{"code", "<pre><code>x a.go\n</code></pre>", 100, `<pre><code>x <a href="/e?file=a.go&amp;line=1" data-file-link>a.go</a>
</code></pre>`},
		{"inside a link", `<a href="https://x">a.go</a> a.go`, 100, `<a href="https://x">a.go</a> <a href="/e?file=a.go&amp;line=1" data-file-link>a.go</a>`},
		{"over the budget", "<p>a.go</p><p>x a.go</p>", 5, `<p><a href="/e?file=a.go&amp;line=1" data-file-link>a.go</a></p><p>x a.go</p>`},
		{"a text that fits after one that does not", "<p>x a.go</p><p>a.go</p>", 5, `<p>x a.go</p><p>a.go</p>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			budget := tc.budget
			if got := string(linkedHTML(template.HTML(tc.in), &budget, findAGo)); got != tc.want {
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
	linkCopyViews(views, findAGo)
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

// A history longer than the budget links its newest lines only, at their place
// in the whole text.
func TestALongHistoryLinksItsNewestLinesOnly(t *testing.T) {
	old := "😀 a.go\n" + strings.Repeat("x\n", copyLinkBudget/2)
	links := newestFileLinks(old+"see a.go\n", 80, findAGo)
	at := filesystem.UTF16Len(old) + 4
	if len(links) != 1 || links[0].Start != at || links[0].End != at+4 {
		t.Fatalf("links %+v, want one at %d", links, at)
	}
}
