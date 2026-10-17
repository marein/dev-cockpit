package plaintext

import "testing"

// What could break a line, hide in it or turn it around shows as its escape,
// text and a tab stay as written.
func TestVisibleEscapesWhatShowsNothing(t *testing.T) {
	for in, want := range map[string]string{
		"a\tb `c` *d* <e> & f":                     "a\tb `c` *d* <e> & f",
		"a\nb\rc\u0085d\u2028e\u2029f":             `a\nb\rc\u0085d\u2028e\u2029f`,
		"\x1b[31m\f\x00\x7f":                       `\x1b[31m\f\x00\x7f`,
		"\u2003x\u00a0y\u202ez\u2066w\u200b\ufeff": `\u2003x\u00a0y\u202ez\u2066w\u200b\ufeff`,
	} {
		if got := Visible(in); got != want {
			t.Fatalf("Visible(%q) = %q, want %q", in, got, want)
		}
	}
}
