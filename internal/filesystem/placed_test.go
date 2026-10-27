package filesystem

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// An entry an index still holds is placed only while it is under the root
// what it was listed as: a file deleted since, one that became a folder, a
// folder that became a file, a symlink, one that walks out and one reached
// through a linked folder that leads outside are all left out, and the rest
// keeps the order it came in.
func TestEntriesUnderPlacesOnlyWhatStandsUnderTheRootAsListed(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	for _, dir := range []string{"src", "became"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(root, "src", "main.go"), filepath.Join(root, "README.md"), filepath.Join(root, "flat"), filepath.Join(outside, "secret.txt")} {
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "README.md"), filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}

	got := EntriesUnder(root, []QuickOpenEntry{
		{Path: "src/main.go"}, {Path: "gone.txt"}, {Path: "became"}, {Path: "link.md"}, {Path: "../etc/passwd"},
		{Path: "out/secret.txt"}, {Path: "src", Folder: true}, {Path: "flat", Folder: true}, {Path: "linked", Folder: true},
		{Path: "out", Folder: true}, {Path: "gone", Folder: true}, {Path: "README.md"},
	})
	want := []PlacedEntry{
		{Rel: "src/main.go", Abs: filepath.Join(root, "src", "main.go")},
		{Rel: "src", Abs: filepath.Join(root, "src"), Folder: true},
		{Rel: "README.md", Abs: filepath.Join(root, "README.md")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("placed %v, want %v", got, want)
	}
}
