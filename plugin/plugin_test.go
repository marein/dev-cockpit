package plugin_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/marein/dev-cockpit/plugin"
)

var constantsNamed = regexp.MustCompile(`\bthe\s+(\w+)\s+constants\b`)

// godoc is what a plugin author reads, so a comment that sends them to
// "the X constants" names a prefix some exported constant still carries.
func TestDocCommentsNameConstantsThatExist(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "plugin.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var consts []string
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.CONST {
			for _, spec := range gen.Specs {
				for _, name := range spec.(*ast.ValueSpec).Names {
					if name.IsExported() {
						consts = append(consts, name.Name)
					}
				}
			}
		}
	}
	named := 0
	for _, group := range file.Comments {
		for _, m := range constantsNamed.FindAllStringSubmatch(group.Text(), -1) {
			named++
			found := false
			for _, c := range consts {
				found = found || strings.HasPrefix(c, m[1])
			}
			if !found {
				t.Errorf("%s: %q names no exported constant", fset.Position(group.Pos()), m[0])
			}
		}
	}
	if named == 0 {
		t.Fatal("no comment names constants, the check reads nothing")
	}
}

// snake spells a Go field name the way the callback socket names it:
// CoderID is coder_id, URL is url.
func snake(name string) string {
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(runes[i-1]) || i+1 < len(runes) && unicode.IsLower(runes[i+1])) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Every field of a struct an external plugin reads or sends on its callback
// socket carries the JSON name of its Go field, so the README's protocol and
// the Go surface read the same, and a field added later follows the rule.
func TestTheJSONNamesMirrorTheGoFields(t *testing.T) {
	for _, v := range append([]any{plugin.Event{}}, assistantStructs...) {
		typ := reflect.TypeOf(v)
		for i := range typ.NumField() {
			field := typ.Field(i)
			if field.Name == "_" {
				continue
			}
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if want := snake(field.Name); name != want {
				t.Errorf("%s.%s is sent as %q, want %q", typ.Name(), field.Name, name, want)
			}
		}
	}
}

var assistantStructs = []any{plugin.AssistantSpec{}, plugin.AssistantQuery{}, plugin.AssistantMessageQuery{}, plugin.AssistantList{}, plugin.Assistant{}, plugin.AssistantMessage{}, plugin.AssistantCoderQuery{}, plugin.AssistantCoderList{}, plugin.AssistantCoder{}}

// The structs of the assistant surface cannot be compared or built without
// field names, so a field of any type, a list too, joins them later without
// breaking a plugin that compiled against them before.
func TestTheAssistantStructsTakeAnyNewField(t *testing.T) {
	for _, v := range assistantStructs {
		typ := reflect.TypeOf(v)
		if typ.Comparable() {
			t.Errorf("%s can be compared with ==", typ.Name())
		}
		if first := typ.Field(0); first.Name != "_" || first.Type.Size() != 0 {
			t.Errorf("%s does not open with a blank field of no size, an unkeyed literal builds it", typ.Name())
		}
	}
}
