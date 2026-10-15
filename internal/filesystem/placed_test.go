package filesystem

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A path an index still holds is placed only while it is a regular file under
// the root: one deleted since, one that became a folder, a symlink, one that
// walks out and one reached through a linked folder that leads outside are
// all left out, and the rest keeps the order it came in.
func TestFilesUnderPlacesOnlyFilesThatStandUnderTheRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	for _, dir := range []string{"src", "became"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(root, "src", "main.go"), filepath.Join(root, "README.md"), filepath.Join(outside, "secret.txt")} {
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

	got := FilesUnder(root, []string{"src/main.go", "gone.txt", "became", "link.md", "../etc/passwd", "out/secret.txt", "README.md"})
	want := []PlacedFile{
		{Rel: "src/main.go", Abs: filepath.Join(root, "src", "main.go")},
		{Rel: "README.md", Abs: filepath.Join(root, "README.md")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("placed %v, want %v", got, want)
	}
}
