package editorintelligence

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTypesGo and fakeInnerGo are the project the symbol tests write, and
// fakeSymbols answers workspace/symbol over them the way gopls does: every
// symbol of the project whatever the query, the way a fuzzy matcher hands
// over more than matches, and those of the standard library, the image's
// fakeImageFiles, only to the exact syntax, its kinds, names and positions
// recorded from gopls v0.23.0.
const fakeTypesGo = `package shapes

import "io"

type Widget struct{ Name string }

type Shape interface{ Area() float64 }

type Kind string

type Handler func(int) error

type Alias = Widget

func NewWidget() *Widget { return &Widget{} }

type RW interface {
	io.Reader
	Local
}

type Local interface{ Close() error }
`

const fakeInnerGo = "package inner\n\ntype Box struct{}\n"

func fakeSymbols(rootURI, query string, indexed, crowded bool) []map[string]any {
	sym := func(name string, kind int, container, uri string, line, char int) map[string]any {
		return map[string]any{
			"name": name, "kind": kind, "containerName": container,
			"location": map[string]any{"uri": uri, "range": map[string]any{
				"start": map[string]any{"line": line, "character": char},
				"end":   map[string]any{"line": line, "character": char + len(name)},
			}},
		}
	}
	types, inner := rootURI+"/types.go", rootURI+"/inner/inner.go"
	all := []map[string]any{
		sym("Widget", 23, "example.com/x", types, 4, 5),
		sym("Widget.Name", 8, "example.com/x", types, 4, 19),
		sym("Shape", 11, "example.com/x", types, 6, 5),
		sym("Kind", 5, "example.com/x", types, 8, 5),
		sym("Handler", 12, "example.com/x", types, 10, 5),
		sym("Alias", 5, "example.com/x", types, 12, 5),
		sym("NewWidget", 12, "example.com/x", types, 14, 5),
		sym("RW", 11, "example.com/x", types, 16, 5),
		sym("RW.Reader", 11, "example.com/x", types, 17, 1),
		sym("RW.Local", 11, "example.com/x", types, 18, 1),
		sym("Local", 11, "example.com/x", types, 21, 5),
		sym("example.com/x/inner.Box", 23, "example.com/x/inner", inner, 2, 5),
	}
	std := []map[string]any{
		sym("Reader", 11, "io", "file:///usr/local/go/src/io/io.go", 85, 5),
		sym("HandlerFunc", 12, "net/http", "file:///usr/local/go/src/net/http/server.go", 2281, 5),
		sym("NewReader", 12, "bytes", "file:///usr/local/go/src/bytes/reader.go", 159, 5),
	}
	exact := strings.HasPrefix(query, "'")
	switch query = strings.TrimPrefix(query, "'"); {
	case query == "none":
		return nil
	case query == "widg":
		many := make([]map[string]any, 0, 100)
		for range 100 {
			many = append(many, all[0])
		}
		return many
	case !indexed:
		return all[:1]
	case exact && crowded:
		many := make([]map[string]any, 0, 100)
		for i := range 100 {
			many = append(many, sym(fmt.Sprintf("Area%03d", i), 23, "image", "file:///usr/local/go/src/image/geom.go", i, 5))
		}
		return many
	case exact:
		return append(all, std...)
	}
	return all
}

func writeTypesProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"types.go": fakeTypesGo, "inner/inner.go": fakeInnerGo} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func typesRequest(root, query string) TypesRequest {
	return TypesRequest{
		Client:      "client-a",
		ProjectName: "proj",
		ProjectRoot: root,
		Query:       query,
		Profiles:    []WarmMode{{ProfileID: "go"}},
	}
}

func typeRows(res TypesResult) string {
	rows := make([]string, 0, len(res.Types))
	for _, t := range res.Types {
		row := fmt.Sprintf("%s %s %s %s:%d:%d", t.Kind, t.Name, t.Container, t.Path, t.Line, t.Character)
		if t.Library {
			row += " library"
		}
		if t.External {
			row += " external"
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

// The project's own types come back with their kind, name, package and
// declaration, ranked by the quick open rule, and the standard library's
// behind them, read off the lines of the image's files, opening read only
// like a definition there. A function, a field and what an interface embeds
// stay out, and so does whatever the server matched fuzzily that the rule
// does not.
func TestServiceTypesListsTheProjectsOwnTypes(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)
	root := writeTypesProject(t)

	res := s.Types(context.Background(), typesRequest(root, "a"))
	want := strings.Join([]string{
		"type Alias example.com/x types.go:13:5",
		"type Handler example.com/x types.go:11:5",
		"interface Shape example.com/x types.go:7:5",
		"interface Local example.com/x types.go:22:5",
		"interface RW example.com/x types.go:17:5",
		"struct Box example.com/x/inner inner/inner.go:3:5",
		"type Kind example.com/x types.go:9:5",
		"struct Widget example.com/x types.go:5:5",
		"type HandlerFunc net/http /usr/local/go/src/net/http/server.go:2282:5 library external",
		"interface Reader io /usr/local/go/src/io/io.go:86:5 library external",
	}, "\n")
	if got := typeRows(res); got != want {
		t.Fatalf("types:\n%s\nwant:\n%s", got, want)
	}
	if res.Truncated || len(res.Statuses) != 0 {
		t.Fatalf("a whole answer: %+v", res)
	}
	if res.Types[0].Language != "Go" {
		t.Fatalf("language %q", res.Types[0].Language)
	}

	for _, query := range []string{"inner box", "inner.Box"} {
		res = s.Types(context.Background(), typesRequest(root, query))
		if got := typeRows(res); got != "struct Box example.com/x/inner inner/inner.go:3:5" {
			t.Fatalf("%s: the package words narrow: %s", query, got)
		}
	}
	res = s.Types(context.Background(), typesRequest(root, "wid"))
	if got := typeRows(res); got != "struct Widget example.com/x types.go:5:5" {
		t.Fatalf("a token the server matched fuzzily stays out: %s", got)
	}
}

// A server that cut its answer at its limit says that more types may match,
// and an empty answer is an empty list, not an error.
func TestServiceTypesSaysWhenTheServerCutItsAnswer(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)
	root := writeTypesProject(t)

	res := s.Types(context.Background(), typesRequest(root, "widg"))
	if !res.Truncated || len(res.Types) != 1 {
		t.Fatalf("cut answer: %+v", res)
	}
	res = s.Types(context.Background(), typesRequest(root, "none"))
	if res.Truncated || len(res.Types) != 0 || len(res.Statuses) != 0 || res.Types == nil {
		t.Fatalf("empty answer: %+v", res)
	}
}

// A search while the server indexes waits for the index like a lookup, so
// the list is whole once it comes and never the partial one.
func TestServiceTypesWaitsOutTheIndexing(t *testing.T) {
	installFakeLSP(t, "indexing", "gopls")
	s := newTestService(t)
	root := writeTypesProject(t)

	res := s.Types(context.Background(), typesRequest(root, "a"))
	if len(res.Types) != 10 {
		t.Fatalf("the answer after the index: %s", typeRows(res))
	}
}

// gopls' limit filled with the standard library's types takes none of the
// project's own, which come from the fuzzy request and stand first.
func TestServiceTypesKeepsTheOwnTypesOfACrowdedGoAnswer(t *testing.T) {
	installFakeLSP(t, "crowded", "gopls")
	s := newTestService(t)
	root := writeTypesProject(t)

	res := s.Types(context.Background(), typesRequest(root, "a"))
	if len(res.Types) != maxTypes || !res.Truncated {
		t.Fatalf("a cut list of %d: %+v", len(res.Types), res.Truncated)
	}
	own := strings.Split(typeRows(TypesResult{Types: res.Types[:8]}), "\n")
	for i, row := range own {
		if strings.HasSuffix(row, "library external") {
			t.Fatalf("row %d is a library's: %s", i, row)
		}
	}
	if got := typeRows(TypesResult{Types: res.Types[8:9]}); got != "struct Area000 image /usr/local/go/src/image/geom.go:1:5 library external" {
		t.Fatalf("the libraries' first: %s", got)
	}
}

// A language that cannot answer travels as its status beside the others.
func TestServiceTypesNamesALanguageThatCannotAnswer(t *testing.T) {
	s := newTestService(t)
	t.Setenv("PATH", t.TempDir())
	res := s.Types(context.Background(), typesRequest(writeTypesProject(t), "a"))
	if len(res.Types) != 0 || len(res.Statuses) != 1 || res.Statuses[0] != (LanguageStatus{Label: "Go", Status: StatusNotInstalled}) {
		t.Fatalf("statuses: %+v", res)
	}

	var none *Service
	res = none.Types(context.Background(), typesRequest(t.TempDir(), "a"))
	if len(res.Statuses) != 1 || res.Statuses[0].Status != StatusUnavailable || res.Types == nil {
		t.Fatalf("no service: %+v", res)
	}
}
