package editorintelligence

import "unicode/utf8"

// lineScan walks source on from a byte offset of a 1-based line, up to a
// last line, the way the tokens of a declaration's head are read. Each
// language passes its white space and comments its own way.
type lineScan struct {
	line func(n int) (string, bool)
	n    int
	text string
	i    int
	last int
}

// next moves to the start of the next line.
func (s *lineScan) next() bool {
	if s.n >= s.last {
		return false
	}
	text, ok := s.line(s.n + 1)
	if !ok {
		return false
	}
	s.n, s.text, s.i = s.n+1, text, 0
	return true
}

func (s *lineScan) peek() byte {
	if s.i < len(s.text) {
		return s.text[s.i]
	}
	return 0
}

// ident reads a label, a keyword's or a name's.
func (s *lineScan) ident() string {
	start := s.i
	for s.i < len(s.text) {
		r, size := utf8.DecodeRuneInString(s.text[s.i:])
		if r == '$' || !isIdentifierRune(r) {
			break
		}
		s.i += size
	}
	return s.text[start:s.i]
}
