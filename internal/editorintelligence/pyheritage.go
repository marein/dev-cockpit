package editorintelligence

import "strings"

// pythonClauses reads Python's base lists off pyright's references, which
// name a class wherever its name stands, among the bases of a class too,
// over lines and behind a module as in `module.Base`. A name an import
// renames is the one exception, its references are the alias's own.
var pythonClauses = &clauses{
	declares: pythonDeclares,
	names:    pythonBaseName,
	alias:    pythonAlias,
}

// pythonDeclares reads the group below the class a Python line declares at
// the UTF-16 column: the name right behind the keyword class, which starts
// its statement.
func pythonDeclares(line string, character int) string {
	start, ok := wordStart(line, character)
	if !ok || strings.Trim(line[:start], " \t") != "class" {
		return ""
	}
	return groupSubclasses
}

// pythonBaseName reads a reference standing among the bases of a class: the
// class they belong to, by its name. The statement is the nearest class
// upwards from the reference, read downwards to the colon its body follows.
// A name in a keyword argument, metaclass= among them, inside type
// arguments, a call or any other expression is no base.
func pythonBaseName(at Location, line func(n int) (string, bool)) (belowType, bool) {
	text, ok := line(at.Line)
	if !ok {
		return belowType{}, false
	}
	asked := UTF16Offset(text, at.Character)
	for n := at.Line; n >= max(1, at.Line-maxHeritageLines); n-- {
		row, ok := line(n)
		if !ok {
			return belowType{}, false
		}
		start := len(row) - len(strings.TrimLeft(row, " \t"))
		if !startsWord(row[start:], "class") {
			continue
		}
		head, ok := pythonReadHead(line, n, row, start)
		if !ok {
			return belowType{}, false
		}
		for _, base := range head.bases {
			if base.n == at.Line && base.from <= asked && asked < base.to {
				head.at.Path, head.at.External = at.Path, at.External
				return belowType{at: head.at, group: groupSubclasses}, true
			}
		}
		return belowType{}, false
	}
	return belowType{}, false
}

// pythonHead is the head of a class statement: where its name stands and
// the last part of the name each base is, on its line by its byte offsets.
type pythonHead struct {
	at    Location
	bases []pythonBase
}

type pythonBase struct {
	n        int
	from, to int
}

// pythonReadHead reads the head of a class statement from its keyword at the
// byte offset of line n on to the colon its body follows. False when what
// follows the keyword is no such head.
func pythonReadHead(line func(n int) (string, bool), n int, text string, start int) (pythonHead, bool) {
	s := pythonScan{lineScan{line: line, n: n, text: text, i: start, last: n + maxHeritageLines}}
	s.ident()
	if !s.skip() {
		return pythonHead{}, false
	}
	head := pythonHead{at: Location{Line: s.n, Character: utf16Len(s.text[:s.i])}}
	if s.ident() == "" || !s.skip() {
		return pythonHead{}, false
	}
	if s.peek() == '[' && (!s.group() || !s.skip()) {
		return pythonHead{}, false
	}
	switch s.peek() {
	case ':':
		return head, true
	case '(':
		s.i++
	default:
		return pythonHead{}, false
	}
	for s.skip() {
		if s.peek() == ')' {
			return head, true
		}
		if base, ok := s.base(); ok {
			head.bases = append(head.bases, base)
		}
		if !s.rest() {
			return pythonHead{}, false
		}
		if s.peek() == ',' {
			s.i++
		}
	}
	return pythonHead{}, false
}

// pythonAlias reads an import at a reference that renames the class, `from
// module import Base as Other`, in parentheses over lines too, and answers
// where the new name stands.
func pythonAlias(at Location, line func(n int) (string, bool)) (Location, bool) {
	text, ok := line(at.Line)
	if !ok || !pythonImported(at, line) {
		return Location{}, false
	}
	s := pythonScan{lineScan{line: line, n: at.Line, text: text, i: UTF16Offset(text, at.Character), last: at.Line + maxHeritageLines}}
	if s.ident() == "" || !s.skip() || s.ident() != "as" || !s.skip() {
		return Location{}, false
	}
	alias := Location{Path: at.Path, External: at.External, Line: s.n, Character: utf16Len(s.text[:s.i])}
	if s.ident() == "" {
		return Location{}, false
	}
	return alias, true
}

// pythonImported reports whether a reference stands among the names a from
// import brings in, on the line of its keyword or below it, inside the
// parentheses the import opens or on lines a backslash joins. An except or
// a with clause renames too and is no import.
func pythonImported(at Location, line func(n int) (string, bool)) bool {
	joined := true
	for n := at.Line; n >= max(1, at.Line-maxHeritageLines); n-- {
		row, ok := line(n)
		if !ok {
			return false
		}
		if n == at.Line {
			row = row[:UTF16Offset(row, at.Character)]
		} else {
			joined = joined && strings.HasSuffix(strings.TrimRight(row, " \t\r"), `\`)
		}
		if strings.Contains(row, ")") {
			return false
		}
		code := strings.TrimLeft(row, " \t")
		if !startsWord(code, "from") {
			continue
		}
		names, ok := afterWord(code, "import")
		return ok && (n == at.Line || joined || strings.HasPrefix(strings.TrimLeft(names, " \t"), "("))
	}
	return false
}

// startsWord reports whether the text starts with the word, whole.
func startsWord(text, word string) bool {
	return strings.HasPrefix(text, word) && !startsIdentifier(text[len(word):])
}

// afterWord answers what follows the first whole word in the text.
func afterWord(text, word string) (string, bool) {
	for i := 0; i+len(word) <= len(text); i++ {
		if text[i:i+len(word)] == word && !endsIdentifier(text[:i]) && !startsIdentifier(text[i+len(word):]) {
			return text[i+len(word):], true
		}
	}
	return "", false
}

// pythonScan walks Python source the way the tokens of a class's head are
// read.
type pythonScan struct{ lineScan }

// skip passes white space, line ends, the backslash joining two lines and
// comments, and reports whether a token follows within the lines it may
// read.
func (s *pythonScan) skip() bool {
	for {
		for s.i < len(s.text) && (s.text[s.i] == ' ' || s.text[s.i] == '\t' || s.text[s.i] == '\r' || s.text[s.i] == '\f') {
			s.i++
		}
		rest := s.text[s.i:]
		if rest != "" && rest[0] != '#' && strings.TrimRight(rest, " \t\r") != `\` {
			return true
		}
		if !s.next() {
			return false
		}
	}
}

// base reads a base at the start of an argument: a name, a dotted one too,
// with type arguments or none, and answers where the last part of the name
// stands. An argument that holds anything else, the value of a keyword, an
// unpacked or a called one, is none, and the scan stands where that showed.
func (s *pythonScan) base() (pythonBase, bool) {
	var base pythonBase
	for {
		from := s.i
		if s.ident() == "" {
			return pythonBase{}, false
		}
		base = pythonBase{n: s.n, from: from, to: s.i}
		if !s.skip() {
			return pythonBase{}, false
		}
		if s.peek() != '.' {
			break
		}
		s.i++
		if !s.skip() {
			return pythonBase{}, false
		}
	}
	if s.peek() == '[' && (!s.group() || !s.skip()) {
		return pythonBase{}, false
	}
	return base, s.peek() == ',' || s.peek() == ')'
}

// rest passes what is left of an argument up to the comma or the
// parenthesis ending it.
func (s *pythonScan) rest() bool {
	for s.skip() {
		switch c := s.peek(); c {
		case ',', ')':
			return true
		case '(', '[', '{':
			if !s.group() {
				return false
			}
		case ']', '}':
			return false
		case '\'', '"':
			if !s.str() {
				return false
			}
		default:
			s.i++
		}
	}
	return false
}

// group passes a bracketed part from its opening bracket to the one closing
// it, with the brackets in its strings and comments left aside.
func (s *pythonScan) group() bool {
	depth := 0
	for s.skip() {
		switch c := s.peek(); c {
		case '(', '[', '{':
			depth++
			s.i++
		case ')', ']', '}':
			depth--
			s.i++
			if depth == 0 {
				return true
			}
		case '\'', '"':
			if !s.str() {
				return false
			}
		default:
			s.i++
		}
	}
	return false
}

// str passes a string from its opening quote, a triple quoted one over
// lines too.
func (s *pythonScan) str() bool {
	quote := s.text[s.i : s.i+1]
	if strings.HasPrefix(s.text[s.i:], strings.Repeat(quote, 3)) {
		quote = strings.Repeat(quote, 3)
	}
	s.i += len(quote)
	for {
		for s.i < len(s.text) {
			switch {
			case s.text[s.i] == '\\':
				s.i = min(s.i+2, len(s.text))
			case strings.HasPrefix(s.text[s.i:], quote):
				s.i += len(quote)
				return true
			default:
				s.i++
			}
		}
		if len(quote) == 1 || !s.next() {
			return false
		}
	}
}
