// Package plaintext writes stored values out as lines of plain text.
package plaintext

import (
	"strconv"
	"strings"
	"unicode"
)

// Visible is a stored value the way a line of plain text prints it: a rune
// that shows nothing, a line break, a control character, a Unicode space or a
// direction mark, is written as its Go escape, so what a writer stored can
// neither start a line nor hide in one. A tab is code's own indent and stays.
func Visible(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\t' || unicode.IsPrint(r) {
			b.WriteRune(r)
			continue
		}
		quoted := strconv.QuoteRune(r)
		b.WriteString(quoted[1 : len(quoted)-1])
	}
	return b.String()
}
