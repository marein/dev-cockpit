package editorintelligence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// structureLines writes a structure one symbol a line, indented by depth.
func structureLines(symbols []StructureSymbol) string {
	var b strings.Builder
	var walk func(symbols []StructureSymbol, depth int)
	walk = func(symbols []StructureSymbol, depth int) {
		for _, sym := range symbols {
			fmt.Fprintf(&b, "%s%s %s [%s] %d:%d\n", strings.Repeat("  ", depth), sym.Kind, sym.Name, sym.Detail, sym.Line, sym.Character)
			walk(sym.Children, depth+1)
		}
	}
	walk(symbols, 0)
	return b.String()
}

// cursorNames lists the symbols marked as standing around the cursor.
func cursorNames(symbols []StructureSymbol) []string {
	var names []string
	for _, sym := range symbols {
		if sym.Cursor {
			names = append(names, sym.Name)
		}
		names = append(names, cursorNames(sym.Children)...)
	}
	return names
}

func span(line, from, toLine, to int) string {
	return fmt.Sprintf(`{"start": {"line": %d, "character": %d}, "end": {"line": %d, "character": %d}}`, line, from, toLine, to)
}

// A tree keeps its nesting and its details, its children come in file
// order whatever order the server gave them in, as pyright gives the
// attributes a constructor assigns after the methods, and a body's content
// stays out.
func TestDocumentStructureOfATree(t *testing.T) {
	raw := json.RawMessage(`[
		{"name": "Service", "detail": "struct{...}", "kind": 23, "range": ` + span(2, 5, 6, 1) + `, "selectionRange": ` + span(2, 5, 2, 12) + `,
		 "children": [{"name": "Name", "detail": "string", "kind": 8, "range": ` + span(3, 1, 3, 12) + `, "selectionRange": ` + span(3, 1, 3, 5) + `}]},
		{"name": "Store", "kind": 5, "range": ` + span(8, 0, 20, 0) + `, "selectionRange": ` + span(8, 6, 8, 11) + `,
		 "children": [
			{"name": "__init__", "kind": 6, "range": ` + span(9, 4, 11, 0) + `, "selectionRange": ` + span(9, 8, 9, 16) + `,
			 "children": [{"name": "items", "kind": 13, "range": ` + span(9, 23, 9, 28) + `, "selectionRange": ` + span(9, 23, 9, 28) + `}]},
			{"name": "find", "detail": "func(  sku\n  string) Item", "kind": 6, "range": ` + span(13, 4, 14, 0) + `, "selectionRange": ` + span(13, 8, 13, 12) + `,
			 "children": [{"name": "found", "kind": 13, "range": ` + span(14, 8, 14, 13) + `, "selectionRange": ` + span(14, 8, 14, 13) + `}]},
			{"name": "items", "kind": 13, "range": ` + span(10, 8, 10, 18) + `, "selectionRange": ` + span(10, 13, 10, 18) + `}
		 ]},
		{"name": "(*Service).Run", "detail": "func() error", "kind": 6, "range": ` + span(7, 0, 7, 30) + `, "selectionRange": ` + span(7, 15, 7, 18) + `}
	]`)
	symbols, cut, err := documentStructure(raw)
	if err != nil || cut {
		t.Fatalf("%v %v", err, cut)
	}
	want := `struct Service [struct{...}] 3:5
  field Name [string] 4:1
method (*Service).Run [func() error] 8:15
class Store [] 9:6
  method __init__ [] 10:8
  variable items [] 11:13
  method find [func( sku string) Item] 14:8
`
	if got := structureLines(symbols); got != want {
		t.Fatalf("structure:\n%s", got)
	}
}

// A flat list nests by what each declaration encloses, under the container
// it names where an enclosing one is named so, and a list mixing both forms
// places its plain entries alike.
func TestDocumentStructureOfAFlatList(t *testing.T) {
	flat := func(name string, kind int, container, r string) string {
		return fmt.Sprintf(`{"name": %q, "kind": %d, "containerName": %q, "location": {"uri": "file:///p/a.ts", "range": %s}}`, name, kind, container, r)
	}
	raw := json.RawMessage("[" + strings.Join([]string{
		flat("ledger", 7, "BillingService", span(12, 14, 12, 28)),
		flat("BillingService", 5, "", span(9, 0, 30, 1)),
		flat("constructor", 9, "BillingService", span(12, 2, 12, 40)),
		flat("settle", 6, "BillingService", span(20, 2, 25, 3)),
		flat("amount", 13, "settle", span(21, 10, 21, 16)),
		flat("VAT_RATE", 13, "", span(2, 13, 2, 28)),
		`{"name": "Page", "kind": 5, "range": ` + span(32, 0, 40, 1) + `, "selectionRange": ` + span(32, 6, 32, 10) + `}`,
		flat("Script", 5, "", span(35, 8, 35, 30)),
	}, ",") + "]")
	symbols, _, err := documentStructure(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := `variable VAT_RATE [] 3:13
class BillingService [] 10:0
  constructor constructor [] 13:2
  property ledger [] 13:14
  method settle [] 21:2
class Page [] 33:6
  class Script [] 36:8
`
	if got := structureLines(symbols); got != want {
		t.Fatalf("structure:\n%s", got)
	}
}

// The cursor marks the innermost declaration whose whole span holds it, a
// member over its type, the narrower of two members overlapping as a
// constructor and a property it declares do, the deeper of two equal spans,
// the declaration itself where a body's content is left out, and nothing
// outside every declaration.
func TestMarkCursor(t *testing.T) {
	tree := json.RawMessage(`[
		{"name": "Service", "kind": 23, "range": ` + span(2, 5, 6, 1) + `, "selectionRange": ` + span(2, 5, 2, 12) + `,
		 "children": [{"name": "Name", "kind": 8, "range": ` + span(3, 1, 3, 12) + `, "selectionRange": ` + span(3, 1, 3, 5) + `}]},
		{"name": "Run", "kind": 6, "range": ` + span(7, 0, 7, 30) + `, "selectionRange": ` + span(7, 15, 7, 18) + `},
		{"name": "Store", "kind": 5, "range": ` + span(8, 0, 20, 0) + `, "selectionRange": ` + span(8, 6, 8, 11) + `,
		 "children": [
			{"name": "__init__", "kind": 6, "range": ` + span(9, 4, 11, 0) + `, "selectionRange": ` + span(9, 8, 9, 16) + `,
			 "children": [{"name": "local", "kind": 13, "range": ` + span(9, 23, 9, 28) + `, "selectionRange": ` + span(9, 23, 9, 28) + `}]},
			{"name": "items", "kind": 13, "range": ` + span(10, 8, 10, 18) + `, "selectionRange": ` + span(10, 13, 10, 18) + `},
			{"name": "Inner", "kind": 5, "range": ` + span(13, 4, 15, 0) + `, "selectionRange": ` + span(13, 10, 13, 15) + `,
			 "children": [{"name": "Same", "kind": 5, "range": ` + span(13, 4, 15, 0) + `, "selectionRange": ` + span(13, 10, 13, 15) + `}]}
		 ]}
	]`)
	for _, c := range []struct {
		line, character int
		want            string
	}{
		{0, 0, ""},
		{2, 4, ""},
		{2, 5, "Service"},
		{3, 6, "Name"},
		{6, 1, "Service"},
		{6, 2, ""},
		{7, 3, "Run"},
		{9, 25, "__init__"},
		{10, 12, "items"},
		{12, 0, "Store"},
		{14, 2, "Same"},
		{21, 0, ""},
	} {
		symbols, _, err := documentStructure(tree)
		if err != nil {
			t.Fatal(err)
		}
		markCursor(symbols, lspPosition{Line: c.line, Character: c.character})
		if got := strings.Join(cursorNames(symbols), ","); got != c.want {
			t.Fatalf("%d:%d marked %q, want %q", c.line, c.character, got, c.want)
		}
	}

	flat := func(name string, kind int, container, r string) string {
		return fmt.Sprintf(`{"name": %q, "kind": %d, "containerName": %q, "location": {"uri": "file:///p/a.ts", "range": %s}}`, name, kind, container, r)
	}
	list := json.RawMessage("[" + strings.Join([]string{
		flat("BillingService", 5, "", span(9, 0, 30, 1)),
		flat("constructor", 9, "BillingService", span(12, 2, 12, 40)),
		flat("ledger", 7, "BillingService", span(12, 14, 12, 28)),
		flat("settle", 6, "BillingService", span(20, 2, 25, 3)),
		flat("amount", 13, "settle", span(21, 10, 21, 16)),
	}, ",") + "]")
	for _, c := range []struct {
		line, character int
		want            string
	}{
		{12, 5, "constructor"},
		{12, 20, "ledger"},
		{21, 12, "settle"},
		{28, 0, "BillingService"},
	} {
		symbols, _, err := documentStructure(list)
		if err != nil {
			t.Fatal(err)
		}
		markCursor(symbols, lspPosition{Line: c.line, Character: c.character})
		if got := strings.Join(cursorNames(symbols), ","); got != c.want {
			t.Fatalf("flat %d:%d marked %q, want %q", c.line, c.character, got, c.want)
		}
	}
}

// A document declaring more than the cap is cut in file order and says so,
// an empty answer is an empty structure and a broken one an error.
func TestDocumentStructureCut(t *testing.T) {
	entries := make([]string, 0, maxStructureSymbols+1)
	for i := range maxStructureSymbols + 1 {
		entries = append(entries, fmt.Sprintf(`{"name": "c%d", "kind": 14, "range": %s, "selectionRange": %s}`, i, span(i, 0, i, 5), span(i, 0, i, 2)))
	}
	symbols, cut, err := documentStructure(json.RawMessage("[" + strings.Join(entries, ",") + "]"))
	if err != nil || !cut || len(symbols) != maxStructureSymbols || symbols[0].Name != "c0" {
		t.Fatalf("cut %v %v %d", err, cut, len(symbols))
	}
	if _, _, err := documentStructure(json.RawMessage("{")); err == nil {
		t.Fatal("a broken answer read as a structure")
	}
	for _, raw := range []string{"null", "[]", " "} {
		if symbols, cut, err := documentStructure(json.RawMessage(raw)); err != nil || cut || symbols == nil || len(symbols) != 0 {
			t.Fatalf("%q: %v %v %v", raw, symbols, cut, err)
		}
	}
}

// The structure goes out while the project still indexes, reads the text
// the request carries, not the file on disk, and marks the declaration
// around the position it carries.
func TestStructureAnswersWhileTheIndexRuns(t *testing.T) {
	installFakeLSP(t, "held", "intelephense")
	s := newTestService(t)
	req := goRequest(t)
	req.Path = "src/Thing.php"
	req.Content = "<?php\n\nclass Thing {}\n"
	req.Line, req.Character = 2, 9
	start := time.Now()
	res := s.Structure(context.Background(), req)
	if !res.Available || structureLines(res.Symbols) != "class Thing [] 3:6\n" || strings.Join(cursorNames(res.Symbols), ",") != "Thing" {
		t.Fatalf("structure %+v", res)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the structure waited for the index, took %v", took)
	}
	states := s.IndexStatus("proj")
	if len(states) != 1 || !states[0].Indexing {
		t.Fatalf("the index was not running: %+v", states)
	}
}

func TestStructureOfAFileNoServerCovers(t *testing.T) {
	req := goRequest(t)
	req.Path = "notes.txt"
	if res := newTestService(t).Structure(context.Background(), req); res.Available || res.Status != StatusNoLanguage || res.Symbols == nil {
		t.Fatalf("no language %+v", res)
	}
	if res := (*Service)(nil).Structure(context.Background(), req); res.Available || res.Status != StatusUnavailable {
		t.Fatalf("no service %+v", res)
	}
}

// Headings nest under the nearest one before them of a higher level, a
// skipped level included, each at the line and the UTF-16 column its words
// start at; a fenced block's lines stay out.
func TestMarkdownStructureNestsByLevel(t *testing.T) {
	content := "Intro.\n\n# Guide\n\n## Install\n\n```sh\n# not a heading\n```\n\nSetup\n-----\n\n### Linux `apt`\n\nUsage\n=====\n\n#### Deep\n\n## 🙂 Notes ##\n"
	res := MarkdownStructure(content, 0, 0)
	want := "h1 Guide [] 3:2\n  h2 Install [] 5:3\n  h2 Setup [] 11:0\n    h3 Linux apt [] 14:4\n" +
		"h1 Usage [] 16:0\n  h4 Deep [] 19:5\n  h2 🙂 Notes [] 21:3\n"
	if !res.Available || res.Truncated || structureLines(res.Symbols) != want {
		t.Fatalf("want\n%s\ngot %+v\n%s", want, res, structureLines(res.Symbols))
	}
	if empty := MarkdownStructure("No heading here.\n", 0, 0); !empty.Available || empty.Symbols == nil || len(empty.Symbols) != 0 {
		t.Fatalf("no headings %+v", empty)
	}
}

// A heading's section runs to the next heading of its level or above; the
// cursor marks the innermost section it stands in, nothing before the
// first heading.
func TestMarkdownStructureMarksTheSectionAroundTheCursor(t *testing.T) {
	content := "Intro.\n# A\ntext of A\n## B\n```\n# code\n```\n### C\ntext of C\n## D\n# E\nlast"
	for _, tc := range []struct {
		line, character int
		want            string
	}{
		{0, 3, ""},
		{1, 0, "A"},
		{2, 4, "A"},
		{5, 1, "B"},
		{7, 5, "C"},
		{8, 0, "C"},
		{9, 0, "D"},
		{10, 2, "E"},
		{11, 4, "E"},
	} {
		got := strings.Join(cursorNames(MarkdownStructure(content, tc.line, tc.character).Symbols), ",")
		if got != tc.want {
			t.Fatalf("%d:%d marked %q, want %q", tc.line, tc.character, got, tc.want)
		}
	}
}

func TestMarkdownStructureCut(t *testing.T) {
	res := MarkdownStructure(strings.Repeat("# h\n", maxStructureSymbols+5), 0, 0)
	if !res.Truncated || len(res.Symbols) != maxStructureSymbols {
		t.Fatalf("want the cut at %d, got %d truncated=%v", maxStructureSymbols, len(res.Symbols), res.Truncated)
	}
}
