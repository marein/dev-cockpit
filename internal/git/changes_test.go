package git

import (
	"context"
	"testing"
)

func TestParseNumstatKeysRenamesByTheirTarget(t *testing.T) {
	// A plain entry carries its path behind the two numbers; a rename leaves
	// that field empty and follows with the source and the target. A binary file
	// carries a dash where a number would be.
	out := "2\t1\tplain.txt\x00" +
		"5\t3\t\x00old.txt\x00new.txt\x00" +
		"-\t-\timage.png\x00"

	counts := parseNumstat([]byte(out))

	if len(counts) != 3 {
		t.Fatalf("counts: %+v", counts)
	}
	if got := counts["plain.txt"]; got.added != 2 || got.removed != 1 || got.binary {
		t.Fatalf("plain: %+v", got)
	}
	if got := counts["new.txt"]; got.added != 5 || got.removed != 3 {
		t.Fatalf("rename target: %+v", got)
	}
	if _, ok := counts["old.txt"]; ok {
		t.Fatal("a rename source must not carry the counts, the target does")
	}
	if got := counts["image.png"]; !got.binary {
		t.Fatalf("binary: %+v", got)
	}
}

func TestChangesWithoutRepositoryAreEmptyAndNoError(t *testing.T) {
	changes, err := New(t.TempDir()).Changes(context.Background())

	if err != nil {
		t.Fatalf("a directory without a repository must not error: %v", err)
	}
	if changes.Repo {
		t.Fatal("reported as a repository")
	}
	if changes.Worktree == nil {
		t.Fatal("the list must be empty, never null")
	}
}
