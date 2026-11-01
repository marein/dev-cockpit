package plaintext

import (
	"strings"
	"unicode/utf8"
)

// Terminal is output written for a terminal as the plain text lines one
// shows of it: an escape sequence goes, a carriage return starts its line
// over and what follows overwrites it, an erase in line clears what it
// names, a backspace steps back, and every other control character goes. A
// tab and the line breaks stay, invalid UTF-8 reads as the replacement rune.
func Terminal(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	var out strings.Builder
	var line []rune
	col := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\x1b':
			n, erase := escape(s[i:])
			switch erase {
			case eraseToEnd:
				line = line[:min(col, len(line))]
			case eraseToCursor:
				for j := 0; j <= col && j < len(line); j++ {
					line[j] = ' '
				}
			case eraseLine:
				line = line[:0]
			}
			i += n
			continue
		case r == '\n':
			out.WriteString(string(line))
			out.WriteByte('\n')
			line, col = line[:0], 0
		case r == '\r':
			col = 0
		case r == '\b':
			col = max(col-1, 0)
		case r == '\t' || r >= 0x20 && r != 0x7f && (r < 0x80 || r >= 0xa0):
			for len(line) < col {
				line = append(line, ' ')
			}
			if col < len(line) {
				line[col] = r
			} else {
				line = append(line, r)
			}
			col++
		}
		i += size
	}
	out.WriteString(string(line))
	return out.String()
}

// What an escape sequence erases of its line.
const (
	eraseNothing = iota
	eraseToEnd
	eraseToCursor
	eraseLine
)

// escape reads the escape sequence s starts with and answers its length and
// what it erases. A string sequence, a title or a link, ends at its
// terminator, at the next escape or at the end of its line, so one left open
// does not take the rest of the output with it.
func escape(s string) (int, int) {
	if len(s) < 2 {
		return len(s), eraseNothing
	}
	switch c := s[1]; {
	case c == '[':
		i := 2
		for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
			i++
		}
		params := s[2:i]
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i == len(s) || s[i] < 0x40 || s[i] > 0x7e {
			return i, eraseNothing
		}
		if s[i] == 'K' {
			switch params {
			case "", "0":
				return i + 1, eraseToEnd
			case "1":
				return i + 1, eraseToCursor
			case "2":
				return i + 1, eraseLine
			}
		}
		return i + 1, eraseNothing
	case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_':
		for i := 2; i < len(s); i++ {
			switch s[i] {
			case '\a':
				return i + 1, eraseNothing
			case '\x1b':
				if i+1 < len(s) && s[i+1] == '\\' {
					return i + 2, eraseNothing
				}
				return i, eraseNothing
			case '\n':
				return i, eraseNothing
			}
		}
		return len(s), eraseNothing
	case c >= 0x20 && c <= 0x2f:
		i := 2
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
			return i + 1, eraseNothing
		}
		return i, eraseNothing
	case c >= 0x30 && c <= 0x7e:
		return 2, eraseNothing
	}
	return 1, eraseNothing
}
