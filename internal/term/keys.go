package term

import (
	"fmt"
	"strings"
)

// KeyBytes translates a key name (as produced by provider.ControlMapper and
// keys.Decode — e.g. "Up", "Enter", "C-c", "C-Home", "M-Left") into the raw
// bytes a program reads from its terminal. appCursor selects the application
// cursor-key encoding (ESC O x) over the normal one (ESC [ x) for unmodified
// arrows and must reflect the live DECCKM state of the program.
//
// The encodings match what tmux send-keys emits, which is what the supported
// TUIs expect: modified cursor/editing keys use the xterm form
// ESC [ <param> ; <mod> <final>, where mod = 1 + Shift + 2·Alt + 4·Ctrl.
func KeyBytes(name string, appCursor bool) ([]byte, bool) {
	shift, alt, ctrl := false, false, false
	for {
		switch {
		case strings.HasPrefix(name, "C-"):
			ctrl = true
			name = name[2:]
		case strings.HasPrefix(name, "M-"):
			alt = true
			name = name[2:]
		case strings.HasPrefix(name, "S-"):
			shift = true
			name = name[2:]
		default:
			return encodeKey(name, appCursor, shift, alt, ctrl)
		}
	}
}

// csiLetterKeys are the cursor and Home/End keys, encoded as a final CSI letter.
var csiLetterKeys = map[string]byte{
	"Up": 'A', "Down": 'B', "Right": 'C', "Left": 'D', "Home": 'H', "End": 'F',
}

// csiTildeKeys are the editing/navigation keys encoded as ESC [ <num> ~.
var csiTildeKeys = map[string]int{
	"Home": 1, "Insert": 2, "IC": 2, "Delete": 3, "DC": 3, "End": 4,
	"PageUp": 5, "PageDown": 6,
}

func encodeKey(name string, appCursor, shift, alt, ctrl bool) ([]byte, bool) {
	mod := 1
	if shift {
		mod++
	}
	if alt {
		mod += 2
	}
	if ctrl {
		mod += 4
	}
	modified := mod > 1

	if letter, ok := csiLetterKeys[name]; ok {
		if modified {
			// Modified cursor/Home/End always use the CSI letter form, even in
			// application cursor mode.
			return []byte(fmt.Sprintf("\x1b[1;%d%c", mod, letter)), true
		}
		switch name {
		case "Home":
			return []byte{0x1b, '[', '1', '~'}, true
		case "End":
			return []byte{0x1b, '[', '4', '~'}, true
		default:
			if appCursor {
				return []byte{0x1b, 'O', letter}, true
			}
			return []byte{0x1b, '[', letter}, true
		}
	}

	if num, ok := csiTildeKeys[name]; ok {
		if modified {
			return []byte(fmt.Sprintf("\x1b[%d;%d~", num, mod)), true
		}
		return []byte(fmt.Sprintf("\x1b[%d~", num)), true
	}

	var b []byte
	switch name {
	case "Enter":
		b = []byte{'\r'}
	case "Tab":
		b = []byte{'\t'}
	case "BTab":
		b = []byte{0x1b, '[', 'Z'}
	case "Space":
		b = []byte{' '}
	case "Escape":
		b = []byte{0x1b}
	case "BSpace":
		b = []byte{0x7f}
	default:
		if len([]rune(name)) != 1 {
			return nil, false
		}
		b = []byte(name)
	}
	if ctrl {
		b = applyCtrl(b)
	}
	if alt {
		b = append([]byte{0x1b}, b...)
	}
	return b, true
}

// applyCtrl folds a single printable byte into its control-character form, the
// way a terminal does when Ctrl is held.
func applyCtrl(b []byte) []byte {
	if len(b) != 1 {
		return b
	}
	c := b[0]
	switch {
	case c >= 'a' && c <= 'z':
		return []byte{c - 'a' + 1}
	case c >= 'A' && c <= 'Z':
		return []byte{c - 'A' + 1}
	case c == ' ' || c == '@':
		return []byte{0x00}
	case c == '[':
		return []byte{0x1b}
	case c == '\\':
		return []byte{0x1c}
	case c == ']':
		return []byte{0x1d}
	case c == '^':
		return []byte{0x1e}
	case c == '_' || c == '/':
		return []byte{0x1f}
	default:
		return b
	}
}

// bracketedPaste wraps text in the paste markers when the program enabled
// bracketed paste mode (DECSET 2004); otherwise it returns the text unchanged.
// Newlines are normalised to carriage returns, matching how a terminal delivers
// a paste.
func bracketedPaste(text string, enabled bool) []byte {
	normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\r"), "\n", "\r")
	if !enabled {
		return []byte(normalized)
	}
	out := make([]byte, 0, len(normalized)+12)
	out = append(out, 0x1b, '[', '2', '0', '0', '~')
	out = append(out, normalized...)
	out = append(out, 0x1b, '[', '2', '0', '1', '~')
	return out
}
