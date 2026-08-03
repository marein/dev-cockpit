package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// record builds one NUL terminated porcelain v2 record, the way git writes it
// with -z.
func record(parts ...string) string {
	return strings.Join(parts, "\x00") + "\x00"
}

// runGit drives a real repository for the few tests that need one. Without git
// on the machine they skip rather than fail: this package is a reader of the
// binary, so its absence is a missing tool and not a broken build.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func writeAt(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestParseStatusEntries(t *testing.T) {
	// A worktree change, a staged add, a staged delete, an unmerged path and an
	// untracked file, in the shape git writes them. A header line is ignored,
	// git writes them in front of the entries when asked for the branch.
	out := record(
		"# branch.head master",
		"1 .M N... 100644 100644 100644 aaaa bbbb internal/git/git.go",
		"1 A. N... 000000 100644 100644 0000 cccc docs/new file.md",
		"1 D. N... 100644 000000 000000 dddd 0000 old.txt",
		"u UU N... 100644 100644 100644 100644 eeee ffff 1111 conflict.txt",
		"? scratch/notes.md",
	)

	files := parseStatus([]byte(out))

	if len(files) != 5 {
		t.Fatalf("files: %d (%v)", len(files), files)
	}
	want := []FileStatus{
		{Path: "internal/git/git.go", Index: ".", Worktree: "M"},
		{Path: "docs/new file.md", Index: "A", Worktree: "."},
		{Path: "old.txt", Index: "D", Worktree: "."},
		{Path: "conflict.txt", Index: "U", Worktree: "U"},
		{Path: "scratch/notes.md", Index: ".", Worktree: "?"},
	}
	for i, w := range want {
		if files[i] != w {
			t.Fatalf("file %d: got %+v, want %+v", i, files[i], w)
		}
	}
}

func TestParseStatusRenameCarriesItsSource(t *testing.T) {
	// A rename writes its target path first and its source as the next record.
	// The entry after it proves the source is consumed and not read as an entry
	// of its own.
	out := record(
		"2 R. N... 100644 100644 100644 aaaa bbbb R100 docs/Übersicht neu.md",
		"docs/Übersicht alt.md",
		"1 .M N... 100644 100644 100644 cccc dddd README.md",
	)

	files := parseStatus([]byte(out))

	if len(files) != 2 {
		t.Fatalf("files: %d (%v)", len(files), files)
	}
	if files[0].Path != "docs/Übersicht neu.md" {
		t.Fatalf("rename target: %q", files[0].Path)
	}
	if files[0].From != "docs/Übersicht alt.md" {
		t.Fatalf("rename source: %q", files[0].From)
	}
	if files[0].Index != "R" || files[0].Worktree != "." {
		t.Fatalf("rename codes: %q/%q", files[0].Index, files[0].Worktree)
	}
	if files[1].Path != "README.md" || files[1].From != "" {
		t.Fatalf("entry after the rename: %+v", files[1])
	}
}

func TestParseStatusSkipsBrokenRecords(t *testing.T) {
	// A truncated entry (the output cap hit, or a format nobody expected) drops
	// out instead of turning into a file with a nonsense path.
	out := record("1 .M N... 100644", "? kept.txt")

	files := parseStatus([]byte(out))

	if len(files) != 1 || files[0].Path != "kept.txt" {
		t.Fatalf("files: %+v", files)
	}
}

func TestWithinPrefixCutsToTheProject(t *testing.T) {
	files := []FileStatus{
		{Path: "app/main.go"},
		{Path: "app/sub/new.go", From: "app/sub/old.go"},
		{Path: "other/thing.go"},
		{Path: "app/moved.go", From: "outside/moved.go"},
	}

	kept := withinPrefix(files, "app/")

	if len(kept) != 3 {
		t.Fatalf("kept: %+v", kept)
	}
	if kept[0].Path != "main.go" {
		t.Fatalf("path: %q", kept[0].Path)
	}
	if kept[1].Path != "sub/new.go" || kept[1].From != "sub/old.go" {
		t.Fatalf("rename: %+v", kept[1])
	}
	// The source of that last one lies outside the project, so it has no path
	// the project could show. Handing the repository relative one out would
	// read as a project path and point at a file that is not there.
	if kept[2].Path != "moved.go" || kept[2].From != "" {
		t.Fatalf("rename from outside the project: %+v", kept[2])
	}
}

// A project below the repository root is where the two git calls disagree: both
// report repository relative paths, but the status paths are cut back to the
// project before the numbers are looked up. Keying the lookup with the short
// path finds nothing, or the numbers of a same named file at the root.
func TestChangesCountsLinesInASubdirectoryProject(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	writeAt(t, root, "a.txt", "root one\nroot two\n")
	writeAt(t, root, "app/a.txt", "one\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-qm", "init")
	writeAt(t, root, "a.txt", "root one\nroot two\nroot three\nroot four\n")
	writeAt(t, root, "app/a.txt", "one\ntwo\n")

	changes, err := New(filepath.Join(root, "app")).Changes(context.Background())
	if err != nil {
		t.Fatalf("changes: %v", err)
	}

	if len(changes.Worktree) != 1 {
		t.Fatalf("worktree: %+v", changes.Worktree)
	}
	entry := changes.Worktree[0]
	if entry.Path != "a.txt" {
		t.Fatalf("path: %q", entry.Path)
	}
	if entry.Added != 1 || entry.Removed != 0 {
		t.Fatalf("the project's own file did not get its own numbers: %+v", entry)
	}
}

func TestFingerprintWithoutRepositoryIsEmpty(t *testing.T) {
	fingerprint, ok := New(t.TempDir()).Fingerprint(context.Background())

	// A directory that is no repository is a state, not a failure: the answer
	// is empty and the caller is told it may trust it, so it stays put between
	// rounds instead of reading as a change every time.
	if !ok {
		t.Fatal("a directory without a repository must be an answer, not a failure")
	}
	if fingerprint.Base != "" || fingerprint.Worktree != "" {
		t.Fatalf("fingerprint: %+v", fingerprint)
	}
	if fingerprint.Moved(Fingerprint{}) {
		t.Fatal("two empty fingerprints must not read as moved")
	}
}

// The two parts exist so a caller can tell a save in the working copy from a
// commit that moved HEAD. A fingerprint that moved both parts at once would
// make that distinction useless.
func TestFingerprintMovedComparesBothParts(t *testing.T) {
	base := Fingerprint{Base: "a", Worktree: "b"}

	if base.Moved(base) {
		t.Fatal("the same fingerprint reads as moved")
	}
	if !base.Moved(Fingerprint{Base: "a", Worktree: "c"}) {
		t.Fatal("a moved working copy is not reported")
	}
	if !base.Moved(Fingerprint{Base: "z", Worktree: "b"}) {
		t.Fatal("a moved base is not reported")
	}
}
