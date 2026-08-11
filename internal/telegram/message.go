package telegram

import (
	"fmt"
	"strings"
)

// maxMessageRunes is where one outgoing message is cut. Telegram takes 4096,
// the gap leaves room for the part counter and for the fact that Telegram
// counts UTF-16 units, not runes.
const maxMessageRunes = 3500

// splitMessage cuts an answer into what Telegram accepts: at paragraph
// boundaries first, at line boundaries next, and only when neither exists
// inside the window, hard. Several parts are counted, so a reader on a phone
// sees that a wall of text belongs together.
func splitMessage(text string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	parts := chunk(text)
	if len(parts) == 1 {
		return parts
	}
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		out = append(out, fmt.Sprintf("(%d/%d)\n%s", i+1, len(parts), part))
	}
	return out
}

func chunk(text string) []string {
	var parts []string
	rest := []rune(text)
	for len(rest) > maxMessageRunes {
		cut := splitPoint(rest[:maxMessageRunes+1])
		part := strings.TrimRight(string(rest[:cut]), "\n")
		if part != "" {
			parts = append(parts, part)
		}
		rest = rest[cut:]
		for len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:]
		}
	}
	if tail := strings.TrimRight(string(rest), "\n"); tail != "" {
		parts = append(parts, tail)
	}
	if len(parts) == 0 {
		return []string{""}
	}
	return parts
}

// splitPoint is where a window of runes may be cut: after the last paragraph
// break inside it, else after the last line break, else at the window's end.
func splitPoint(window []rune) int {
	limit := len(window) - 1
	if at := lastIndex(window[:limit], "\n\n"); at > 0 {
		return at
	}
	if at := lastIndex(window[:limit], "\n"); at > 0 {
		return at
	}
	return limit
}

// lastIndex is the rune index right after the last occurrence of sep, or -1.
func lastIndex(window []rune, sep string) int {
	needle := []rune(sep)
	for i := len(window) - len(needle); i >= 0; i-- {
		if match(window[i:], needle) {
			return i + len(needle)
		}
	}
	return -1
}

func match(window, needle []rune) bool {
	for i, r := range needle {
		if window[i] != r {
			return false
		}
	}
	return true
}
