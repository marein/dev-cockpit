package git

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestLastCommitReadsASignedCommitWithShowSignature(t *testing.T) {
	dir := t.TempDir()
	signedRepo(t, dir)
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "signed")
	want, err := strconv.ParseInt(strings.TrimSpace(gitOut(t, dir, "log", "-1", "--no-show-signature", "--format=%ct")), 10, 64)
	if err != nil {
		t.Fatalf("committer time: %v", err)
	}

	got, ok := New(dir).LastCommit(context.Background())
	if !ok || got.Unix() != want {
		t.Fatalf("LastCommit = %v, %v, want %d", got, ok, want)
	}
}
