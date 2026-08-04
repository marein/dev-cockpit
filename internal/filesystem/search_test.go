package filesystem

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSearchFindsMatchesPastTheOldFileLimit is the regression test for the bug
// this scan was rewritten to fix. The old search stopped after 5000 files, so a
// needle living further down the walk was reported as "no matches" - fast,
// confident and wrong.
func TestSearchFindsMatchesPastTheOldFileLimit(t *testing.T) {
	root := t.TempDir()
	// 5001 files sorting before the needle, so the old limit would be spent
	// before ever reaching it.
	for i := 0; i < 5001; i++ {
		writeFile(t, root, "aaa/pad"+strconv.Itoa(i)+".php", "<?php // nothing here\n")
	}
	writeFile(t, root, "src/ConnectFour/Domain/Game/Game.php", "<?php\nfinal class Game\n{\n}\n")

	matches, truncated, err := SearchFiles(root, "final class Game", DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %v, want exactly one", matches)
	}
	if matches[0].Path != "src/ConnectFour/Domain/Game/Game.php" {
		t.Errorf("path = %q", matches[0].Path)
	}
	if matches[0].Line != 2 {
		t.Errorf("line = %d, want 2", matches[0].Line)
	}
	if truncated {
		t.Error("a single match must not be reported as truncated")
	}
}

func TestSearchIsCaseInsensitiveAndReportsLines(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/a.php", "first\nSECOND needle here\nthird\nand NeEdLe again\n")

	matches, _, err := SearchFiles(root, "NEEDLE", DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	want := []SearchMatch{
		{Path: "src/a.php", Line: 2, Text: "SECOND needle here"},
		{Path: "src/a.php", Line: 4, Text: "and NeEdLe again"},
	}
	if !reflect.DeepEqual(matches, want) {
		t.Errorf("matches = %#v\nwant %#v", matches, want)
	}
}

// TestSearchReportsOneMatchPerLine pins the behaviour the palette relies on: a
// line containing the needle twice is still one entry.
func TestSearchReportsOneMatchPerLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "needle and needle on one line\nplain\nneedle\n")

	matches, _, err := SearchFiles(root, "needle", DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %v, want 2 (one per matching line)", matches)
	}
	if matches[0].Line != 1 || matches[1].Line != 3 {
		t.Errorf("lines = %d, %d, want 1, 3", matches[0].Line, matches[1].Line)
	}
}

func TestSearchLineNumbersWithoutTrailingNewline(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "one\ntwo\nneedle")
	matches, _, err := SearchFiles(root, "needle", DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Line != 3 || matches[0].Text != "needle" {
		t.Errorf("matches = %#v, want line 3", matches)
	}
}

func TestSearchCapsTheAnswerAndReportsTruncation(t *testing.T) {
	root := t.TempDir()
	// Spread the matches over enough files to cross a batch boundary.
	for f := 0; f < 20; f++ {
		var b strings.Builder
		for i := 0; i < 40; i++ {
			b.WriteString("needle line\n")
		}
		writeFile(t, root, "src/f"+strconv.Itoa(f)+".txt", b.String())
	}
	matches, truncated, err := SearchFiles(root, "needle", DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != MaxSearchMatches {
		t.Errorf("matches = %d, want %d", len(matches), MaxSearchMatches)
	}
	if !truncated {
		t.Error("truncated must be set when matches were left out")
	}
}

// TestSearchIsDeterministic is the guard on the parallel scan: workers finish in
// disk order, so without the final sort the same query would answer differently
// from run to run.
func TestSearchIsDeterministic(t *testing.T) {
	root := t.TempDir()
	for f := 0; f < 60; f++ {
		writeFile(t, root, "src/dir"+strconv.Itoa(f%7)+"/f"+strconv.Itoa(f)+".txt",
			"pad\nneedle one\npad\nneedle two\n")
	}
	first, _, err := SearchFiles(root, "needle", DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		again, _, err := SearchFiles(root, "needle", DefaultExclusionSet())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d differs from the first run", i)
		}
	}
	// And the order must be by path, then line.
	for i := 1; i < len(first); i++ {
		a, b := first[i-1], first[i]
		if a.Path > b.Path || (a.Path == b.Path && a.Line >= b.Line) {
			t.Fatalf("not ordered at %d: %v then %v", i, a, b)
		}
	}
}

// TestSearchMatchesSerialScan is the equivalence check that makes the batched
// parallel scan safe to swap in: it must return exactly what a plain in-order
// single threaded scan would.
func TestSearchMatchesSerialScan(t *testing.T) {
	root := t.TempDir()
	for f := 0; f < 900; f++ {
		body := "alpha\nbeta\n"
		if f%5 == 0 {
			body = "alpha\nneedle in " + strconv.Itoa(f) + "\nbeta\nneedle twice\n"
		}
		writeFile(t, root, "d"+strconv.Itoa(f%11)+"/f"+strconv.Itoa(f)+".txt", body)
	}
	got, gotTrunc, err := SearchFiles(root, "needle", DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	want, wantTrunc := serialSearchReference(t, root, "needle")
	if gotTrunc != wantTrunc {
		t.Errorf("truncated = %v, want %v", gotTrunc, wantTrunc)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parallel scan differs from the serial reference:\n got %d matches, want %d", len(got), len(want))
		for i := 0; i < len(got) && i < len(want); i++ {
			if got[i] != want[i] {
				t.Errorf("  first difference at %d: got %v, want %v", i, got[i], want[i])
				break
			}
		}
	}
}

// serialSearchReference is a deliberately naive in-order scan: walk everything,
// read every file, take the first MaxSearchMatches matching lines. It is the
// definition the real implementation has to match.
func serialSearchReference(t *testing.T, root, query string) ([]SearchMatch, bool) {
	t.Helper()
	needle := strings.ToLower(query)
	out := []SearchMatch{}
	truncated := false
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > maxSearchFileBytes {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(strings.ToLower(line), needle) {
				continue
			}
			out = append(out, SearchMatch{Path: relTo(root, path), Line: i + 1, Text: searchSnippet([]byte(line), needle)})
			if len(out) >= MaxSearchMatches {
				truncated = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out, truncated
}

func TestSearchSkipsBinaryAndOversizedFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "text.txt", "needle here\n")
	writeFile(t, root, "binary.bin", "needle\x00inside\n")
	big := strings.Repeat("x", maxSearchFileBytes+1024) + "needle\n"
	writeFile(t, root, "big.txt", big)

	matches, _, err := SearchFiles(root, "needle", DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Path != "text.txt" {
		t.Errorf("matches = %v, want only text.txt", matches)
	}
}

func TestSearchSkipsExcludedDirs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/keep.php", "needle\n")
	for _, dir := range DefaultExclusions {
		writeFile(t, root, dir+"/hidden.php", "needle\n")
	}
	matches, _, err := SearchFiles(root, "needle", DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Path != "src/keep.php" {
		t.Errorf("matches = %v, want only src/keep.php", matches)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "anything\n")
	matches, truncated, err := SearchFiles(root, "   ", DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 || truncated {
		t.Errorf("empty query = %v / %v, want no matches", matches, truncated)
	}
}

func TestSearchSnippetTrimsLongLines(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("a", 400) + "needle" + strings.Repeat("b", 400)
	writeFile(t, root, "long.txt", long+"\n")
	matches, _, err := SearchFiles(root, "needle", DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %v", matches)
	}
	if len(matches[0].Text) > maxSnippetBytes {
		t.Errorf("snippet is %d bytes, want at most %d", len(matches[0].Text), maxSnippetBytes)
	}
	if !strings.Contains(matches[0].Text, "needle") {
		t.Error("snippet lost the needle it was cut around")
	}
}
