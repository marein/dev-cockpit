package editorintelligence

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// walkPHP and walkPHPDeck are PHP files whose types stand below Shape and
// Square through others: an interface extending one that extends Shape, the
// classes and the enum implementing those, subclasses of subclasses, one of
// them through an import alias in another file, a class naming Shape and
// a class below it at once, and two classes extending each other.
const walkPHP = `<?php
namespace App;

interface Shape {}
interface Solid extends Shape {}
interface Prism extends Solid {}
class Square implements Shape {}
class Tile extends Square {}
final class Floor extends Tile {}
class Cube implements Prism {}
enum Kind implements Solid {}
class Both extends Square implements Shape {}
class Knot extends Loop {}
class Loop extends Knot {}
`

const walkPHPDeck = `<?php
namespace App\Sub;

use App\Tile as Plate;

class Deck extends Plate {}
`

func walkRequest(t *testing.T, files map[string]string, path string, line, character int) Request {
	t.Helper()
	req := goRequest(t)
	for name, text := range files {
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

func phpWalkRequest(t *testing.T, line, character int) Request {
	t.Helper()
	return walkRequest(t, map[string]string{"src/walk.php": walkPHP, "src/Sub/deck.php": walkPHPDeck}, "src/walk.php", line, character)
}

func TestServiceWalksPHPToTheIndirectTypes(t *testing.T) {
	installFakeLSP(t, "normal", "intelephense")
	s := newTestService(t)

	// Below Shape the types naming it; below them the ones naming those,
	// each once, Both among the direct ones alone.
	wantDirect := []Location{
		{Path: "src/walk.php", Line: 5, Character: 10},
		{Path: "src/walk.php", Line: 7, Character: 6},
		{Path: "src/walk.php", Line: 12, Character: 6},
	}
	wantIndirect := []Location{
		{Path: "src/walk.php", Line: 6, Character: 10},
		{Path: "src/walk.php", Line: 8, Character: 6},
		{Path: "src/walk.php", Line: 9, Character: 12},
		{Path: "src/walk.php", Line: 10, Character: 6},
		{Path: "src/walk.php", Line: 11, Character: 5},
		{Path: "src/Sub/deck.php", Line: 6, Character: 6},
	}
	req := phpWalkRequest(t, 3, 12)
	res, err := s.References(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	closeAll(t, s, req)
	if !res.Available || res.Below != groupImplementations || !reflect.DeepEqual(res.Implementations, wantDirect) {
		t.Fatalf("direct %+v", res)
	}
	if !reflect.DeepEqual(res.Indirect, wantIndirect) || res.IndirectTruncated || res.ImplementationsTruncated {
		t.Fatalf("indirect %+v", res)
	}

	// A class: its subclasses, and theirs below them.
	res, err = s.References(context.Background(), phpWalkRequest(t, 6, 8))
	if err != nil {
		t.Fatal(err)
	}
	wantSubs := []Location{{Path: "src/walk.php", Line: 8, Character: 6}, {Path: "src/walk.php", Line: 12, Character: 6}}
	wantDeeper := []Location{{Path: "src/walk.php", Line: 9, Character: 12}, {Path: "src/Sub/deck.php", Line: 6, Character: 6}}
	if res.Below != groupSubclasses || !reflect.DeepEqual(res.Subclasses, wantSubs) || !reflect.DeepEqual(res.Indirect, wantDeeper) {
		t.Fatalf("square %+v", res)
	}

	// Two classes extending each other: neither stands below itself.
	res, err = s.References(context.Background(), phpWalkRequest(t, 12, 8))
	if err != nil {
		t.Fatal(err)
	}
	if want := []Location{{Path: "src/walk.php", Line: 14, Character: 6}}; !reflect.DeepEqual(res.Subclasses, want) || len(res.Indirect) != 0 || res.IndirectTruncated {
		t.Fatalf("knot %+v", res)
	}
}

func TestServiceWalkIsBoundedInTime(t *testing.T) {
	installFakeLSP(t, "normal", "intelephense")
	s := newTestService(t)
	s.walkBudget = time.Nanosecond

	// The direct ones stand, and the indirect ones say they were cut.
	res, err := s.References(context.Background(), phpWalkRequest(t, 3, 12))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Implementations) != 3 || len(res.Indirect) != 0 || !res.IndirectTruncated || res.ImplementationsTruncated {
		t.Fatalf("implementations %+v", res)
	}
}

// walkPythonChain is a chain of classes each extending the one before it,
// longer than the walk goes.
func walkPythonChain(n int) string {
	var b strings.Builder
	b.WriteString("class C0:\n    pass\n")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&b, "\n\nclass C%d(C%d):\n    pass\n", i, i-1)
	}
	return b.String()
}

func TestServiceWalkIsBoundedInCount(t *testing.T) {
	installFakeLSP(t, "lists-late", "pyright-langserver")
	s := newTestService(t)

	// C1 extends C0, every class below it through C1: the walk asks C1 to
	// C50 and lists what they name, C2 to C51, and says it was cut.
	files := map[string]string{"src/shapes/chain.py": walkPythonChain(maxWalkTypes + 5)}
	res, err := s.References(context.Background(), walkRequest(t, files, "src/shapes/chain.py", 0, 6))
	if err != nil {
		t.Fatal(err)
	}
	if want := []Location{{Path: "src/shapes/chain.py", Line: 5, Character: 6}}; !reflect.DeepEqual(res.Subclasses, want) {
		t.Fatalf("direct %+v", res.Subclasses)
	}
	if len(res.Indirect) != maxWalkTypes || res.Indirect[maxWalkTypes-1].Line != 1+4*(maxWalkTypes+1) || !res.IndirectTruncated {
		t.Fatalf("indirect %d %+v", len(res.Indirect), res)
	}

	// A chain the walk reaches the end of is whole.
	files = map[string]string{"src/shapes/chain.py": walkPythonChain(maxWalkTypes)}
	res, err = s.References(context.Background(), walkRequest(t, files, "src/shapes/chain.py", 0, 6))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Indirect) != maxWalkTypes-2 || res.IndirectTruncated {
		t.Fatalf("whole %d %+v", len(res.Indirect), res)
	}
}
