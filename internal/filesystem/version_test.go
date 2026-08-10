package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeDisk puts content on the disk behind the editor's back, the way a coder
// or git does it.
func writeDisk(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readDisk(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWriteFileTextIfUnchangedWritesOnTheVersionItRead(t *testing.T) {
	root := t.TempDir()
	writeDisk(t, root, "a.txt", "one\n")
	_, version, err := ReadFileText(root, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, next, err := WriteFileTextIfUnchanged(root, "a.txt", []byte("two\n"), version)
	if err != nil {
		t.Fatalf("a save on the version it read was refused: %v", err)
	}
	if got := readDisk(t, root, "a.txt"); got != "two\n" {
		t.Fatalf("the file holds %q", got)
	}
	if next == version {
		t.Fatal("the save answered the version it was given")
	}
	// Twice in a row without asking anything: the answer of the first save is
	// what the second one carries.
	if _, _, err := WriteFileTextIfUnchanged(root, "a.txt", []byte("three\n"), next); err != nil {
		t.Fatalf("the second save was refused: %v", err)
	}
	if got := readDisk(t, root, "a.txt"); got != "three\n" {
		t.Fatalf("the second save left %q", got)
	}
}

// The case mtime plus size cannot see: one byte swapped for another, so the
// size stands, and the timestamp put back to what it was, so a stat says
// nothing moved. The token is over the content, so it moved.
func TestWriteFileTextIfUnchangedSeesAChangeAStatCannot(t *testing.T) {
	root := t.TempDir()
	writeDisk(t, root, "a.txt", "aaaa")
	_, version, err := ReadFileText(root, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	writeDisk(t, root, "a.txt", "aaba")
	if err := os.Chtimes(filepath.Join(root, "a.txt"), before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// The premise of the test, not the assertion: size and mtime really are the
	// ones the version was taken with.
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("the setup moved the stat: %d/%v against %d/%v",
			after.Size(), after.ModTime(), before.Size(), before.ModTime())
	}
	_, _, err = WriteFileTextIfUnchanged(root, "a.txt", []byte("mine"), version)
	if !errors.Is(err, ErrFileChanged) {
		t.Fatalf("a changed file of the same size and mtime was written: %v", err)
	}
	if got := readDisk(t, root, "a.txt"); got != "aaba" {
		t.Fatalf("the refused write touched the file: %q", got)
	}
}

// The other way round: identical content written again, timestamps fresh, which
// is what a git checkout does. Nothing changed, so nothing is refused and
// nobody is asked a question about a conflict that does not exist.
func TestWriteFileTextIfUnchangedIgnoresARewriteOfTheSameContent(t *testing.T) {
	root := t.TempDir()
	writeDisk(t, root, "a.txt", "same\n")
	_, version, err := ReadFileText(root, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	writeDisk(t, root, "a.txt", "same\n")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, "a.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteFileTextIfUnchanged(root, "a.txt", []byte("mine\n"), version); err != nil {
		t.Fatalf("a rewrite of the same content was taken for a conflict: %v", err)
	}
	if got := readDisk(t, root, "a.txt"); got != "mine\n" {
		t.Fatalf("the file holds %q", got)
	}
}

func TestWriteFileTextIfUnchangedRefusesADeletedFile(t *testing.T) {
	root := t.TempDir()
	writeDisk(t, root, "a.txt", "one\n")
	_, version, err := ReadFileText(root, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "a.txt")); err != nil {
		t.Fatal(err)
	}
	_, _, err = WriteFileTextIfUnchanged(root, "a.txt", []byte("mine\n"), version)
	if !errors.Is(err, ErrFileDeleted) {
		t.Fatalf("a deleted file answered %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the refused write put the deleted file back")
	}
	// And the way out of that dialog, a save without a version, writes it as a
	// new file and answers a version the next save can carry.
	_, next, err := WriteFileTextIfUnchanged(root, "a.txt", []byte("mine\n"), "")
	if err != nil {
		t.Fatalf("the create path was refused: %v", err)
	}
	if got := readDisk(t, root, "a.txt"); got != "mine\n" {
		t.Fatalf("the recreated file holds %q", got)
	}
	if _, _, err := WriteFileTextIfUnchanged(root, "a.txt", []byte("again\n"), next); err != nil {
		t.Fatalf("the version the create path answered was refused: %v", err)
	}
}

func TestReadFileTextAnswersTheVersionOfWhatItRead(t *testing.T) {
	root := t.TempDir()
	writeDisk(t, root, "a.txt", "one\n")
	content, version, err := ReadFileText(root, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if content != "one\n" || version == "" {
		t.Fatalf("read answered %q with version %q", content, version)
	}
	// An empty file has a version like every other one: only the absence of a
	// version means "nothing was read here", which is what the create path is.
	writeDisk(t, root, "empty.txt", "")
	_, emptyVersion, err := ReadFileText(root, "empty.txt")
	if err != nil {
		t.Fatal(err)
	}
	if emptyVersion == "" {
		t.Fatal("an empty file answered no version")
	}
	if emptyVersion == version {
		t.Fatal("two different files answered the same version")
	}
}
