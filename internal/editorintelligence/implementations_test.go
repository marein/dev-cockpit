package editorintelligence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// implementationsRequest asks the usages of what stands on the given line of
// a Go file, a position the fake's type hierarchy answers by its line.
func implementationsRequest(t *testing.T, line int) Request {
	t.Helper()
	req := goRequest(t)
	req.Content = "package geo\n// Shapes.\nvar x = 1\ntype Shape interface{ Area() float64 }\ntype Square struct{}\nvar _ Shape = Square{}\nfunc f() {}\n"
	req.Line, req.Character = line, 6
	return req
}

func TestServiceReferencesListImplementationsApart(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)

	res, err := s.References(context.Background(), implementationsRequest(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("references: %+v", res)
	}
	// The usage on the line one implementation is declared on stands in
	// that group alone, and both of the outside ones are counted.
	wantImpls := []Location{
		{Path: "def.go", Line: 2, Character: 5},
		{Path: "impl.go", Line: 3, Character: 5},
	}
	if !reflect.DeepEqual(res.Implementations, wantImpls) {
		t.Fatalf("implementations %+v", res.Implementations)
	}
	wantUsages := []Location{{Path: "use.go", Line: 5, Character: 2}}
	if !reflect.DeepEqual(res.Locations, wantUsages) {
		t.Fatalf("usages %+v", res.Locations)
	}
	if res.Outside != 2 || res.Truncated || res.ImplementationsTruncated {
		t.Fatalf("answer %+v", res)
	}
}

func TestServiceReferencesWithoutImplementations(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)

	// A struct, an interface named but declared elsewhere, a position the
	// server refuses as no type, and one that names nothing: the usages
	// alone, whatever subtypes the server would answer.
	base := implementationsRequest(t, 0)
	for name, line := range map[string]int{"struct": 4, "usage of an interface": 5, "refused": 6, "no type": 1} {
		t.Run(name, func(t *testing.T) {
			req := base
			req.Line = line
			res, err := s.References(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Available || len(res.Implementations) != 0 || len(res.Locations) != 2 || res.Outside != 1 {
				t.Fatalf("references: %+v", res)
			}
		})
	}
}

func TestServiceDefinitionAsksNoImplementations(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)

	res, err := s.Definition(context.Background(), implementationsRequest(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Implementations) != 0 {
		t.Fatalf("definition: %+v", res)
	}
}

// implementationsTS is the TypeScript file the fake's implementation answer
// describes, see fakeImplementations.
const implementationsTS = `export interface Shape { area(): number }
export class Square implements Shape { area() { return 1; } }
export class Tile extends Square {}
export class Wide
  extends Square
  implements Shape {}
export const unit: Shape = { area: () => 0 };
let s: Shape;
`

var (
	tsShapeDirect = []Location{
		{Path: "src/shape.ts", Line: 2, Character: 13},
		{Path: "src/shape.ts", Line: 4, Character: 13},
	}
	tsShapeIndirect = []Location{
		{Path: "src/shape.ts", Line: 3, Character: 13},
		{Path: fakeTSLib, Line: 40, Character: 23, External: true},
	}
)

func tsImplementationsRequest(t *testing.T, line, character int) Request {
	t.Helper()
	req := goRequest(t)
	req.Path = "src/shape.ts"
	req.Content = implementationsTS
	req.Line, req.Character = line, character
	return req
}

func TestServiceReferencesListTSImplementationsApart(t *testing.T) {
	installFakeLSP(t, "normal", "tsgo")
	s := newTestService(t)

	res, err := s.References(context.Background(), tsImplementationsRequest(t, 0, 19))
	if err != nil {
		t.Fatal(err)
	}
	// The classes naming Shape in their heritage, Wide two lines below its
	// name; apart from them the one extending Square and the standard
	// library's class read out of the image, which name it nowhere. Not the
	// object literal, and not the answer outside the project, which is not
	// counted either.
	if !reflect.DeepEqual(res.Implementations, tsShapeDirect) || !reflect.DeepEqual(res.Indirect, tsShapeIndirect) || res.IndirectTruncated {
		t.Fatalf("implementations %+v", res)
	}
	// Wide names Shape two lines below its name, in its heritage, which is
	// its row's; the object literal's line stays a usage.
	wantUsages := []Location{
		{Path: "src/shape.ts", Line: 1, Character: 17},
		{Path: "src/shape.ts", Line: 7, Character: 19},
		{Path: "src/shape.ts", Line: 8, Character: 7},
	}
	if !reflect.DeepEqual(res.Locations, wantUsages) {
		t.Fatalf("usages %+v", res.Locations)
	}
	if len(res.Subclasses) != 0 || res.Outside != 0 || res.Truncated || res.ImplementationsTruncated {
		t.Fatalf("answer %+v", res)
	}
}

func TestServiceReferencesListTSSubclassesApart(t *testing.T) {
	installFakeLSP(t, "normal", "tsgo")
	s := newTestService(t)

	res, err := s.References(context.Background(), tsImplementationsRequest(t, 1, 15))
	if err != nil {
		t.Fatal(err)
	}
	// The class itself, which tsgo names among them, stays its declaration.
	wantSubs := []Location{
		{Path: "src/shape.ts", Line: 3, Character: 13},
		{Path: "src/shape.ts", Line: 4, Character: 13},
	}
	if !reflect.DeepEqual(res.Subclasses, wantSubs) {
		t.Fatalf("subclasses %+v", res.Subclasses)
	}
	if want := []Location{{Path: "src/shape.ts", Line: 2, Character: 13}}; !reflect.DeepEqual(res.Locations, want) {
		t.Fatalf("usages %+v", res.Locations)
	}
	if len(res.Implementations) != 0 {
		t.Fatalf("answer %+v", res)
	}
}

func TestServiceReferencesOffATSDeclaration(t *testing.T) {
	installFakeLSP(t, "normal", "tsgo")
	s := newTestService(t)

	// The fake answers Shape's implementations on these words too, so a
	// group here would be one taken for a usage of Shape, a value's name or
	// the keyword in front of a class.
	for name, pos := range map[string][2]int{"usage": {7, 8}, "keyword": {1, 9}, "value": {6, 15}} {
		t.Run(name, func(t *testing.T) {
			res, err := s.References(context.Background(), tsImplementationsRequest(t, pos[0], pos[1]))
			if err != nil {
				t.Fatal(err)
			}
			if !res.Available || len(res.Implementations) != 0 || len(res.Subclasses) != 0 || len(res.Locations) == 0 {
				t.Fatalf("references: %+v", res)
			}
		})
	}
}

// implementationsPHP and implementationsPHPCircle are the PHP files the
// fake answers off their text the way intelephense does: a type's
// references are every place its name stands, the alias an import renames
// it to a name of its own.
const implementationsPHP = `<?php
namespace App;

/**
 * Shapes.
 */
interface Shape {}
interface Solid extends Shape {}
abstract class Base implements Shape {}
final class Square extends Base implements
    Shape {}
enum Suit: string implements Shape {}
function f(Shape $s): Shape {
    return new class extends Base implements Shape {};
}
trait Painted {}
`

const implementationsPHPCircle = `<?php
namespace App\Round;

use App\Shape as Form;
use App\Base;

class Circle extends Base implements Form {}
$c = new Circle();
`

func phpImplementationsRequest(t *testing.T, path string, line, character int) Request {
	t.Helper()
	req := goRequest(t)
	for name, text := range map[string]string{"src/shape.php": implementationsPHP, "src/Round/circle.php": implementationsPHPCircle} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(req.ProjectRoot, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(req.ProjectRoot, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if name == path {
			req.Content = text
		}
	}
	req.Path, req.Line, req.Character = path, line, character
	return req
}

// The types below Shape: an interface extending it, the classes and the enum
// implementing it, one of them over two lines, an anonymous class, and one
// naming it through an import alias in another file.
var phpShapeBelow = []Location{
	{Path: "src/shape.php", Line: 8, Character: 10},
	{Path: "src/shape.php", Line: 9, Character: 15},
	{Path: "src/shape.php", Line: 10, Character: 12},
	{Path: "src/shape.php", Line: 12, Character: 5},
	{Path: "src/shape.php", Line: 14, Character: 15},
	{Path: "src/Round/circle.php", Line: 7, Character: 6},
}

var phpBaseBelow = []Location{
	{Path: "src/shape.php", Line: 10, Character: 12},
	{Path: "src/shape.php", Line: 14, Character: 15},
	{Path: "src/Round/circle.php", Line: 7, Character: 6},
}

// closeAll lets the request's own document go, as its tab closing does,
// after which the server holds no document: the lookup let go of every file
// it opened, and the next request in another file finds this one closed, as
// after a restart of the server.
func closeAll(t *testing.T, s *Service, req Request) {
	t.Helper()
	s.CloseDocument(req.Client, req.ProjectName, req.Path)
	mc := slotOf(s, req.ProjectName, req.Path)
	mc.conn.docMu.Lock()
	defer mc.conn.docMu.Unlock()
	if len(mc.conn.docClients) != 0 {
		t.Fatalf("documents left open: %v", mc.conn.docClients)
	}
}

func TestServiceReferencesListPHPImplementationsApart(t *testing.T) {
	installFakeLSP(t, "normal", "intelephense")
	s := newTestService(t)

	req := phpImplementationsRequest(t, "src/shape.php", 6, 10)
	res, err := s.References(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	closeAll(t, s, req)
	if !res.Available || res.Below != groupImplementations || !reflect.DeepEqual(res.Implementations, phpShapeBelow) {
		t.Fatalf("implementations %+v", res)
	}
	// The declaration, the type hints and the import stay usages; the
	// clauses, the one a line below Square's name too, are their types'.
	wantUsages := []Location{
		{Path: "src/shape.php", Line: 7, Character: 10},
		{Path: "src/shape.php", Line: 13, Character: 11},
		{Path: "src/shape.php", Line: 13, Character: 22},
		{Path: "src/Round/circle.php", Line: 4, Character: 8},
	}
	if !reflect.DeepEqual(res.Locations, wantUsages) {
		t.Fatalf("usages %+v", res.Locations)
	}
	if len(res.Subclasses) != 0 || res.Outside != 0 || res.Truncated || res.ImplementationsTruncated {
		t.Fatalf("answer %+v", res)
	}
}

func TestServiceReferencesListPHPSubclassesApart(t *testing.T) {
	installFakeLSP(t, "normal", "intelephense")
	s := newTestService(t)

	// An abstract class: the classes extending it, the anonymous one and
	// the one in another file among them.
	res, err := s.References(context.Background(), phpImplementationsRequest(t, "src/shape.php", 8, 16))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Below != groupSubclasses || !reflect.DeepEqual(res.Subclasses, phpBaseBelow) || len(res.Implementations) != 0 {
		t.Fatalf("subclasses %+v", res)
	}
	wantUsages := []Location{
		{Path: "src/shape.php", Line: 9, Character: 15},
		{Path: "src/Round/circle.php", Line: 5, Character: 8},
	}
	if !reflect.DeepEqual(res.Locations, wantUsages) {
		t.Fatalf("usages %+v", res.Locations)
	}
}

func TestServicePHPAliasAsksAreBounded(t *testing.T) {
	installFakeLSP(t, "normal", "intelephense")
	s := newTestService(t)

	// Two imports renaming Shape more than the asks reach, circle.php's one
	// among them: the classes behind the first ones are listed beside those
	// naming Shape itself, and the group says it was cut.
	req := phpImplementationsRequest(t, "src/shape.php", 6, 10)
	for n := range maxAliasAsks + 1 {
		text := fmt.Sprintf("<?php\nuse App\\Shape as F%d;\nclass C%d implements F%d {}\n", n, n, n)
		if err := os.WriteFile(filepath.Join(req.ProjectRoot, fmt.Sprintf("src/alias%02d.php", n)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.References(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if want := len(phpShapeBelow) - 1 + maxAliasAsks; !res.Available || len(res.Implementations) != want || !res.ImplementationsTruncated {
		t.Fatalf("implementations %d of %d, cut %v", len(res.Implementations), want, res.ImplementationsTruncated)
	}
}

func TestServiceReferencesOffAPHPDeclaration(t *testing.T) {
	installFakeLSP(t, "normal", "intelephense")
	s := newTestService(t)

	// A usage of the interface, an enum and a trait: the usages alone.
	for name, pos := range map[string][2]int{"usage": {12, 11}, "enum": {11, 5}, "trait": {15, 6}} {
		t.Run(name, func(t *testing.T) {
			res, err := s.References(context.Background(), phpImplementationsRequest(t, "src/shape.php", pos[0], pos[1]))
			if err != nil {
				t.Fatal(err)
			}
			if !res.Available || res.Below != "" || len(res.Implementations) != 0 || len(res.Subclasses) != 0 || len(res.Locations) == 0 {
				t.Fatalf("references: %+v", res)
			}
		})
	}
}

// A lookup holds a file the editors hold as they hold it, its text
// unchanged, and letting go leaves it open for them; a file nobody holds it
// opens from the disk and closes again.
func TestHoldDocumentKeepsAnEditorsText(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "shape.php"), []byte("<?php\ninterface Shape {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	php, _, _ := ProfileForPath("shape.php")
	sent := &methodsWriter{}
	conn := &lspConn{profile: php, rootPath: root, rootURI: fileURI(root), docVersion: map[string]int{}, docText: map[string]string{},
		docClients: map[string]map[string]bool{}, stdin: sent}

	if held, err := holdDocument(conn, lookupHolder+"1", root, php, "shape.php"); err != nil || !held || conn.docText["shape.php"] != "<?php\ninterface Shape {}\n" {
		t.Fatalf("from the disk: %v %v, %q", held, err, conn.docText["shape.php"])
	}
	conn.closeDocument(lookupHolder+"1", "shape.php")
	if want := []string{"textDocument/didOpen", "textDocument/didClose"}; !reflect.DeepEqual(sent.methods, want) {
		t.Fatalf("from the disk sent %v", sent.methods)
	}

	if err := conn.ensureDocument("client-a", "shape.php", "php", "<?php\ninterface Unsaved {}\n"); err != nil {
		t.Fatal(err)
	}
	sent.methods = nil
	if held, err := holdDocument(conn, lookupHolder+"2", root, php, "shape.php"); err != nil || !held || conn.docText["shape.php"] != "<?php\ninterface Unsaved {}\n" {
		t.Fatalf("an editor's: %v %v, %q", held, err, conn.docText["shape.php"])
	}
	conn.closeDocument(lookupHolder+"2", "shape.php")
	if !conn.docClients["shape.php"]["client-a"] || len(sent.methods) != 0 {
		t.Fatalf("the editor's document: %v, sent %v", conn.docClients, sent.methods)
	}

	for _, rel := range []string{"missing.php", "shape.ts"} {
		if held, err := holdDocument(conn, lookupHolder+"3", root, php, rel); err != nil || held {
			t.Fatalf("%s: %v %v", rel, held, err)
		}
	}
}

// methodsWriter records the methods of the frames a connection sends.
type methodsWriter struct{ methods []string }

func (w *methodsWriter) Write(p []byte) (int, error) {
	var msg rpcMessage
	if json.Unmarshal(p, &msg) == nil {
		w.methods = append(w.methods, msg.Method)
	}
	return len(p), nil
}

func (w *methodsWriter) Close() error { return nil }

// implementationsPython and implementationsPythonRound are the Python files
// the fake answers off their text the way pyright does: a class's
// references are every place its name stands, in any file of the
// workspace, the alias an import renames it to a name of its own.
const implementationsPython = `from abc import ABCMeta


class Shape:
    pass


class Square(Shape, metaclass=ABCMeta):
    pass


class Wide(
    Square,
):
    pass


def area(s: Shape) -> Shape:
    return s


class Loose:
    pass
`

const implementationsPythonRound = `from shapes import base
from shapes.base import (
    Shape as Form,
)


class Circle(Form):
    pass


class Ring(base.Shape):
    pass


c = Circle()
`

func pythonImplementationsRequest(t *testing.T, path string, line, character int) Request {
	t.Helper()
	req := goRequest(t)
	for name, text := range map[string]string{"src/shapes/base.py": implementationsPython, "src/shapes/round.py": implementationsPythonRound} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(req.ProjectRoot, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(req.ProjectRoot, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if name == path {
			req.Content = text
		}
	}
	req.Path, req.Line, req.Character = path, line, character
	return req
}

// The classes naming Shape among their bases: one beside a metaclass, one
// through an import alias over lines and one behind its module. Wide names
// Square and stands below Shape only through it.
var pythonShapeBelow = []Location{
	{Path: "src/shapes/base.py", Line: 8, Character: 6},
	{Path: "src/shapes/round.py", Line: 7, Character: 6},
	{Path: "src/shapes/round.py", Line: 11, Character: 6},
}

func TestServiceReferencesListPythonSubclassesApart(t *testing.T) {
	installFakeLSP(t, "lists-late", "pyright-langserver")
	s := newTestService(t)

	req := pythonImplementationsRequest(t, "src/shapes/base.py", 3, 6)
	res, err := s.References(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	closeAll(t, s, req)
	if !res.Available || res.Below != groupSubclasses || !reflect.DeepEqual(res.Subclasses, pythonShapeBelow) || len(res.Implementations) != 0 {
		t.Fatalf("subclasses %+v", res)
	}
	if want := []Location{{Path: "src/shapes/base.py", Line: 12, Character: 6}}; !reflect.DeepEqual(res.Indirect, want) || res.IndirectTruncated {
		t.Fatalf("indirect %+v", res)
	}
	// The declaration, the type hints and the import stay usages; the base
	// lists are their classes'.
	wantUsages := []Location{
		{Path: "src/shapes/base.py", Line: 4, Character: 6},
		{Path: "src/shapes/base.py", Line: 18, Character: 12},
		{Path: "src/shapes/base.py", Line: 18, Character: 22},
		{Path: "src/shapes/round.py", Line: 3, Character: 4},
	}
	if !reflect.DeepEqual(res.Locations, wantUsages) {
		t.Fatalf("usages %+v", res.Locations)
	}
	if res.Outside != 0 || res.Truncated || res.SubclassesTruncated {
		t.Fatalf("answer %+v", res)
	}

	// A base list over lines is one class's, and a class nothing extends
	// has the group without a row.
	res, err = s.References(context.Background(), pythonImplementationsRequest(t, "src/shapes/base.py", 7, 6))
	if err != nil {
		t.Fatal(err)
	}
	if want := []Location{{Path: "src/shapes/base.py", Line: 12, Character: 6}}; !res.Available || res.Below != groupSubclasses || !reflect.DeepEqual(res.Subclasses, want) {
		t.Fatalf("square %+v", res)
	}
	if want := []Location{{Path: "src/shapes/base.py", Line: 8, Character: 6}}; !reflect.DeepEqual(res.Locations, want) {
		t.Fatalf("square's usages %+v", res.Locations)
	}
	res, err = s.References(context.Background(), pythonImplementationsRequest(t, "src/shapes/base.py", 21, 6))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Below != groupSubclasses || len(res.Subclasses) != 0 || len(res.Locations) != 1 {
		t.Fatalf("loose %+v", res)
	}
}

func TestServiceReferencesOffAPythonDeclaration(t *testing.T) {
	installFakeLSP(t, "lists-late", "pyright-langserver")
	s := newTestService(t)

	// A usage of the class and a function: the usages alone.
	for name, pos := range map[string][2]int{"usage": {17, 12}, "function": {17, 4}} {
		t.Run(name, func(t *testing.T) {
			res, err := s.References(context.Background(), pythonImplementationsRequest(t, "src/shapes/base.py", pos[0], pos[1]))
			if err != nil {
				t.Fatal(err)
			}
			if !res.Available || res.Below != "" || len(res.Subclasses) != 0 || len(res.Locations) == 0 {
				t.Fatalf("references: %+v", res)
			}
		})
	}
}
