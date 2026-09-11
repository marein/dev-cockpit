package render

import (
	"strings"
	"testing"
)

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

// The notice is where a flash names a session, a shell or a project, and the
// name stands in double quotes the way the handlers write it. The template
// prints the message as text, so the quotes survive and markup in a name
// cannot become markup.
func TestNoticeKeepsQuotedNamesAndEscapes(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil)
	var out strings.Builder
	err := tmpl.ExecuteTemplate(&out, "notice.gohtml", map[string]any{
		"Level":   "success",
		"Message": `Shell "a <b>" deleted.`,
		"Dismiss": true,
	})
	if err != nil {
		t.Fatalf("render notice: %v", err)
	}
	if got := out.String(); !strings.Contains(got, `Shell &#34;a &lt;b&gt;&#34; deleted.`) {
		t.Fatalf("notice output misses the quoted, escaped name: %q", got)
	}
}
