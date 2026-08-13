package editorintelligence

import "testing"

func TestValidPosition(t *testing.T) {
	doc := newDocText("abc\nd𝔘f\n")
	cases := []struct {
		line, char int
		want       bool
	}{
		{0, 0, true},
		{0, 3, true},
		{0, 4, false},
		// The surrogate pair counts as two UTF-16 units.
		{1, 4, true},
		{1, 5, false},
		{2, 0, true},
		{2, 1, false},
		{-1, 0, false},
		{3, 0, false},
		{0, -1, false},
	}
	for _, c := range cases {
		if got := doc.validPosition(c.line, c.char); got != c.want {
			t.Errorf("validPosition(%d, %d) = %v, want %v", c.line, c.char, got, c.want)
		}
	}
}

func TestUTF16Len(t *testing.T) {
	if got := utf16Len("aä𝔘"); got != 4 {
		t.Fatalf("utf16Len = %d, want 4", got)
	}
}
