// DisplayName, ShortID and ShortTitle format labels for coder sessions.
package coder

import "strings"

// DisplayName returns name when present, otherwise a stable short fallback.
func DisplayName(name, sessionID string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	return "coder-" + ShortID(sessionID)
}

// ShortID returns a compact form of an identifier for display.
func ShortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// TitleRunes bounds a title the cockpit shortens itself. It is about what a
// tab shows, and about the length claude's own generated titles come in.
const TitleRunes = 32

// ShortTitle cuts a text down to a label: its first line of substance,
// whitespace collapsed, at most TitleRunes runes, on a word boundary where
// there is one. A coder that titles a session with the prompt it was given
// (copilot does, claude's own title is already short) would otherwise put a
// paragraph in a tab strip.
func ShortTitle(text string) string {
	line := ""
	for _, candidate := range strings.Split(text, "\n") {
		if line = strings.Join(strings.Fields(candidate), " "); line != "" {
			break
		}
	}
	runes := []rune(line)
	if len(runes) <= TitleRunes {
		return line
	}
	cut := string(runes[:TitleRunes])
	if space := strings.LastIndex(cut, " "); space > TitleRunes/2 {
		cut = cut[:space]
	}
	return strings.TrimRight(cut, " ") + "\u2026"
}
