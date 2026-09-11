package render

import "testing"

// The label is display text, so a brand casing the plain capitalization
// cannot produce is special cased, while the ids stay lowercase everywhere
// they are ids.
func TestCoderLabelUsesTheBrandCasing(t *testing.T) {
	cases := map[string]string{
		"claude":   "Claude",
		"copilot":  "Copilot",
		"opencode": "OpenCode",
		"":         "",
	}
	for id, want := range cases {
		if got := CoderLabel(id); got != want {
			t.Errorf("CoderLabel(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestNoticeHTMLSetsQuotedNamesInMonospace(t *testing.T) {
	got := string(NoticeHTML(`Shell "a <b>" deleted.`))
	want := `Shell <span class="dc-mono">a &lt;b&gt;</span> deleted.`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := string(NoticeHTML(`an "unpaired quote`)); got != "an &#34;unpaired quote" {
		t.Fatalf("unpaired quote: got %q", got)
	}
}
