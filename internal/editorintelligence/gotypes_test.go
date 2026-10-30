package editorintelligence

import "testing"

// The lines are the ones gopls v0.23.0 answered workspace symbols on, each
// with the kind it filed the symbol under and the column it named.
func TestGoTypeOfTellsTypesFromWhatSharesTheirKind(t *testing.T) {
	cases := []struct {
		name, line  string
		kind, char  int
		want, label string
	}{
		{"Widget", "type Widget struct{ Name string }", symbolStruct, 5, "Widget", "struct"},
		{"example.com/x/inner.Box", "type Box struct{}", symbolStruct, 5, "Box", "struct"},
		{"inner.GroupedOne", "\tGroupedOne struct{}", symbolStruct, 1, "GroupedOne", "struct"},
		{"Kind", "type Kind string", symbolClass, 5, "Kind", "type"},
		{"Alias", "type Alias = Widget", symbolClass, 5, "Alias", "type"},
		{"Shape", "type Shape interface{ Area() float64 }", symbolInterface, 5, "Shape", "interface"},
		{"Number", "type Number interface{ ~int | ~float64 }", symbolInterface, 5, "Number", "interface"},
		{"Set", "type Set[T comparable] interface {", symbolInterface, 5, "Set", "interface"},
		{"Any", "type Any = interface{}", symbolInterface, 5, "Any", "interface"},
		{"Grouped", "\tGrouped interface{ M() }", symbolInterface, 1, "Grouped", "interface"},
		{"Handler", "type Handler func(int) error", symbolFunction, 5, "Handler", "type"},
		{"Pair", "type Pair[K comparable, V any] func(K) V", symbolFunction, 5, "Pair", "type"},
		{"Ünicode", "type Ünicode func()", symbolFunction, 5, "Ünicode", "type"},
		{"NewWidget", "func NewWidget() *Widget {", symbolFunction, 5, "", ""},
		{"Map", "func Map[T any](xs []T) []T {", symbolFunction, 5, "", ""},
		{"funcs", "func funcs() {}", symbolFunction, 5, "", ""},
		{"RW.Reader", "\tio.Reader", symbolInterface, 1, "", ""},
		{"RW.Local", "\tLocal", symbolInterface, 1, "", ""},
		{"RW.Base", "\tBase[int]", symbolInterface, 1, "", ""},
		{"Number.~int | ~float64", "type Number interface{ ~int | ~float64 }", symbolInterface, 23, "", ""},
		{"Widget.Name", "type Widget struct{ Name string }", 8, 20, "", ""},
		{"Widget.Area", "func (w Widget) Area() float64 {", 6, 16, "", ""},
		{"WidgetMax", "const WidgetMax = 3", 14, 6, "", ""},
		{"Shapes", "type Shapes interfaceish", symbolInterface, 5, "", ""},
	}
	for _, tc := range cases {
		read := func(n int) (string, bool) { return tc.line, n == 3 }
		at, ok := goTypeOf(Symbol{Name: tc.name, Kind: tc.kind, Line: 3, EndLine: 3, Character: tc.char}, read)
		if at.Name != tc.want || at.Kind != tc.label || ok != (tc.want != "") {
			t.Errorf("%s on %q: %+v %v, want %q %q", tc.name, tc.line, at, ok, tc.want, tc.label)
		}
		if ok && (at.Line != 3 || at.Character != tc.char) {
			t.Errorf("%s moved to %d:%d", tc.name, at.Line, at.Character)
		}
	}
}

// A struct or another named type needs no line, an interface or a func type
// does, and one that cannot be read is no type.
func TestGoTypeOfReadsTheLineOnlyWhereTheKindIsShared(t *testing.T) {
	unread := func(int) (string, bool) {
		t.Fatal("the line must not be read")
		return "", false
	}
	if _, ok := goTypeOf(Symbol{Name: "Widget", Kind: symbolStruct, Line: 1, Character: 5}, unread); !ok {
		t.Fatal("a struct")
	}
	if _, ok := goTypeOf(Symbol{Name: "Kind", Kind: symbolClass, Line: 1, Character: 5}, unread); !ok {
		t.Fatal("a named type")
	}
	gone := func(int) (string, bool) { return "", false }
	if _, ok := goTypeOf(Symbol{Name: "Shape", Kind: symbolInterface, Line: 1, Character: 5}, gone); ok {
		t.Fatal("an unreadable interface")
	}
}
