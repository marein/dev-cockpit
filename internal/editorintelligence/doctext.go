package editorintelligence

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// docText indexes editor content for LSP position math. LSP positions and
// CodeMirror document offsets both count UTF-16 code units, while Go strings
// are UTF-8, so every conversion goes through this index. Lines are split on
// "\n", matching the CodeMirror document model the browser sends.
type docText struct {
	lines    []string
	starts16 []int
}

func newDocText(content string) *docText {
	lines := strings.Split(content, "\n")
	starts := make([]int, len(lines))
	offset := 0
	for i, line := range lines {
		starts[i] = offset
		offset += utf16Len(line) + 1
	}
	return &docText{lines: lines, starts16: starts}
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

func (d *docText) lineCount() int {
	return len(d.lines)
}

// validPosition reports whether line/char address a position inside the
// document, char measured in UTF-16 units.
func (d *docText) validPosition(line, char int) bool {
	if line < 0 || line >= len(d.lines) || char < 0 {
		return false
	}
	return char <= utf16Len(d.lines[line])
}

// offset16 converts an LSP position to an absolute UTF-16 offset. The
// character is clamped to the line length, as the LSP spec prescribes for
// positions past the end of a line.
func (d *docText) offset16(line, char int) (int, bool) {
	if line < 0 || line >= len(d.lines) {
		return 0, false
	}
	length := utf16Len(d.lines[line])
	if char < 0 {
		char = 0
	}
	if char > length {
		char = length
	}
	return d.starts16[line] + char, true
}

// lineByteIndex returns the line's text and the byte index matching the
// UTF-16 character offset, clamped to the line end.
func (d *docText) lineByteIndex(line, char int) (string, bool) {
	if line < 0 || line >= len(d.lines) {
		return "", false
	}
	text := d.lines[line]
	if char <= 0 {
		return text[:0], true
	}
	units := 0
	for i, r := range text {
		if units >= char {
			return text[:i], true
		}
		units++
		if r > 0xFFFF {
			units++
		}
	}
	return text, true
}

// byteOffset returns the byte index of the position in the content string
// the docText was built from.
func (d *docText) byteOffset(line, char int) (int, bool) {
	prefix, ok := d.lineByteIndex(line, char)
	if !ok {
		return 0, false
	}
	start := 0
	for i := 0; i < line; i++ {
		start += len(d.lines[i]) + 1
	}
	return start + len(prefix), true
}

// wordStart16 returns the absolute UTF-16 offset where the identifier-like
// word containing the position starts, and the typed prefix itself. With no
// word before the position it returns the position and an empty prefix.
func (d *docText) wordStart16(line, char int) (int, string) {
	prefix, ok := d.lineByteIndex(line, char)
	if !ok {
		return 0, ""
	}
	i := len(prefix)
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(prefix[:i])
		if !isWordRune(r) {
			break
		}
		i -= size
	}
	word := prefix[i:]
	pos, _ := d.offset16(line, char)
	return pos - utf16Len(word), word
}

func isWordRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
