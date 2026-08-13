package editorintelligence

import "strings"

// docText indexes editor content for LSP position checks. LSP positions and
// CodeMirror document offsets both count UTF-16 code units, while Go strings
// are UTF-8, so the length math goes through this index. Lines are split on
// "\n", matching the CodeMirror document model the browser sends.
type docText struct {
	lines []string
}

func newDocText(content string) *docText {
	return &docText{lines: strings.Split(content, "\n")}
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

// validPosition reports whether line/char address a position inside the
// document, char measured in UTF-16 units.
func (d *docText) validPosition(line, char int) bool {
	if line < 0 || line >= len(d.lines) || char < 0 {
		return false
	}
	return char <= utf16Len(d.lines[line])
}
