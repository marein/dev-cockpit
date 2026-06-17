package term

import (
	"bytes"
	"testing"
)

func TestKeyBytes(t *testing.T) {
	cases := []struct {
		name      string
		appCursor bool
		want      []byte
		ok        bool
	}{
		{"Enter", false, []byte{'\r'}, true},
		{"BSpace", false, []byte{0x7f}, true},
		{"Escape", false, []byte{0x1b}, true},
		{"Tab", false, []byte{'\t'}, true},
		{"BTab", false, []byte{0x1b, '[', 'Z'}, true},
		{"Up", false, []byte{0x1b, '[', 'A'}, true},
		{"Up", true, []byte{0x1b, 'O', 'A'}, true},
		{"Left", false, []byte{0x1b, '[', 'D'}, true},
		{"Home", true, []byte{0x1b, '[', '1', '~'}, true},
		{"Home", false, []byte{0x1b, '[', '1', '~'}, true},
		{"End", true, []byte{0x1b, '[', '4', '~'}, true},
		{"PageUp", false, []byte{0x1b, '[', '5', '~'}, true},
		{"C-c", false, []byte{0x03}, true},
		{"C-a", false, []byte{0x01}, true},
		{"C-C", false, []byte{0x03}, true},
		{"M-x", false, []byte{0x1b, 'x'}, true},
		{"C-M-c", false, []byte{0x1b, 0x03}, true},
		// Modified cursor/editing keys: xterm CSI form, matching tmux.
		{"C-Home", false, []byte("\x1b[1;5H"), true},
		{"C-Home", true, []byte("\x1b[1;5H"), true},
		{"C-End", false, []byte("\x1b[1;5F"), true},
		{"C-Up", false, []byte("\x1b[1;5A"), true},
		{"M-Left", false, []byte("\x1b[1;3D"), true},
		{"S-Home", false, []byte("\x1b[1;2H"), true},
		{"C-PageUp", false, []byte("\x1b[5;5~"), true},
		{"a", false, []byte{'a'}, true},
		{"Unknown", false, nil, false},
	}
	for _, c := range cases {
		got, ok := KeyBytes(c.name, c.appCursor)
		if ok != c.ok {
			t.Errorf("KeyBytes(%q,%v) ok=%v want %v", c.name, c.appCursor, ok, c.ok)
			continue
		}
		if ok && !bytes.Equal(got, c.want) {
			t.Errorf("KeyBytes(%q,%v)=%v want %v", c.name, c.appCursor, got, c.want)
		}
	}
}

func TestBracketedPaste(t *testing.T) {
	if got := bracketedPaste("a\nb", false); !bytes.Equal(got, []byte("a\rb")) {
		t.Errorf("unbracketed=%q", got)
	}
	want := append(append([]byte{0x1b, '[', '2', '0', '0', '~'}, []byte("a\rb")...), 0x1b, '[', '2', '0', '1', '~')
	if got := bracketedPaste("a\nb", true); !bytes.Equal(got, want) {
		t.Errorf("bracketed=%q", got)
	}
}
