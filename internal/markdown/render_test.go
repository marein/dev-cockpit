package markdown

import (
	"strings"
	"testing"
)

// The markup goes, the words stay, and nothing that stood on its own line ends
// up glued to the next one.
func TestPlainKeepsTheWordsAndTheirBoundaries(t *testing.T) {
	got := Plain("## The tests\n\nThe **suite** passes, see [the run](https://example.test):\n\n- `go test ./...`\n- the e2e pass\n\n```\ngo build ./...\n```\n")
	for _, want := range []string{"The tests", "The suite passes", "the run", "go test ./...", "the e2e pass", "go build ./..."} {
		if !strings.Contains(got, want) {
			t.Fatalf("want %q in the plain text, got %q", want, got)
		}
	}
	for _, gone := range []string{"##", "**", "](", "```"} {
		if strings.Contains(got, gone) {
			t.Fatalf("want the markup gone, %q is still in %q", gone, got)
		}
	}
	if strings.Contains(got, "testsThe") || strings.Contains(got, "...the") {
		t.Fatalf("two blocks ran into each other: %q", got)
	}
}

// An address that is the text is what it says, so it survives on its own.
func TestPlainKeepsABareLink(t *testing.T) {
	if got := Plain("See https://example.test/run for the log."); !strings.Contains(got, "https://example.test/run") {
		t.Fatalf("want the address kept, got %q", got)
	}
}

// A voice reads the words and the inline commands, never a code block and
// never an address.
func TestSpeechDropsCodeBlocksAndAddresses(t *testing.T) {
	got := Speech("Run `make build` first, see [the docs](https://example.test/docs):\n\n```go\nfunc main() {}\n```\n\nThen open https://example.test/run and check.\n")
	for _, want := range []string{"make build", "the docs", "Then open", "and check"} {
		if !strings.Contains(got, want) {
			t.Fatalf("want %q in the speech text, got %q", want, got)
		}
	}
	for _, gone := range []string{"func main", "https://example.test"} {
		if strings.Contains(got, gone) {
			t.Fatalf("want %q gone from the speech text, got %q", gone, got)
		}
	}
}

// An escaped value reads on the page and in Plain as it was written: no tag
// vanishes, nothing turns bold, struck or linked, and an entity it holds stays
// the text it was.
func TestAnEscapedValueReadsAsWritten(t *testing.T) {
	value := "Fix <form> in __init__ *now* ~~old~~ [x](y) `a` &amp; a|b \\n"
	page, err := RenderGFM("Title: " + Escape(value))
	if err != nil {
		t.Fatal(err)
	}
	if want := "<p>Title: Fix &lt;form&gt; in __init__ *now* ~~old~~ [x](y) `a` &amp;amp; a|b \\n</p>\n"; page != want {
		t.Fatalf("the page reads %q, want %q", page, want)
	}
	if got := Plain("Title: " + Escape(value)); got != "Title: "+value {
		t.Fatalf("Plain reads %q", got)
	}
}

// Plain reads an escape and an entity as the character the page shows, and a
// code span as written.
func TestPlainReadsEscapesAsThePageDoes(t *testing.T) {
	if got, want := Plain("\\_\\_tests\\_\\_ a &lt;b&gt; &#65; \\&lt; `x\\_y &lt;`"), "__tests__ a <b> A &lt; x\\_y &lt;"; got != want {
		t.Fatalf("Plain reads %q, want %q", got, want)
	}
	if got, want := Speech("\\*not bold\\* &amp; more"), "*not bold* & more"; got != want {
		t.Fatalf("Speech reads %q, want %q", got, want)
	}
}
