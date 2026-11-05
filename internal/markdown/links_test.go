package markdown

import (
	"strings"
	"testing"
)

const blank = `target="_blank" rel="noopener noreferrer"`

func TestExternalLinksOpenInANewWindow(t *testing.T) {
	cases := []struct {
		name, src string
		external  bool
	}{
		{"https", "[a](https://example.test/x)", true},
		{"http", "[a](http://example.test)", true},
		{"uppercase scheme", "[a](HTTPS://example.test)", true},
		{"mixed case scheme", "[a](hTtP://example.test)", true},
		{"protocol relative", "[a](//example.test/x)", true},
		{"autolink angle", "<https://example.test>", true},
		{"autolink bare", "see https://example.test/run now", true},
		{"autolink www", "see www.example.test now", true},
		{"relative path", "[a](/projects/x)", false},
		{"relative file", "[a](docs/a.md)", false},
		{"query", "[a](?page=2)", false},
		{"anchor", "[a](#top)", false},
		{"mailto", "[a](mailto:a@example.test)", false},
		{"tel", "[a](tel:+4912345)", false},
		{"email autolink", "<a@example.test>", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RenderGFM(c.src)
			if err != nil {
				t.Fatal(err)
			}
			if has := strings.Contains(got, blank); has != c.external {
				t.Fatalf("external=%v, got %q", c.external, got)
			}
			if n := strings.Count(got, "target="); n > 1 {
				t.Fatalf("target doubled in %q", got)
			}
		})
	}
}

func TestExternalLinksThroughTheMediaRenderer(t *testing.T) {
	resolve := func(d string) (Media, bool) {
		if d == "out.mp4" {
			return Media{URL: "/m/out.mp4", Kind: "video"}, true
		}
		if d == "doc.pdf" {
			return Media{URL: "/m/doc.pdf", Kind: "file"}, true
		}
		return Media{}, false
	}
	before, _, err := RenderGFMWithMediaAround("[x](https://example.test) [v](out.mp4) [f](doc.pdf) <https://a.test> [r](/p)", resolve, -1)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(before, blank); n != 2 {
		t.Fatalf("want two external links, got %d in %q", n, before)
	}
	for _, want := range []string{`<video src="/m/out.mp4"`, `<a href="/m/doc.pdf" download>`, `<a href="/p">`} {
		if !strings.Contains(before, want) {
			t.Fatalf("want %q in %q", want, before)
		}
	}
	if strings.Contains(before, "https://example.test") && strings.Count(before, "target=") != 2 {
		t.Fatalf("target doubled in %q", before)
	}
}
