package markdown

import (
	"reflect"
	"strings"
	"testing"
)

func TestFencesAreTheTopLevelBlocksInOrder(t *testing.T) {
	src := "```go\nx := 1\n```\n\n- item\n\n  ```ask\n  nested\n  ```\n\n~~~ask extra\nA?\n- b\n~~~\n"
	want := []Fence{{Language: "go", Body: "x := 1\n"}, {Language: "ask", Body: "A?\n- b\n"}}
	if got := Fences(src); !reflect.DeepEqual(got, want) {
		t.Fatalf("want %+v, got %+v", want, got)
	}
}

func TestRenderingAroundAFenceLeavesItOut(t *testing.T) {
	src := "See [the docs][d].\n\n```ask\nA?\n```\n\n**after** [again][d]\n\n[d]: https://example.com\n"
	none := func(string) (Media, bool) { return Media{}, false }
	before, after, err := RenderGFMWithMediaAround(src, none, 0)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if before != "<p>See <a href=\"https://example.com\">the docs</a>.</p>\n" {
		t.Fatalf("unexpected before: %q", before)
	}
	if after != "<p><strong>after</strong> <a href=\"https://example.com\">again</a></p>\n" {
		t.Fatalf("unexpected after: %q", after)
	}
	whole, rest, err := RenderGFMWithMediaAround(src, none, 1)
	plain, _, _ := RenderGFMWithMediaAround(src, none, -1)
	if err != nil || rest != "" || whole != plain || !strings.Contains(whole, "language-ask") {
		t.Fatalf("want the whole document for a fence that is not there, got %q %q %v", whole, rest, err)
	}
}
