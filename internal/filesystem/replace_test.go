package filesystem

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func readBack(t *testing.T, root, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestReplaceLiteralKeepsTheDollar is the mistake this function is most often
// found with: without a regex there are no back references, so a dollar in the
// replacement is a dollar in the file.
func TestReplaceLiteralKeepsTheDollar(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.php", "echo NEEDLE;\necho needle;\n")

	report, err := ApplyReplace(root, ReplaceRequest{Query: "needle", Replacement: "$1 price"}, DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if report.Replaced != 2 || len(report.Changed) != 1 {
		t.Fatalf("report = %+v, want two occurrences in one file", report)
	}
	if got := readBack(t, root, "a.php"); got != "echo $1 price;\necho $1 price;\n" {
		t.Errorf("file = %q, the dollar did not survive", got)
	}
}

// TestReplaceRegexExpandsBackReferences is the other half: with a regex, $1 is
// what it means everywhere else.
func TestReplaceRegexExpandsBackReferences(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "func GetName() {}\nfunc GetAge() {}\n")

	report, err := ApplyReplace(root, ReplaceRequest{
		Query: `Get(\w+)`, Replacement: "Read$1", UseRegex: true,
	}, DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if report.Replaced != 2 {
		t.Fatalf("replaced = %d, want 2", report.Replaced)
	}
	if got := readBack(t, root, "a.go"); got != "func ReadName() {}\nfunc ReadAge() {}\n" {
		t.Errorf("file = %q", got)
	}
}

// TestReplaceReachesPastThePreviewCap is the promise the button makes: the list
// shows the first MaxSearchMatches lines, the replacement takes every one.
func TestReplaceReachesPastThePreviewCap(t *testing.T) {
	root := t.TempDir()
	const files, perFile = 6, 60
	for f := 0; f < files; f++ {
		var b strings.Builder
		for i := 0; i < perFile; i++ {
			b.WriteString("needle line\n")
		}
		writeFile(t, root, "src/f"+strconv.Itoa(f)+".txt", b.String())
	}
	want := files * perFile

	preview, err := PreviewReplace(root, ReplaceRequest{Query: "needle", Replacement: "found"}, DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Matches) != MaxSearchMatches || !preview.Truncated {
		t.Errorf("preview shows %d rows, truncated = %v", len(preview.Matches), preview.Truncated)
	}
	if preview.Total != want || preview.Files != files {
		t.Errorf("preview counts %d in %d, want %d in %d", preview.Total, preview.Files, want, files)
	}

	report, err := ApplyReplace(root, ReplaceRequest{Query: "needle", Replacement: "found"}, DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if report.Replaced != want || len(report.Changed) != files {
		t.Errorf("replaced %d in %d files, want %d in %d", report.Replaced, len(report.Changed), want, files)
	}
	if left, _, err := SearchFiles(root, "needle", false, DefaultExclusionSet(), SearchOptions{}); err != nil || len(left) != 0 {
		t.Errorf("matches left after replacing everything: %v", left)
	}
}

// TestReplacePreviewShowsBeforeAndAfter is what makes the list worth trusting.
func TestReplacePreviewShowsBeforeAndAfter(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "the needle sits here\nplain line\n")

	report, err := PreviewReplace(root, ReplaceRequest{Query: "needle", Replacement: "pin"}, DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Matches) != 1 {
		t.Fatalf("matches = %#v", report.Matches)
	}
	row := report.Matches[0]
	if row.Text != "the needle sits here" || row.Text[row.MatchStart:row.MatchStart+row.MatchLen] != "needle" {
		t.Errorf("before = %q marked at %d+%d", row.Text, row.MatchStart, row.MatchLen)
	}
	if row.After != "the pin sits here" || row.After[row.AfterStart:row.AfterStart+row.AfterLen] != "pin" {
		t.Errorf("after = %q marked at %d+%d", row.After, row.AfterStart, row.AfterLen)
	}
	if readBack(t, root, "a.txt") != "the needle sits here\nplain line\n" {
		t.Error("a preview wrote to the file")
	}
}

// TestReplaceHonoursScopeMaskAndLeavesTheRestAlone keeps the replacement inside
// what the search in front of it showed, and never opens a file it does not
// change.
func TestReplaceHonoursScopeMaskAndLeavesTheRestAlone(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "top.go", "needle\n")
	writeFile(t, root, "src/in.go", "needle\n")
	writeFile(t, root, "src/in.md", "needle\n")
	writeFile(t, root, "src/deep/in.go", "needle\n")
	untouched := filepath.Join(root, "src", "in.md")
	before, err := os.Stat(untouched)
	if err != nil {
		t.Fatal(err)
	}

	report, err := ApplyReplace(root, ReplaceRequest{
		Query: "needle", Replacement: "found",
		Options: SearchOptions{Folder: "src", Mask: ParseFileMask("*.go")},
	}, DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(report.Changed, ",") != "src/deep/in.go,src/in.go" {
		t.Errorf("changed = %v, want the two go files under src", report.Changed)
	}
	if readBack(t, root, "top.go") != "needle\n" {
		t.Error("the file outside the scope was changed")
	}
	if readBack(t, root, "src/in.md") != "needle\n" {
		t.Error("the file outside the mask was changed")
	}
	after, err := os.Stat(untouched)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("a file without a match was written to")
	}
}

// TestReplaceRefusesWhileABufferIsUnsaved is the guard that keeps a browser's
// buffer and the disk from parting ways: one held file stops the whole job.
func TestReplaceRefusesWhileABufferIsUnsaved(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "needle\n")
	writeFile(t, root, "b.txt", "needle\n")

	report, err := ApplyReplace(root, ReplaceRequest{
		Query: "needle", Replacement: "found", Dirty: []string{"b.txt"},
	}, DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(report.Blocked, ",") != "b.txt" {
		t.Errorf("blocked = %v, want b.txt", report.Blocked)
	}
	if report.Replaced != 0 || len(report.Changed) != 0 {
		t.Errorf("a refused job wrote something: %+v", report)
	}
	for _, rel := range []string{"a.txt", "b.txt"} {
		if readBack(t, root, rel) != "needle\n" {
			t.Errorf("%s was written although the job was refused", rel)
		}
	}
}

// TestReplaceOneLineTakesOnlyThatLine is the row's own replace.
func TestReplaceOneLineTakesOnlyThatLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "needle one\nneedle two\nneedle three\n")
	writeFile(t, root, "b.txt", "needle elsewhere\n")

	report, err := ApplyReplace(root, ReplaceRequest{
		Query: "needle", Replacement: "found", OnlyPath: "a.txt", OnlyLine: 2,
	}, DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if report.Replaced != 1 || strings.Join(report.Changed, ",") != "a.txt" {
		t.Fatalf("report = %+v, want one line of a.txt", report)
	}
	if got := readBack(t, root, "a.txt"); got != "needle one\nfound two\nneedle three\n" {
		t.Errorf("file = %q", got)
	}
	if readBack(t, root, "b.txt") != "needle elsewhere\n" {
		t.Error("another file was changed by a single line replace")
	}
}

// TestReplaceOnALineThatMovedWritesNothing covers the disk moving between the
// preview and the press.
func TestReplaceOnALineThatMovedWritesNothing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "needle one\nplain\n")

	report, err := ApplyReplace(root, ReplaceRequest{
		Query: "needle", Replacement: "found", OnlyPath: "a.txt", OnlyLine: 2,
	}, DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if report.Replaced != 0 || len(report.Changed) != 0 {
		t.Errorf("report = %+v, want nothing written", report)
	}
	if readBack(t, root, "a.txt") != "needle one\nplain\n" {
		t.Error("the file changed")
	}
}

// TestReplaceMindsCaseWhenAskedTo is the search's rule on the writing side:
// what the option leaves out is not written either.
func TestReplaceMindsCaseWhenAskedTo(t *testing.T) {
	body := "GetName\ngetname\nGETNAME\n"

	root := t.TempDir()
	writeFile(t, root, "a.go", body)
	report, err := ApplyReplace(root, ReplaceRequest{
		Query: "getname", Replacement: "found",
		Options: SearchOptions{CaseSensitive: true},
	}, DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if report.Replaced != 1 {
		t.Errorf("replaced = %d, want only the one that was typed", report.Replaced)
	}
	if got := readBack(t, root, "a.go"); got != "GetName\nfound\nGETNAME\n" {
		t.Errorf("file = %q", got)
	}

	// The same query without the option takes all three, which is what it
	// always did.
	folded := t.TempDir()
	writeFile(t, folded, "a.go", body)
	report, err = ApplyReplace(folded, ReplaceRequest{Query: "getname", Replacement: "found"}, DefaultExclusionSet())
	if err != nil {
		t.Fatal(err)
	}
	if report.Replaced != 3 {
		t.Errorf("replaced = %d, want all three", report.Replaced)
	}

	// And with a regex, the back reference still expands.
	rx := t.TempDir()
	writeFile(t, rx, "a.go", body)
	if _, err := ApplyReplace(rx, ReplaceRequest{
		Query: `get(\w+)`, Replacement: "read$1", UseRegex: true,
		Options: SearchOptions{CaseSensitive: true},
	}, DefaultExclusionSet()); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, rx, "a.go"); got != "GetName\nreadname\nGETNAME\n" {
		t.Errorf("regex file = %q", got)
	}
}
