package proctree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenFilesNamesAFileTheProcessHolds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.txt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, open := range OpenFiles(os.Getpid()) {
		if open == path || open == resolved {
			return
		}
	}
	t.Fatalf("OpenFiles did not name %s", path)
}

func TestOpenFilesOfAMissingProcessIsEmpty(t *testing.T) {
	if got := OpenFiles(1 << 30); len(got) != 0 {
		t.Fatalf("got %v, want nothing", got)
	}
}
