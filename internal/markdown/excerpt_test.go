package markdown

import "testing"

// A preview is one run of words, whatever the source did with its lines: a
// report whose first line is half a sentence reads as half a sentence, and
// the preview a push shows carries the paragraph behind it too.
func TestExcerptFoldsLinesIntoOneRun(t *testing.T) {
	line, more := Excerpt("## Done\n\nThe tests pass\nand the branch is pushed.", 140)
	if line != "Done The tests pass and the branch is pushed." {
		t.Fatalf("want one run of words without the markup, got %q", line)
	}
	if more {
		t.Fatal("nothing was left behind, so nothing stands behind it")
	}
}

// What was cut says so, and it says it on a word boundary.
func TestExcerptCutsOnAWordAndSaysThereIsMore(t *testing.T) {
	line, more := Excerpt("cockpit steers the coder and reports back", 20)
	if !more {
		t.Fatalf("a cut text holds more, got %q", line)
	}
	if line != "cockpit steers the…" {
		t.Fatalf("want the cut on a word boundary, got %q", line)
	}
	if n := len([]rune(line)); n > 20 {
		t.Fatalf("the mark counts against the room: %d runes in %q", n, line)
	}
}

// A word longer than the room has no boundary to give way at, so it is cut at
// the rune rather than disappearing.
func TestExcerptCutsAWordThatFillsTheRoom(t *testing.T) {
	line, more := Excerpt("notification-titel-reihenfolge", 10) //nolint:misspell
	if !more || line != "notificat…" {
		t.Fatalf("want the rune cut, got %q (%v)", line, more)
	}
}
