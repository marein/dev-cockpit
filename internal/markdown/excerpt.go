package markdown

import (
	"strings"
	"unicode/utf8"
)

// Excerpt is the one line a preview of a Markdown text shows, and whether
// anything stands behind it. The text is written for a page, so the markup
// goes through the parser instead of a hand-rolled cut, and everything that
// was a line break or an indent becomes one space: a preview lands in a single
// run of words wherever it surfaces, and how the source broke its lines
// decides nothing about how much of it a reader sees.
//
// The cut sits on a word boundary, a word torn in half reads as a rendering
// fault, but only where that boundary is past half of the room: a line with no
// boundary at all ("notification-titel-reihenfolge") and one whose last
// boundary sits near its start ("Coder asks: git" in twelve runes) are cut at
// the rune instead, because giving up more than half the room buys a whole
// word with what was the difference between two of these lines. That is the
// same trade coder.ShortTitle makes for a session label. The mark that says it
// goes on counts against max, so a budget means the same thing wherever it is
// measured.
func Excerpt(src string, max int) (string, bool) {
	line := strings.Join(strings.Fields(Plain(src)), " ")
	if max <= 0 {
		return "", line != ""
	}
	if utf8.RuneCountInString(line) <= max {
		return line, false
	}
	cut := runePrefix(line, max-1)
	if space := strings.LastIndexByte(cut, ' '); space > max/2 {
		cut = cut[:space]
	}
	return cut + "…", true
}

// runePrefix is the first max runes of s.
func runePrefix(s string, max int) string {
	runes := 0
	for i := range s {
		if runes == max {
			return s[:i]
		}
		runes++
	}
	return s
}
