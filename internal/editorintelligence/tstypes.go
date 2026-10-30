package editorintelligence

import "strings"

// tsTypeOf reads tsgo's workspace symbols. An interface comes under
// Interface and an enum, a const one too, under Enum, while a class and a
// type alias both come under Class: the keyword in front of the name, where
// the range starts, tells them apart. A namespace, a module and a variable
// holding a class are no types.
func tsTypeOf(sym Symbol, line func(n int) (string, bool)) (Declared, bool) {
	at := Declared{Name: sym.Name, Line: sym.Line, Character: sym.Character}
	switch sym.Kind {
	case symbolInterface:
		at.Kind = "interface"
	case symbolEnum:
		at.Kind = "enum"
	case symbolClass:
		at.Kind = "class"
		if text, ok := line(sym.Line); ok && endsWithKeyword(strings.TrimRight(text[:UTF16Offset(text, sym.Character)], " \t"), "type") {
			at.Kind = "type"
		}
	default:
		return Declared{}, false
	}
	return at, true
}

// tsTypeAsks asks tsgo twice, each request behind the setting it needs.
// tsgo names no symbol of a declaration file by default, neither of
// node_modules nor of its own standard library, and switched on those share
// its limit with the project's own, which a short query then fills with
// library symbols. So the project's own types come with the switch off and
// the libraries' with it on, which tsgo names only in a project with TS
// files, see Profile.LibraryFiles. The setting is all the configuration it
// is sent, every other one keeps the default the cockpit leaves it at.
func tsTypeAsks(_, query string) []symbolAsk {
	return []symbolAsk{
		{query: query, settings: tsLibrarySymbols(false)},
		{query: query, settings: tsLibrarySymbols(true), library: true},
	}
}

func tsLibrarySymbols(include bool) map[string]any {
	return map[string]any{"js/ts": map[string]any{
		"workspaceSymbols": map[string]any{"excludeLibrarySymbols": !include},
	}}
}
