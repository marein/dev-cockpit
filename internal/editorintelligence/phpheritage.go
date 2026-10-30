package editorintelligence

import "strings"

// phpClauses reads PHP's heritage clauses off intelephense's references,
// which name a type wherever its name stands, in an extends or an
// implements clause too, however it is written there: imported, fully
// qualified, behind an aliased namespace or relative to the current one. A
// name an import renames is the one exception, its references are the
// alias's own.
var phpClauses = &clauses{
	declares: tsDeclares,
	names:    phpClauseName,
	alias:    phpAlias,
}

// phpClauseName reads a reference standing in the heritage clauses of a
// type: the type they belong to, by its name or, for an anonymous class, by
// its keyword, and the group the clause puts it in below the named type. A
// class or an enum implements an interface, an interface extends one, a
// class extends a class. The head is looked for upwards from the reference,
// the nearest keyword first, and read downwards to its body.
func phpClauseName(at Location, line func(n int) (string, bool)) (belowType, bool) {
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
		end := len(row)
		if n == at.Line {
			end = asked
		}
		starts := phpKeywords(row[:end])
		for k := len(starts) - 1; k >= 0; k-- {
			head, ok := phpReadHead(line, n, row, starts[k])
			if !ok {
				continue
			}
			for _, name := range head.names {
				if name.n == at.Line && name.from <= asked && asked < name.to {
					head.at.Path, head.at.External = at.Path, at.External
					return belowType{at: head.at, group: phpClauseGroup(head.keyword, name.clause)}, true
				}
			}
		}
	}
	return belowType{}, false
}

// phpClauseGroup answers the group a clause of a declaration with the
// keyword puts it in below the type the clause names.
func phpClauseGroup(keyword, clause string) string {
	switch {
	case keyword == "interface" && clause == "extends", keyword != "interface" && clause == "implements":
		return groupImplementations
	case keyword == "class" && clause == "extends":
		return groupSubclasses
	}
	return ""
}

// phpHead is the head of a type declaration: its keyword, where its name
// stands, or the keyword of an anonymous class, and the names its clauses
// hold, each on its line by its byte offsets.
type phpHead struct {
	keyword string
	at      Location
	names   []phpClauseRef
}

type phpClauseRef struct {
	clause   string
	n        int
	from, to int
}

// phpKeywords answers where the keywords of a type declaration with clauses
// stand in a line, in any case as PHP takes them, each a whole word and none
// a constant, a property or a variable named like one.
func phpKeywords(text string) []int {
	var starts []int
	for i := range len(text) {
		for _, keyword := range []string{"class", "interface", "enum"} {
			end := i + len(keyword)
			if end > len(text) || !strings.EqualFold(text[i:end], keyword) {
				continue
			}
			before := text[:i]
			if !endsIdentifier(before) && !strings.HasSuffix(before, `\`) && !strings.HasSuffix(before, ":") && !strings.HasSuffix(before, ">") && !startsIdentifier(text[end:]) {
				starts = append(starts, i)
			}
		}
	}
	return starts
}

// phpReadHead reads the head of a declaration from its keyword at the byte
// offset of line n on to the brace its body opens with. False when what
// follows the keyword is no such head.
func phpReadHead(line func(n int) (string, bool), n int, text string, start int) (phpHead, bool) {
	s := phpScan{lineScan{line: line, n: n, text: text, i: start, last: n + maxHeritageLines}}
	head := phpHead{keyword: strings.ToLower(s.ident()), at: Location{Line: n, Character: utf16Len(text[:start])}}
	if !s.skip() {
		return phpHead{}, false
	}
	switch {
	case head.keyword == "class" && s.peek() == '(':
		if !s.balanced('(', ')') {
			return phpHead{}, false
		}
	case head.keyword == "class" && (s.peek() == '{' || s.atWord("extends") || s.atWord("implements")):
	default:
		at := Location{Line: s.n, Character: utf16Len(s.text[:s.i])}
		if name := strings.ToLower(s.ident()); name == "" || name == "extends" || name == "implements" {
			return phpHead{}, false
		}
		head.at = at
	}
	for s.skip() {
		switch {
		case s.peek() == '{':
			return head, true
		case head.keyword == "enum" && s.peek() == ':':
			s.i++
			if !s.skip() || s.ident() == "" {
				return phpHead{}, false
			}
			continue
		}
		clause := strings.ToLower(s.ident())
		if clause != "extends" && clause != "implements" {
			return phpHead{}, false
		}
		for {
			if !s.skip() {
				return phpHead{}, false
			}
			from := s.i
			if s.name() == "" {
				return phpHead{}, false
			}
			head.names = append(head.names, phpClauseRef{clause: clause, n: s.n, from: from, to: s.i})
			if !s.skip() || s.peek() != ',' {
				break
			}
			s.i++
		}
	}
	return phpHead{}, false
}

// phpAlias reads an import at a reference that renames the type, `use Foo
// as Bar` alone or in a group, and answers where the new name stands.
func phpAlias(at Location, line func(n int) (string, bool)) (Location, bool) {
	text, ok := line(at.Line)
	if !ok {
		return Location{}, false
	}
	s := phpScan{lineScan{line: line, n: at.Line, text: text, i: UTF16Offset(text, at.Character), last: at.Line + maxHeritageLines}}
	if s.name() == "" || !s.skip() || !strings.EqualFold(s.ident(), "as") || !s.skip() {
		return Location{}, false
	}
	alias := Location{Path: at.Path, External: at.External, Line: s.n, Character: utf16Len(s.text[:s.i])}
	if s.ident() == "" {
		return Location{}, false
	}
	return alias, true
}

// phpScan walks PHP source the way the tokens of a declaration's head are
// read.
type phpScan struct{ lineScan }

// skip passes white space, line ends and comments, and reports whether a
// token follows within the lines it may read. An attribute starts like a
// comment of the hash kind and is none.
func (s *phpScan) skip() bool {
	for {
		for s.i < len(s.text) && (s.text[s.i] == ' ' || s.text[s.i] == '\t' || s.text[s.i] == '\r') {
			s.i++
		}
		rest := s.text[s.i:]
		switch {
		case rest == "" || strings.HasPrefix(rest, "//") || strings.HasPrefix(rest, "#") && !strings.HasPrefix(rest, "#["):
			if !s.next() {
				return false
			}
		case strings.HasPrefix(rest, "/*"):
			s.i += 2
			for {
				if end := strings.Index(s.text[s.i:], "*/"); end >= 0 {
					s.i += end + 2
					break
				}
				if !s.next() {
					return false
				}
			}
		default:
			return true
		}
	}
}

// name reads a name the way a clause holds it, qualified and fully
// qualified ones whole.
func (s *phpScan) name() string {
	start := s.i
	for {
		if s.peek() == '\\' {
			s.i++
		}
		if s.ident() == "" || s.peek() != '\\' {
			break
		}
	}
	return s.text[start:s.i]
}

// atWord reports whether the keyword stands next, as a whole word.
func (s *phpScan) atWord(keyword string) bool {
	rest := s.text[s.i:]
	return len(rest) >= len(keyword) && strings.EqualFold(rest[:len(keyword)], keyword) && !startsIdentifier(rest[len(keyword):])
}

// balanced passes a bracketed part from its opening bracket to the one
// closing it, the brackets in its strings left aside.
func (s *phpScan) balanced(open, close byte) bool {
	depth, quote := 0, byte(0)
	for {
		for ; s.i < len(s.text); s.i++ {
			c := s.text[s.i]
			switch {
			case quote != 0:
				if c == '\\' {
					s.i++
				} else if c == quote {
					quote = 0
				}
			case c == '\'' || c == '"':
				quote = c
			case c == open:
				depth++
			case c == close:
				depth--
				if depth == 0 {
					s.i++
					return true
				}
			}
		}
		if !s.next() {
			return false
		}
	}
}
