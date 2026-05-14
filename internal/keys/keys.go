// Package keys decodes browser-supplied terminal input into tmux send events.
package keys

import "strings"

// Event is one decoded input item: a literal text run when Key is empty,
// otherwise a named tmux key such as "Enter" or "Up".
type Event struct {
	Text string
	Key  string
}

// Decode normalizes line endings and splits raw input into literal runs and
// named keys (Enter, BSpace, Escape, and arrow keys from ANSI sequences).
func Decode(raw string) []Event {
	text := strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\n", "\r")
	var out []Event
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, Event{Text: buf.String()})
			buf.Reset()
		}
	}
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if i+3 <= len(runes) && runes[i] == 0x1b && runes[i+1] == '[' {
			if k, ok := ansiArrow(runes[i+2]); ok {
				flush()
				out = append(out, Event{Key: k})
				i += 2
				continue
			}
		}
		switch runes[i] {
		case '\r':
			flush()
			out = append(out, Event{Key: "Enter"})
		case 0x7f:
			flush()
			out = append(out, Event{Key: "BSpace"})
		case 0x1b:
			flush()
			out = append(out, Event{Key: "Escape"})
		default:
			buf.WriteRune(runes[i])
		}
	}
	flush()
	return out
}

func ansiArrow(r rune) (string, bool) {
	switch r {
	case 'A':
		return "Up", true
	case 'B':
		return "Down", true
	case 'C':
		return "Right", true
	case 'D':
		return "Left", true
	}
	return "", false
}
