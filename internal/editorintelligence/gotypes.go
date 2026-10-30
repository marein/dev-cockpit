package editorintelligence

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// goTypeOf reads gopls' workspace symbols. gopls files a declared type under
// the kind of its type expression: a struct under Struct, an interface under
// Interface, a func type under Function beside the functions, every other
// named type and every alias under Class. What an interface embeds is filed
// under Interface too, named after the interface ("Shape.Reader") and
// standing where the embedded name does. A name may carry the package it is
// qualified by when the query matched that ("inner.Box").
func goTypeOf(sym Symbol, line func(n int) (string, bool)) (Declared, bool) {
	at := Declared{Name: sym.Name[strings.LastIndexByte(sym.Name, '.')+1:], Line: sym.Line, Character: sym.Character}
	switch sym.Kind {
	case symbolStruct:
		at.Kind = "struct"
	case symbolClass:
		at.Kind = "type"
	case symbolInterface:
		if text, ok := line(sym.Line); ok && goDeclares(text, sym.Character, at.Name, "interface") {
			at.Kind = "interface"
		}
	case symbolFunction:
		if text, ok := line(sym.Line); ok && goDeclares(text, sym.Character, at.Name, "func") {
			at.Kind = "type"
		}
	}
	if at.Kind == "" {
		return Declared{}, false
	}
	return at, true
}

// goTypeAsks asks gopls twice. Its fuzzy matcher fills its limit of a
// hundred symbols with the project's own ones, loose matches included, which
// it ranks twice as high as a match outside the workspace, so a library's
// type hardly ever makes it in. In its exact syntax, a quote in front of
// every word, a symbol matches only when its name or package holds each word
// as it stands, which is what the quick open rule keeps, and the project's
// own matches still rank first: that request brings the dependencies' and
// the standard library's types.
func goTypeAsks(_, query string) []symbolAsk {
	words := strings.Fields(query)
	for i, word := range words {
		words[i] = "'" + word
	}
	return []symbolAsk{{query: query}, {query: strings.Join(words, " ")}}
}

// goDeclares reports whether the line declares name as a type whose
// expression starts with keyword: the name stands at the UTF-16 column,
// followed by its type parameters if it has any, an alias' equals sign if it
// is one, and the keyword. A function has its parameters there instead, an
// embedded interface nothing or an element of its own.
func goDeclares(line string, column int, name, keyword string) bool {
	rest, ok := strings.CutPrefix(line[UTF16Offset(line, column):], name)
	if !ok || startsIdentifier(rest) {
		return false
	}
	rest = strings.TrimLeft(rest, " \t")
	if strings.HasPrefix(rest, "[") {
		depth := 0
		end := strings.IndexFunc(rest, func(r rune) bool {
			switch r {
			case '[':
				depth++
			case ']':
				depth--
			}
			return depth == 0
		})
		if end < 0 {
			return false
		}
		rest = strings.TrimLeft(rest[end+1:], " \t")
	}
	if after, ok := strings.CutPrefix(rest, "="); ok {
		rest = strings.TrimLeft(after, " \t")
	}
	after, ok := strings.CutPrefix(rest, keyword)
	return ok && !startsIdentifier(after)
}

func startsIdentifier(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return isIdentifierRune(r)
}

func endsIdentifier(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return isIdentifierRune(r)
}

func isIdentifierRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
