package editorintelligence

// pythonTypeOf reads pyright's workspace symbols, in which a class, a nested
// one and one declared in a function included, is the one kind of type: an
// alias, a type statement too, comes as a variable.
func pythonTypeOf(sym Symbol, _ func(n int) (string, bool)) (Declared, bool) {
	if sym.Kind != symbolClass {
		return Declared{}, false
	}
	return Declared{Name: sym.Name, Kind: "class", Line: sym.Line, Character: sym.Character}, true
}
