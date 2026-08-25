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
