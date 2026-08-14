package editorintelligence

import (
	"encoding/json"
	"testing"
)

func TestDecodeLocationsShapes(t *testing.T) {
	single := json.RawMessage(`{"uri":"file:///r/a.php","range":{"start":{"line":2,"character":4},"end":{"line":2,"character":9}}}`)
	locs, err := decodeLocations(single)
	if err != nil || len(locs) != 1 || locs[0].URI != "file:///r/a.php" || locs[0].Range.Start.Line != 2 {
		t.Fatalf("single: %v %+v", err, locs)
	}

	array := json.RawMessage(`[{"uri":"file:///r/a.php","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}}},{"uri":"file:///r/b.php","range":{"start":{"line":5,"character":8},"end":{"line":5,"character":11}}}]`)
	locs, err = decodeLocations(array)
	if err != nil || len(locs) != 2 || locs[1].URI != "file:///r/b.php" {
		t.Fatalf("array: %v %+v", err, locs)
	}

	links := json.RawMessage(`[{"targetUri":"file:///r/c.php","targetRange":{"start":{"line":9,"character":0},"end":{"line":12,"character":1}},"targetSelectionRange":{"start":{"line":9,"character":6},"end":{"line":9,"character":10}}}]`)
	locs, err = decodeLocations(links)
	if err != nil || len(locs) != 1 || locs[0].URI != "file:///r/c.php" || locs[0].Range.Start.Character != 6 {
		t.Fatalf("links: %v %+v", err, locs)
	}

	for _, raw := range []string{"", "null"} {
		locs, err = decodeLocations(json.RawMessage(raw))
		if err != nil || len(locs) != 0 {
			t.Fatalf("%q: %v %+v", raw, err, locs)
		}
	}
}

func TestMapLocations(t *testing.T) {
	roots := []SourceRoot{{Path: "/cache/mod"}, {Path: "/usr/lib/stubs", Image: "img:tag"}}
	raw := []lspLocation{
		{URI: "file:///r/proj/a.php", Range: lspRange{Start: lspPosition{Line: 2, Character: 4}}},
		// A duplicate collapses.
		{URI: "file:///r/proj/a.php", Range: lspRange{Start: lspPosition{Line: 2, Character: 4}}},
		// Percent encoding unescapes.
		{URI: "file:///r/proj/sp%20ace.php", Range: lspRange{Start: lspPosition{Line: 0, Character: 0}}},
		// Under a source root: openable, absolute and marked, whether the
		// root lies on this host or inside an image.
		{URI: "file:///cache/mod/example.com/dep@v1.2.3/dep.go", Range: lspRange{Start: lspPosition{Line: 9}}},
		{URI: "file:///usr/lib/stubs/str.php", Range: lspRange{Start: lspPosition{Line: 1}}},
		// Neither in the project nor under a root, and a sibling with a
		// root's name as its prefix: both only counted.
		{URI: "file:///etc/passwd"},
		{URI: "file:///r/project-b/x.php"},
		{URI: "file:///cache/module/x.go"},
	}
	locs, outside := mapLocations("file:///r/proj", raw, roots)
	if outside != 3 {
		t.Fatalf("outside = %d", outside)
	}
	want := []Location{
		{Path: "a.php", Line: 3, Character: 4},
		{Path: "sp ace.php", Line: 1},
		{Path: "/cache/mod/example.com/dep@v1.2.3/dep.go", Line: 10, External: true},
		{Path: "/usr/lib/stubs/str.php", Line: 2, External: true},
	}
	if len(locs) != len(want) {
		t.Fatalf("locs %+v", locs)
	}
	for i := range want {
		if locs[i] != want[i] {
			t.Fatalf("loc %d = %+v, want %+v", i, locs[i], want[i])
		}
	}
	// Without roots the same answer stays what it was: counted, never
	// opened.
	locs, outside = mapLocations("file:///r/proj", raw, nil)
	if len(locs) != 2 || outside != 5 {
		t.Fatalf("no roots: %d locs, %d outside", len(locs), outside)
	}
}

func TestSortReferences(t *testing.T) {
	locs := []Location{
		{Path: "b.php", Line: 3},
		{Path: "a.php", Line: 9},
		{Path: "b.php", Line: 1},
		{Path: "use.php", Line: 7},
		{Path: "use.php", Line: 2},
	}
	sortReferences(locs, "use.php")
	want := []Location{
		{Path: "use.php", Line: 2},
		{Path: "use.php", Line: 7},
		{Path: "a.php", Line: 9},
		{Path: "b.php", Line: 1},
		{Path: "b.php", Line: 3},
	}
	for i := range want {
		if locs[i] != want[i] {
			t.Fatalf("order %+v", locs)
		}
	}
}

func TestProfileForPath(t *testing.T) {
	if p, lang, ok := ProfileForPath("src/Kernel.php"); !ok || p.ID != "php" || lang != "php" {
		t.Fatalf("php: %v %v %v", p, lang, ok)
	}
	// One server owns both languages, so the profile is the same for every
	// one of its extensions and only the language id tells them apart, which
	// is what didOpen carries.
	for path, want := range map[string]string{
		"src/index.ts":     "typescript",
		"src/index.mts":    "typescript",
		"src/index.cts":    "typescript",
		"src/App.tsx":      "typescriptreact",
		"src/index.js":     "javascript",
		"src/index.mjs":    "javascript",
		"src/index.cjs":    "javascript",
		"src/App.jsx":      "javascriptreact",
		"src/INDEX.TS":     "typescript",
		"vite.config.mts":  "typescript",
		"eslint.config.js": "javascript",
	} {
		if p, lang, ok := ProfileForPath(path); !ok || p.ID != "typescript" || lang != want {
			t.Errorf("%s: %v %v %v, want typescript/%s", path, p, lang, ok, want)
		}
	}
	if _, _, ok := ProfileForPath("README.md"); ok {
		t.Fatal("md must have no profile")
	}
	if _, _, ok := ProfileForPath("Makefile"); ok {
		t.Fatal("no extension must have no profile")
	}
}
