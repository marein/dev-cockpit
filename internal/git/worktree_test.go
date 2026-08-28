package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// worktreeRepo is a repository with one commit on master and a second branch,
// the shape every check here starts from.
func worktreeRepo(t *testing.T, dir string) {
	t.Helper()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	runGit(t, dir, "branch", "feature")
}

func TestAddWorktreeChecksOutAnExistingBranch(t *testing.T) {
	main := t.TempDir()
	worktreeRepo(t, main)
	dir := filepath.Join(t.TempDir(), "linked")
	if err := New(main).AddWorktree(context.Background(), NewWorktree{Dir: dir, Branch: "feature"}); err != nil {
		t.Fatalf("add worktree: %v", err)
	}
	if got := currentBranch(t, dir); got != "feature" {
		t.Fatalf("the worktree stands on %q, want feature", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatalf("the worktree carries no working copy: %v", err)
	}
	if got := currentBranch(t, main); got != "master" {
		t.Fatalf("the main moved to %q", got)
	}
}

// A directory the caller already made, which is what the project creation
// hands over, is filled and not refused.
func TestAddWorktreeFillsAnEmptyDirectory(t *testing.T) {
	main := t.TempDir()
	worktreeRepo(t, main)
	dir := filepath.Join(t.TempDir(), "linked")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := New(main).AddWorktree(context.Background(), NewWorktree{Dir: dir, Branch: "feature"}); err != nil {
		t.Fatalf("add worktree into an empty directory: %v", err)
	}
	if got := currentBranch(t, dir); got != "feature" {
		t.Fatalf("the worktree stands on %q, want feature", got)
	}
}

func TestAddWorktreeCreatesTheBranchAtItsStart(t *testing.T) {
	main := t.TempDir()
	worktreeRepo(t, main)
	dir := filepath.Join(t.TempDir(), "linked")
	if err := New(main).AddWorktree(context.Background(), NewWorktree{Dir: dir, Branch: "fresh", Start: "master"}); err != nil {
		t.Fatalf("add worktree: %v", err)
	}
	if got := currentBranch(t, dir); got != "fresh" {
		t.Fatalf("the worktree stands on %q, want fresh", got)
	}
	if got := currentBranch(t, main); got != "master" {
		t.Fatalf("the main moved to %q", got)
	}
}

// The branch of a worktree is not free for a second one, and git says so.
// Nothing is left behind, the directory it would have filled stays untouched.
func TestAddWorktreeRefusesABranchThatIsAlreadyCheckedOut(t *testing.T) {
	main := t.TempDir()
	worktreeRepo(t, main)
	first := filepath.Join(t.TempDir(), "first")
	if err := New(main).AddWorktree(context.Background(), NewWorktree{Dir: first, Branch: "feature"}); err != nil {
		t.Fatalf("add worktree: %v", err)
	}
	second := filepath.Join(t.TempDir(), "second")
	err := New(main).AddWorktree(context.Background(), NewWorktree{Dir: second, Branch: "feature"})
	if err == nil {
		t.Fatal("a branch was checked out twice")
	}
	if !strings.Contains(err.Error(), "already used by worktree") {
		t.Fatalf("the refusal is not git's own: %v", err)
	}
	if _, statErr := os.Stat(second); !os.IsNotExist(statErr) {
		t.Fatalf("the refused worktree left a directory: %v", statErr)
	}
}

// A worktree added from inside a worktree belongs to the main repository, so
// the cockpit's own registration reading, which only ever looks at the main,
// finds all three.
func TestAddWorktreeFromAWorktreeRegistersInTheMain(t *testing.T) {
	main := t.TempDir()
	worktreeRepo(t, main)
	first := filepath.Join(t.TempDir(), "first")
	if err := New(main).AddWorktree(context.Background(), NewWorktree{Dir: first, Branch: "feature"}); err != nil {
		t.Fatalf("add worktree: %v", err)
	}
	second := filepath.Join(t.TempDir(), "second")
	if err := New(first).AddWorktree(context.Background(), NewWorktree{Dir: second, Branch: "sibling", Start: "master"}); err != nil {
		t.Fatalf("add worktree from a worktree: %v", err)
	}
	list, err := New(main).Worktrees(context.Background())
	if err != nil {
		t.Fatalf("worktrees: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("the main knows %d working copies, want 3: %+v", len(list), list)
	}
}

func TestWorktreesNameTheBranchOfEachWorkingCopy(t *testing.T) {
	main := t.TempDir()
	worktreeRepo(t, main)
	linked := filepath.Join(t.TempDir(), "linked")
	if err := New(main).AddWorktree(context.Background(), NewWorktree{Dir: linked, Branch: "feature"}); err != nil {
		t.Fatalf("add worktree: %v", err)
	}
	detached := filepath.Join(t.TempDir(), "detached")
	runGit(t, main, "worktree", "add", "--detach", detached)

	list, err := New(main).Worktrees(context.Background())
	if err != nil {
		t.Fatalf("worktrees: %v", err)
	}
	taken := map[string]string{}
	for _, w := range list {
		taken[filepath.Base(w.Path)] = w.Branch
	}
	if taken[filepath.Base(main)] != "master" {
		t.Fatalf("the main stands on %q, want master", taken[filepath.Base(main)])
	}
	if taken["linked"] != "feature" {
		t.Fatalf("the linked worktree stands on %q, want feature", taken["linked"])
	}
	if branch, ok := taken["detached"]; !ok || branch != "" {
		t.Fatalf("a detached working copy names a branch: %q %v", branch, ok)
	}
}

func TestWorktreesOfADirectoryWithoutRepositoryAreEmpty(t *testing.T) {
	list, err := New(t.TempDir()).Worktrees(context.Background())
	if err != nil {
		t.Fatalf("worktrees without a repository = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("a directory without a repository answered %+v", list)
	}
}

func TestAddWorktreeRefusesArgumentsThatReadAsOptions(t *testing.T) {
	main := t.TempDir()
	worktreeRepo(t, main)
	dir := filepath.Join(t.TempDir(), "linked")
	if err := New(main).AddWorktree(context.Background(), NewWorktree{Dir: dir, Branch: "-f"}); err == nil {
		t.Fatal("a branch that reads as an option was accepted")
	}
	if err := New(main).AddWorktree(context.Background(), NewWorktree{Dir: dir, Branch: "ok", Start: "--force"}); err == nil {
		t.Fatal("a starting point that reads as an option was accepted")
	}
	if err := New(main).AddWorktree(context.Background(), NewWorktree{Dir: "relative", Branch: "ok"}); err == nil {
		t.Fatal("a relative directory was accepted")
	}
}
