package tmux

import (
	"bytes"
	"slices"
	"testing"
)

func TestFilterMarksSplitsBellsFromSequences(t *testing.T) {
	for _, c := range []struct {
		in, out string
		marks   []string
	}{
		{"a\x1b]133;C\x07b\x07", "ab\x07", []string{"133;C"}},
		{"\x1b]8;;file:///p/x.go\x07x.go\x1b]8;;\x1b\\", "x.go", []string{"8;;file:///p/x.go", "8;;"}},
		{"\x1bPtmux;\x1b\x1b]52;c;RE9ORQ==\x07\x1b\\\x07", "\x1bPtmux;\x1b\x07", []string{"52;c;RE9ORQ=="}},
		{"\xc2\x9d133;D\xc2\x9c\x07", "\x07", []string{"133;D"}},
		{"\x1b]0;never ended\x1b[1m\x1b]133;A\x07", "\x1b[1m", []string{"0;never ended", "133;A"}},
		{"\x1b]0;cancelled\x18\x07", "\x07", nil},
		{"\xc2\xb0\x1b[2J", "\xc2\xb0\x1b[2J", nil},
	} {
		for k := 0; k <= len(c.in); k++ {
			var f OSCFilter
			out, marks := f.FilterMarks([]byte(c.in[:k]))
			out2, marks2 := f.FilterMarks([]byte(c.in[k:]))
			out, marks = append(out, out2...), append(marks, marks2...)
			if string(out) != c.out || !slices.Equal(marks, c.marks) {
				t.Errorf("%q split at %d: got %q %q, want %q %q", c.in, k, out, marks, c.out, c.marks)
			}
		}
	}
}

func FuzzFilterMarks(f *testing.F) {
	f.Add([]byte("\x1b]0;open"), uint(3))
	f.Add([]byte("\xc2\x9d8;;x\xc2"), uint(1))
	f.Add([]byte("\x1bPtmux;\x1b\x1b]52;c;"), uint(9))
	f.Fuzz(func(t *testing.T, junk []byte, split uint) {
		const tail = "\x1b]133;C\x07\x1b]0;t\x07\x07"
		var whole, parts OSCFilter
		wantOut, wantMarks := whole.FilterMarks(junk)
		at := int(split % uint(len(junk)+1))
		out, marks := parts.FilterMarks(junk[:at])
		out2, marks2 := parts.FilterMarks(junk[at:])
		if !bytes.Equal(append(out, out2...), wantOut) || !slices.Equal(append(marks, marks2...), wantMarks) {
			t.Fatalf("split at %d changed the result", at)
		}
		if len(parts.payload) > maxOSCPayload {
			t.Fatalf("payload grew to %d", len(parts.payload))
		}
		out, marks = parts.FilterMarks([]byte(tail))
		if bytes.Count(out, []byte{0x07}) != 1 || len(marks) < 2 || !slices.Equal(marks[len(marks)-2:], []string{"133;C", "0;t"}) {
			t.Fatalf("after %q: got %q %q", junk, out, marks)
		}
	})
}
