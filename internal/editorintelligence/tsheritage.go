package editorintelligence

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// maxHeritageLines bounds how far below a declaration's own line its body
// is looked for; a TypeScript declaration whose brace does not come within
// it takes its own line alone, a PHP clause name or a Python base further
// below is no clause's.
const maxHeritageLines = 32

// tsDeclares reads the group below the type a TypeScript or JavaScript line
// declares at the UTF-16 column: the name right behind the keyword interface
// or class, whatever modifiers stand before it.
func tsDeclares(line string, character int) string {
	start, ok := wordStart(line, character)
	if !ok {
		return ""
	}
	before := strings.TrimRight(line[:start], " \t")
	switch {
	case endsWithKeyword(before, "interface"):
		return groupImplementations
	case endsWithKeyword(before, "class"):
		return groupSubclasses
	}
	return ""
}

// wordStart answers the byte offset the word at the UTF-16 column of a line
// starts at, false when no word stands there.
func wordStart(line string, character int) (int, bool) {
	start := UTF16Offset(line, character)
	for start > 0 && endsIdentifier(line[:start]) {
		_, size := utf8.DecodeLastRuneInString(line[:start])
		start -= size
	}
	return start, startsIdentifier(line[start:])
}

// tsHeritage reads one location of tsgo's implementation answer. A class or
// an interface it names by its name, a class expression and an anonymous
// default class by the start of the expression, modifiers included. Whatever
// else it names, an object literal, a function or a variable typed with the
// type, a type alias naming it, starts otherwise. For a class or an interface
// it answers the head up to the line its body opens on.
func tsHeritage(at Location, line func(n int) (string, bool)) (heritageHead, bool) {
	text, ok := line(at.Line)
	if !ok {
		return heritageHead{}, false
	}
	offset := UTF16Offset(text, at.Character)
	rest := text[offset:]
	if !endsWithKeyword(strings.TrimRight(text[:offset], " \t"), "class", "interface") {
		if rest, ok = tsClassExpression(rest); !ok {
			return heritageHead{}, false
		}
	}
	head := tsReadHead(at.Line, text, len(text)-len(rest), line)
	for i := range head.names {
		head.names[i].Path, head.names[i].External = at.Path, at.External
	}
	return head, true
}

// tsClassExpression answers what follows the keyword class at the start of
// text, behind the modifiers an anonymous default class carries.
func tsClassExpression(text string) (string, bool) {
	for {
		end := strings.IndexFunc(text, func(r rune) bool { return !isIdentifierRune(r) })
		if end < 0 {
			end = len(text)
		}
		switch text[:end] {
		case "export", "default", "abstract", "declare":
			text = strings.TrimLeft(text[end:], " \t")
		case "class":
			return text[end:], true
		default:
			return "", false
		}
	}
}

// tsReadHead reads a declaration's head from a byte offset of line n,
// behind its name or keyword, to the line its body opens on: the first
// brace outside the brackets, type arguments, strings and comments its
// heritage holds. The names on the way that stand outside them are those
// of the types it extends or implements, a type argument's and a
// constraint's are not. A head whose brace does not come within
// maxHeritageLines is its own line alone.
func tsReadHead(n int, text string, start int, line func(n int) (string, bool)) heritageHead {
	head := heritageHead{last: n}
	depth, comment := 0, false
	for at := n; at <= n+maxHeritageLines; at++ {
		i := start
		if at > n {
			var ok bool
			if text, ok = line(at); !ok {
				break
			}
			i = 0
		}
		quote := byte(0)
		for ; i < len(text); i++ {
			c := text[i]
			next := byte(0)
			if i+1 < len(text) {
				next = text[i+1]
			}
			switch {
			case comment:
				if c == '*' && next == '/' {
					comment = false
					i++
				}
			case quote != 0:
				if c == '\\' {
					i++
				} else if c == quote {
					quote = 0
				}
			case c == '/' && next == '/':
				i = len(text)
			case c == '/' && next == '*':
				comment = true
				i++
			case c == '\'' || c == '"' || c == '`':
				quote = c
			case c == '{' && depth == 0:
				head.last = at
				return head
			case c == '(' || c == '[' || c == '{' || c == '<':
				depth++
			case c == ')' || c == ']' || c == '}' || c == '>' && (i == 0 || text[i-1] != '='):
				depth = max(depth-1, 0)
			case depth == 0 && !endsIdentifier(text[:i]) && startsIdentifier(text[i:]):
				head.names = append(head.names, Location{Line: at, Character: utf16Len(text[:i])})
			}
		}
	}
	head.names = slices.DeleteFunc(head.names, func(name Location) bool { return name.Line != n })
	return head
}
