package tmux

import "testing"

func TestFilterKeepsHyperlinksAcrossChunks(t *testing.T) {
	var f OSCFilter
	var got []byte
	for _, chunk := range []string{"a\x1b]0;title\x07b\x1b]", "8;id=1;file:///p/x.go\x07x.go\x1b", "]8;;\x1b\\c"} {
		got = append(got, f.Filter([]byte(chunk))...)
	}
	want := "ab\x1b]8;id=1;file:///p/x.go\x1b\\x.go\x1b]8;;\x1b\\c"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFilterMarksStripsHyperlinks(t *testing.T) {
	var f OSCFilter
	out, marks := f.FilterMarks([]byte("\x1b]8;;file:///p/x.go\x07x.go\x1b]8;;\x07"))
	if string(out) != "x.go" || len(marks) != 2 {
		t.Fatalf("got %q %q", out, marks)
	}
}

func TestFilterAbortsMalformedHyperlinks(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"\x1b]8;;http://x\x1b]11;?\x07", "\x1b]8;;http://x\x18"},
		{"\x1b]8;;x\x1b[2J\x1b]0;t\x07", "\x1b]8;;x\x18"},
		{"\x1b]8;;x\x1b\ny\x07z", "\x1b]8;;x\x18z"},
		{"\x1b]8;;x\x1b\x07z", "\x1b]8;;x\x18z"},
		{"\x1b]8;id=\n;x\x07z", "\x1b]8;id=\x18z"},
		{"\x1b]8;;x\x9b2J\x07z", "\x1b]8;;x\x18z"},
		{"\x1b]8;;x\x9d11;?\x07z", "\x1b]8;;x\x18z"},
		{"\x1b]8;;x\x9c\x1b]11;?\x07z", "\x1b]8;;x\x18z"},
		{"\x1b]8;;x\xc2\x9b2J\x07z", "\x1b]8;;x\x18z"},
		{"\x1b]8;;x\xc2\x9d11;?\x07z", "\x1b]8;;x\x18z"},
		{"\x1b]8;;x\xc2\x9c\x1b]11;?\x07z", "\x1b]8;;x\x18z"},
		{"a\xc2\x9d11;?\x07b", "ab"},
		{"a\xc2\xb0\x9d", "a\xc2\xb0\x9d"},
		{"\x1b]8;;http://x\x07t\x1b]8;;\x07", "\x1b]8;;http://x\x1b\\t\x1b]8;;\x1b\\"},
		{"\x1b]8;;http://x\x1b\\t\x1b]8;;\x1b\\", "\x1b]8;;http://x\x1b\\t\x1b]8;;\x1b\\"},
	} {
		for k := 0; k <= len(c.in); k++ {
			var f OSCFilter
			got := string(f.Filter([]byte(c.in[:k]))) + string(f.Filter([]byte(c.in[k:])))
			if got != c.want {
				t.Errorf("%q split at %d: got %q, want %q", c.in, k, got, c.want)
			}
		}
	}
}
