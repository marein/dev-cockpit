package markdown

import (
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/filesystem"
	"golang.org/x/net/html"
)

func render(t *testing.T, src string) string {
	t.Helper()
	out, err := RenderPreview(src)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

func elements(n *html.Node, tag string) []*html.Node {
	var found []*html.Node
	for d := range n.Descendants() {
		if d.Type == html.ElementNode && d.Data == tag {
			found = append(found, d)
		}
	}
	return found
}

func textOf(n *html.Node) string {
	var b strings.Builder
	for d := range n.Descendants() {
		if d.Type == html.TextNode {
			b.WriteString(d.Data)
		}
	}
	return b.String()
}

// The e2e runner previews the same README.
func TestTheReadmeFoldsItsSections(t *testing.T) {
	src, err := os.ReadFile("../../tests/e2e/editor-preview-gfm.md")
	if err != nil {
		t.Fatal(err)
	}
	out := render(t, string(src))
	for _, want := range []string{"<summary>Start the server</summary>", "<summary>Build from source</summary>", `<blockquote class="border-yellow">`} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %s in %q", want, out)
		}
	}
	doc, err := html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	folds := elements(doc, "details")
	if len(folds) != 2 || strings.Contains(out, "<details open") || len(elements(folds[0], "details")) != 1 {
		t.Fatalf("want two closed folds, one in the other, got %q", out)
	}
	if tables := elements(folds[0], "table"); len(tables) != 1 || len(elements(tables[0], "code")) != 2 {
		t.Fatal("want the table with its inline code inside the fold")
	}
	if code := elements(folds[1], "pre"); len(code) != 1 || !strings.Contains(textOf(code[0]), "go build") {
		t.Fatal("want the code inside the nested fold")
	}
	links := regexp.MustCompile(`href="#([^"]+)"`).FindAllStringSubmatch(out, -1)
	for _, link := range links {
		if id, _ := url.PathUnescape(link[1]); !strings.Contains(out, `id="user-content-`+id+`"`) {
			t.Fatalf("no heading for #%s", link[1])
		}
	}
	if len(links) != 4 {
		t.Fatalf("want the four links of the table of contents, got %d", len(links))
	}
}

func TestNothingThatRunsSurvives(t *testing.T) {
	out := strings.ToLower(render(t, strings.Join([]string{
		"<script>window.x = 1</script>",
		`<img src="missing.png" onerror="window.x = 1">`,
		`<a href="JaVaScRiPt:window.x = 1">raw</a> [md](javascript:window.x)`,
		`<svg onload="window.x = 1"></svg><iframe src="https://example.test"></iframe>`,
		`<style>p{display:none}</style><form action="/x"><button>go</button></form>`,
	}, "\n\n")))
	for _, gone := range []string{"window.x", "<script", "onerror", "javascript", "<svg", "<iframe", "<style", "<form", "<button"} {
		if strings.Contains(out, gone) {
			t.Fatalf("want %q gone, got %q", gone, out)
		}
	}
}

func TestEveryIDIsTheFilesOwn(t *testing.T) {
	out := render(t, "# Title\n\n<a id=\"csrf\" name=\"pe\">a</a><map name=\"m\"></map>\n")
	ids := regexp.MustCompile(`\sid="([^"]*)"`).FindAllStringSubmatch(out, -1)
	for _, id := range ids {
		if !strings.HasPrefix(id[1], "user-content-") {
			t.Fatalf("want every id prefixed, got %q", out)
		}
	}
	if len(ids) != 2 || strings.Contains(out, "name=") {
		t.Fatalf("want two ids and no name, got %q", out)
	}
}

func TestHeadingIDsAreGitHubs(t *testing.T) {
	out := render(t, "# Über uns!\n\n## Über uns\n\n### Ü\n")
	ids := regexp.MustCompile(`\sid="([^"]*)"`).FindAllStringSubmatch(out, -1)
	var got []string
	for _, id := range ids {
		got = append(got, id[1])
	}
	if want := "user-content-über-uns user-content-über-uns-1 user-content-ü"; strings.Join(got, " ") != want {
		t.Fatalf("want ids %s, got %q", want, out)
	}
}

func TestGitHubMarkupRenders(t *testing.T) {
	out := render(t, `<p align="center"><img src="https://example.test/logo.png" alt="Logo" width="120"></p>

Press <kbd>Ctrl</kbd>, H<sub>2</sub>O, ~~old~~, [web](https://example.test) and [here](#overview).

- [x] done

| Left | Right |
|:-----|------:|
| a    | b     |
`)
	for _, want := range []string{
		`<p align="center"><img src="https://example.test/logo.png" alt="Logo" width="120"></p>`,
		"<kbd>Ctrl</kbd>", "<sub>2</sub>", "<del>old</del>",
		`<a href="https://example.test" rel="nofollow noopener" target="_blank">web</a>`, `<a href="#overview" rel="nofollow">here</a>`,
		`<input checked="" disabled="" type="checkbox">`, `<th align="left">Left</th>`, `<td align="right">b</td>`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %s in %q", want, out)
		}
	}
	if _, err := RenderPreview(strings.Repeat("a", filesystem.MaxEditableBytes+1)); !errors.Is(err, filesystem.ErrTooLarge) {
		t.Fatalf("want a file past the editor's limit refused, got %v", err)
	}
}
