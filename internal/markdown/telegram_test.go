package markdown

import (
	"strings"
	"testing"
)

func TestTelegramHTMLTranslatesTheMarkupTelegramKnows(t *testing.T) {
	for _, tt := range []struct {
		name string
		src  string
		want string
	}{
		{"bold", "this is **bold** text", "this is <b>bold</b> text"},
		{"italic", "this is *slanted* text", "this is <i>slanted</i> text"},
		{"code", "run `go test ./...` first", "run <code>go test ./...</code> first"},
		{"strikethrough", "~~gone~~ now", "<s>gone</s> now"},
		{"link", "see [the docs](https://example.org/a)", `see <a href="https://example.org/a">the docs</a>`},
		{"autolink", "at <https://example.org>", `at <a href="https://example.org">https://example.org</a>`},
		{"heading", "# The heading", "<b>The heading</b>"},
		{"code block", "```go\nfunc main() {}\n```", `<pre><code class="language-go">func main() {}</code></pre>`},
		{"code block without a language", "```\nplain\n```", "<pre>plain</pre>"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenderTelegramHTML(tt.src); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The three characters Telegram asks for, in every layer they can turn up in.
// This is what keeps an answer full of generics and shell redirections from
// being refused as a whole.
func TestTelegramHTMLEscapesTheThreeCharactersEverywhere(t *testing.T) {
	for _, tt := range []struct {
		name string
		src  string
		want string
	}{
		{"in text", "a < b && c > d", "a &lt; b &amp;&amp; c &gt; d"},
		{"in bold", "**a<b&c>d**", "<b>a&lt;b&amp;c&gt;d</b>"},
		{"in italic", "*a<b&c>d*", "<i>a&lt;b&amp;c&gt;d</i>"},
		{"in code", "`map[string]<T> && x`", "<code>map[string]&lt;T&gt; &amp;&amp; x</code>"},
		{"in a code block", "```\nif a < b && c > d {\n```", "<pre>if a &lt; b &amp;&amp; c &gt; d {</pre>"},
		{"in a link label", "[a<b&c>d](https://example.org)", `<a href="https://example.org">a&lt;b&amp;c&gt;d</a>`},
		{"in a link address", "[x](https://example.org/?a=1&b=2)", `<a href="https://example.org/?a=1&amp;b=2">x</a>`},
		{"in strikethrough", "~~a<b&c>d~~", "<s>a&lt;b&amp;c&gt;d</s>"},
		{"raw html in the answer", "a <script>alert(1)</script> b", "a &lt;script&gt;alert(1)&lt;/script&gt; b"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenderTelegramHTML(tt.src); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTelegramHTMLKeepsWhatItCannotMap(t *testing.T) {
	for _, tt := range []struct {
		name     string
		src      string
		contains []string
	}{
		{"a list keeps its markers", "- one\n- two", []string{"- one", "- two"}},
		{"an ordered list keeps its numbers", "1. one\n2. two", []string{"1. one", "2. two"}},
		{"a table becomes a code block", "| a | b |\n| --- | --- |\n| 1 | 2 |", []string{"<pre>", "a | b", "1 | 2"}},
		{"a quote keeps its words", "> quoted", []string{"quoted"}},
		{"an image keeps what it says", "![a shot](https://example.org/a.png)", []string{"a shot", "example.org/a.png"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderTelegramHTML(tt.src)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Fatalf("%q is missing from %q", want, got)
				}
			}
		})
	}
}

func TestTelegramHTMLUsesNoTagTelegramDoesNotKnow(t *testing.T) {
	// Everything a real answer throws at it, in one go.
	src := "# Heading\n\nA **bold** and *slanted* line with `code`, a [link](https://example.org) and ~~a strike~~.\n\n" +
		"- a list item\n- another one\n\n```go\nfunc main() {}\n```\n\n> a quote\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n"
	got := RenderTelegramHTML(src)
	allowed := map[string]bool{"b": true, "i": true, "code": true, "pre": true, "a": true, "s": true}
	for _, tag := range tagsIn(got) {
		if !allowed[tag] {
			t.Fatalf("the output carries <%s>, which Telegram does not parse:\n%s", tag, got)
		}
	}
}

// tagsIn collects the element names of the rendered output.
func tagsIn(html string) []string {
	var names []string
	for i := 0; i < len(html); i++ {
		if html[i] != '<' {
			continue
		}
		end := strings.IndexByte(html[i:], '>')
		if end < 0 {
			break
		}
		tag := html[i+1 : i+end]
		tag = strings.TrimPrefix(tag, "/")
		if space := strings.IndexByte(tag, ' '); space >= 0 {
			tag = tag[:space]
		}
		names = append(names, tag)
		i += end
	}
	return names
}

func TestTelegramHTMLLeavesAnUnknownSchemeAsWords(t *testing.T) {
	// An href Telegram refuses would take the whole message with it.
	got := RenderTelegramHTML("[open it](javascript:alert(1))")
	if strings.Contains(got, "href") || strings.Contains(got, "javascript") {
		t.Fatalf("an unknown scheme reached the output: %q", got)
	}
	if !strings.Contains(got, "open it") {
		t.Fatalf("the words were lost: %q", got)
	}
}
