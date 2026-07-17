package editorintelligence

import "testing"

func TestDocTextOffsets(t *testing.T) {
	// The second line carries an umlaut (1 UTF-16 unit, 2 bytes) and an
	// emoji (2 UTF-16 units, 4 bytes), the exact case where byte offsets
	// and UTF-16 offsets drift apart.
	doc := newDocText("abc\nä😀x\nlast")

	cases := []struct {
		line, char, want int
	}{
		{0, 0, 0},
		{0, 3, 3},
		{1, 0, 4},
		{1, 1, 5},
		{1, 3, 7},
		{1, 4, 8},
		{2, 4, 13},
	}
	for _, c := range cases {
		got, ok := doc.offset16(c.line, c.char)
		if !ok || got != c.want {
			t.Fatalf("offset16(%d,%d) = %d,%v want %d", c.line, c.char, got, ok, c.want)
		}
	}

	if _, ok := doc.offset16(3, 0); ok {
		t.Fatal("line out of range must fail")
	}
	if got, _ := doc.offset16(0, 99); got != 3 {
		t.Fatalf("char past line end must clamp, got %d", got)
	}
}

func TestDocTextValidPosition(t *testing.T) {
	doc := newDocText("ab\ncd")
	valid := [][2]int{{0, 0}, {0, 2}, {1, 2}}
	invalid := [][2]int{{-1, 0}, {0, 3}, {2, 0}, {0, -1}}
	for _, p := range valid {
		if !doc.validPosition(p[0], p[1]) {
			t.Fatalf("expected %v valid", p)
		}
	}
	for _, p := range invalid {
		if doc.validPosition(p[0], p[1]) {
			t.Fatalf("expected %v invalid", p)
		}
	}
}

func TestDocTextByteOffset(t *testing.T) {
	content := "abc\nä😀x\nlast"
	doc := newDocText(content)
	got, ok := doc.byteOffset(1, 3)
	if !ok {
		t.Fatal("byteOffset failed")
	}
	if content[got:] != "x\nlast" {
		t.Fatalf("byteOffset points at %q", content[got:])
	}
}

func TestWordStart(t *testing.T) {
	doc := newDocText("x := fmt.Prin\nsecond")
	start, prefix := doc.wordStart16(0, 13)
	if prefix != "Prin" || start != 9 {
		t.Fatalf("got start %d prefix %q", start, prefix)
	}
	start, prefix = doc.wordStart16(0, 9)
	if prefix != "" || start != 9 {
		t.Fatalf("after dot: start %d prefix %q", start, prefix)
	}
	start, prefix = doc.wordStart16(0, 0)
	if prefix != "" || start != 0 {
		t.Fatalf("line start: start %d prefix %q", start, prefix)
	}
}

func TestWordStartNonASCII(t *testing.T) {
	doc := newDocText("😀grüße")
	start, prefix := doc.wordStart16(0, 7)
	if prefix != "grüße" || start != 2 {
		t.Fatalf("got start %d prefix %q", start, prefix)
	}
}
