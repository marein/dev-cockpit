package git

import (
	"context"
	"errors"
	"testing"
)

func TestFileAtReadsARevisionAndDefaultsToHead(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "old\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "first")
	writeAt(t, dir, "a.txt", "new\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "second")

	old, exists, err := New(dir).FileAt(context.Background(), "HEAD~1", "a.txt")
	if err != nil || !exists || string(old) != "old\n" {
		t.Fatalf("HEAD~1: %q %v %v", old, exists, err)
	}
	head, exists, err := New(dir).FileAt(context.Background(), "", "a.txt")
	if err != nil || !exists || string(head) != "new\n" {
		t.Fatalf("empty rev is HEAD: %q %v %v", head, exists, err)
	}
	if _, exists, err := New(dir).FileAt(context.Background(), "HEAD~1", "later.txt"); err != nil || exists {
		t.Fatalf("a file the revision does not hold: %v %v", exists, err)
	}
}

func TestFileAtRefusesAnUnknownRevision(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")

	for _, rev := range []string{"no-such-branch", "--force", "a b"} {
		if _, _, err := New(dir).FileAt(context.Background(), rev, "a.txt"); !errors.Is(err, ErrRevision) {
			t.Fatalf("%q must answer ErrRevision, got %v", rev, err)
		}
	}
}

// A name git accepts has to reach git. The editor builds branch names out of
// \w, so it creates names with a leading underscore itself, and a pattern that
// keeps options out must not refuse them on the way back in.
func TestFileAtTakesTheNamesGitTakes(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "one\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	runGit(t, dir, "switch", "-q", "-c", "_wip")
	runGit(t, dir, "check-ref-format", "--branch", "_wip")

	content, exists, err := New(dir).FileAt(context.Background(), "_wip", "a.txt")
	if err != nil || !exists || string(content) != "one\n" {
		t.Fatalf("a legal branch name was refused: %q %v %v", content, exists, err)
	}
}
