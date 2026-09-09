package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mustMkdir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
}

func exists(t *testing.T, root, rel string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// TestCopyReplacesAFolderWithEverythingBelowIt is the answer the overwrite
// dialog promises: a folder is replaced the way a file is, whole, so what only
// the old one held is gone and what the new one holds is there, however deep.
func TestCopyReplacesAFolderWithEverythingBelowIt(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/lib/new.txt", "new")
	writeFile(t, root, "src/lib/deep/inner.txt", "inner")
	writeFile(t, root, "dst/lib/old.txt", "old")

	entry, err := CopyEntry(root, "src/lib", "dst", true)
	if err != nil {
		t.Fatal(err)
	}
	if entry.RelPath != "dst/lib" || !entry.IsDir {
		t.Fatalf("entry = %+v, want the folder at dst/lib", entry)
	}
	if got := readBack(t, root, "dst/lib/new.txt"); got != "new" {
		t.Errorf("dst/lib/new.txt = %q, the copy did not arrive", got)
	}
	if got := readBack(t, root, "dst/lib/deep/inner.txt"); got != "inner" {
		t.Errorf("dst/lib/deep/inner.txt = %q, the copy stopped at the top level", got)
	}
	if exists(t, root, "dst/lib/old.txt") {
		t.Error("the replaced folder kept a file of its own, that is a merge and not a replacement")
	}
	if !exists(t, root, "src/lib/new.txt") {
		t.Error("the source was touched, a copy must leave it alone")
	}
}

// TestMoveReplacesAFolderWithEverythingBelowIt is the same answer for the drag
// and drop half: the moved folder stands at the target and its old place is
// empty.
func TestMoveReplacesAFolderWithEverythingBelowIt(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/lib/new.txt", "new")
	writeFile(t, root, "dst/lib/old.txt", "old")

	entry, err := MoveEntry(root, "src/lib", "dst", true)
	if err != nil {
		t.Fatal(err)
	}
	if entry.RelPath != "dst/lib" || !entry.IsDir {
		t.Fatalf("entry = %+v, want the folder at dst/lib", entry)
	}
	if got := readBack(t, root, "dst/lib/new.txt"); got != "new" {
		t.Errorf("dst/lib/new.txt = %q, the move did not arrive", got)
	}
	if exists(t, root, "dst/lib/old.txt") {
		t.Error("the replaced folder kept a file of its own")
	}
	if exists(t, root, "src/lib") {
		t.Error("the source folder is still there, that is a copy and not a move")
	}
}

// TestReplaceAcrossKinds covers the two mixed cases rename refuses on its own: a
// folder onto a taken file name and a file onto a taken folder name.
func TestReplaceAcrossKinds(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/thing/inside.txt", "folder content")
	writeFile(t, root, "dst/thing", "file content")
	writeFile(t, root, "other/name.txt", "plain file")
	mustMkdir(t, root, "target/name.txt")

	if _, err := CopyEntry(root, "src/thing", "dst", true); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, root, "dst/thing/inside.txt"); got != "folder content" {
		t.Errorf("dst/thing/inside.txt = %q, a folder did not replace the file", got)
	}

	if _, err := MoveEntry(root, "other/name.txt", "target", true); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, root, "target/name.txt"); got != "plain file" {
		t.Errorf("target/name.txt = %q, a file did not replace the folder", got)
	}
}

// TestReplaceRefusesWhatWouldTakeTheSourceWithIt is the one case a replacement
// cannot serve: the source sits inside the folder it would overwrite, so
// clearing that folder would delete the source first.
func TestReplaceRefusesWhatWouldTakeTheSourceWithIt(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "lib/nested/lib/kept.txt", "kept")

	if _, err := CopyEntry(root, "lib/nested/lib", "", true); err == nil {
		t.Error("the copy replaced a folder holding its own source")
	}
	if _, err := MoveEntry(root, "lib/nested/lib", "", true); err == nil {
		t.Error("the move replaced a folder holding its own source")
	}
	if got := readBack(t, root, "lib/nested/lib/kept.txt"); got != "kept" {
		t.Errorf("lib/nested/lib/kept.txt = %q, the refusal still took something", got)
	}
}

// TestTakenFolderNameWithoutOverwriteStaysAQuestion keeps the 409 the browser
// asks its question on: without overwrite nothing is written, for a folder just
// as for a file.
func TestTakenFolderNameWithoutOverwriteStaysAQuestion(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/lib/new.txt", "new")
	writeFile(t, root, "dst/lib/old.txt", "old")

	if _, err := CopyEntry(root, "src/lib", "dst", false); !errors.Is(err, ErrExists) {
		t.Errorf("copy error = %v, want ErrExists", err)
	}
	if _, err := MoveEntry(root, "src/lib", "dst", false); !errors.Is(err, ErrExists) {
		t.Errorf("move error = %v, want ErrExists", err)
	}
	if got := readBack(t, root, "dst/lib/old.txt"); got != "old" {
		t.Errorf("dst/lib/old.txt = %q, a refused write still changed something", got)
	}
}

// TestReplacementLeavesNoStagingBehind guards the swap itself: what it builds
// beside the target is gone by the time the caller sees the entry, so the tree
// never shows the half of a replacement.
func TestReplacementLeavesNoStagingBehind(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/lib/new.txt", "new")
	writeFile(t, root, "dst/lib/old.txt", "old")

	if _, err := CopyEntry(root, "src/lib", "dst", true); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "dst"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "lib" {
			t.Errorf("dst holds %q, the replacement left something behind", entry.Name())
		}
	}
}
