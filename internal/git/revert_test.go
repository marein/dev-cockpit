package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readAt(t *testing.T, root, rel string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(content)
}

func TestRevertTakesAModifiedFileBackToHead(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "head\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	writeAt(t, dir, "a.txt", "staged\n")
	runGit(t, dir, "add", "a.txt")
	writeAt(t, dir, "a.txt", "typed on top\n")

	if err := New(dir).Revert(context.Background(), "a.txt"); err != nil {
		t.Fatalf("revert: %v", err)
	}

	if got := readAt(t, dir, "a.txt"); got != "head\n" {
		t.Fatalf("content: %q", got)
	}
	if left := worktreeByPath(t, dir); len(left) != 0 {
		t.Fatalf("one revert must leave the file clean, staged edits included: %+v", left)
	}
}

func TestRevertDeletesAnUntrackedFile(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "kept.txt", "kept\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	writeAt(t, dir, "loose.txt", "no HEAD state\n")

	if err := New(dir).Revert(context.Background(), "loose.txt"); err != nil {
		t.Fatalf("revert: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "loose.txt")); !os.IsNotExist(err) {
		t.Fatalf("an untracked file has no HEAD state, reverting deletes it: %v", err)
	}
	if got := readAt(t, dir, "kept.txt"); got != "kept\n" {
		t.Fatalf("kept.txt: %q", got)
	}
}

func TestRevertBringsADeletedFileBack(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "head\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if err := New(dir).Revert(context.Background(), "a.txt"); err != nil {
		t.Fatalf("revert: %v", err)
	}

	if got := readAt(t, dir, "a.txt"); got != "head\n" {
		t.Fatalf("content: %q", got)
	}
}

func TestRevertDeletesAStagedAddition(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "kept.txt", "kept\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	writeAt(t, dir, "fresh.txt", "staged, no HEAD state\n")
	runGit(t, dir, "add", "fresh.txt")

	if err := New(dir).Revert(context.Background(), "fresh.txt"); err != nil {
		t.Fatalf("revert: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "fresh.txt")); !os.IsNotExist(err) {
		t.Fatalf("a staged addition has no HEAD state, reverting deletes it: %v", err)
	}
	if left := worktreeByPath(t, dir); len(left) != 0 {
		t.Fatalf("nothing may stay staged: %+v", left)
	}
}

func TestRevertOfADirectoryRestoresAndDeletesRecursively(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "sub/tracked.txt", "head\n")
	writeAt(t, dir, "sub/gone.txt", "head\n")
	writeAt(t, dir, "outside.txt", "head\n")
	writeAt(t, dir, ".gitignore", "sub/ignored.txt\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	writeAt(t, dir, "sub/tracked.txt", "changed\n")
	if err := os.Remove(filepath.Join(dir, "sub/gone.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	writeAt(t, dir, "sub/loose.txt", "untracked\n")
	writeAt(t, dir, "sub/deep/nested.txt", "untracked folder\n")
	writeAt(t, dir, "sub/ignored.txt", "ignored\n")
	writeAt(t, dir, "outside.txt", "outside changed\n")

	if err := New(dir).Revert(context.Background(), "sub"); err != nil {
		t.Fatalf("revert: %v", err)
	}

	if got := readAt(t, dir, "sub/tracked.txt"); got != "head\n" {
		t.Fatalf("tracked: %q", got)
	}
	if got := readAt(t, dir, "sub/gone.txt"); got != "head\n" {
		t.Fatalf("the deleted file must come back: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub/loose.txt")); !os.IsNotExist(err) {
		t.Fatalf("untracked file under the path must go: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub/deep")); !os.IsNotExist(err) {
		t.Fatalf("an untracked folder under the path must go with its files: %v", err)
	}
	if got := readAt(t, dir, "sub/ignored.txt"); got != "ignored\n" {
		t.Fatalf("an ignored file is no change and must stay: %q", got)
	}
	if got := readAt(t, dir, "outside.txt"); got != "outside changed\n" {
		t.Fatalf("a change outside the path must stay: %q", got)
	}
}

func TestRevertTakesARenameBackWithItsSource(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "old.txt", "same content, long enough to pair\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	runGit(t, dir, "mv", "old.txt", "new.txt")

	if err := New(dir).Revert(context.Background(), "new.txt"); err != nil {
		t.Fatalf("revert: %v", err)
	}

	if got := readAt(t, dir, "old.txt"); got != "same content, long enough to pair\n" {
		t.Fatalf("the source must come back: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("the target has no HEAD state and must go: %v", err)
	}
	if left := worktreeByPath(t, dir); len(left) != 0 {
		t.Fatalf("half a rename may not stay pending: %+v", left)
	}
}

func TestRevertInASubdirectoryProjectStaysInsideIt(t *testing.T) {
	root := t.TempDir()
	commitRepo(t, root)
	writeAt(t, root, "outside.txt", "head\n")
	writeAt(t, root, "app/inner.txt", "head\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-qm", "init")
	writeAt(t, root, "outside.txt", "outside changed\n")
	writeAt(t, root, "app/inner.txt", "inner changed\n")

	if err := New(root+"/app").Revert(context.Background(), "inner.txt"); err != nil {
		t.Fatalf("revert: %v", err)
	}

	if got := readAt(t, root, "app/inner.txt"); got != "head\n" {
		t.Fatalf("inner: %q", got)
	}
	if got := readAt(t, root, "outside.txt"); got != "outside changed\n" {
		t.Fatalf("a file outside the project must stay a change: %q", got)
	}
}

func TestRevertRefusesWithoutACommitAndWithoutARepository(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "only-copy.txt", "staged\n")
	runGit(t, dir, "add", "-A")

	err := New(dir).Revert(context.Background(), "only-copy.txt")

	if err == nil || !strings.Contains(err.Error(), "no commit yet") {
		t.Fatalf("an unborn repository has no state to revert to: %v", err)
	}
	if got := readAt(t, dir, "only-copy.txt"); got != "staged\n" {
		t.Fatalf("a refused revert must leave the file: %q", got)
	}

	if err := New(t.TempDir()).Revert(context.Background(), "a.txt"); err == nil {
		t.Fatal("a directory without a repository cannot revert")
	}

	if err := New(dir).Revert(context.Background(), ""); err == nil {
		t.Fatal("an empty path must be refused")
	}
}
