package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	out, err := New(dir).run(context.Background(), []string{"symbolic-ref", "--short", "HEAD"}, nil)
	if err != nil {
		t.Fatalf("symbolic-ref: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestCreateBranchStartsAtHeadAndSwitches(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	head := headOf(t, dir)

	if err := New(dir).CreateBranch(context.Background(), "feature/x"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := currentBranch(t, dir); got != "feature/x" {
		t.Fatalf("branch: %s", got)
	}
	if got := headOf(t, dir); got != head {
		t.Fatalf("a new branch must not move HEAD's commit: %s", got)
	}
	if err := New(dir).CreateBranch(context.Background(), "feature/x"); err == nil {
		t.Fatal("a taken name must be refused in git's words")
	}
}

func TestCheckoutSwitchesBetweenLocalBranches(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	was := currentBranch(t, dir)
	runGit(t, dir, "switch", "-qc", "other")

	if err := New(dir).Checkout(context.Background(), was); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if got := currentBranch(t, dir); got != was {
		t.Fatalf("branch: %s", got)
	}
}

func TestCheckoutOfARemoteBranchCreatesTheTrackingBranch(t *testing.T) {
	work, remote := remotePair(t)
	other := cloneOf(t, remote)
	runGit(t, other, "switch", "-qc", "feature")
	writeAt(t, other, "f.txt", "f\n")
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-qm", "feature work")
	runGit(t, other, "push", "-q", "-u", "origin", "feature")

	if ran, err := New(work).Fetch(context.Background()); err != nil || !ran {
		t.Fatalf("fetch: ran=%v err=%v", ran, err)
	}
	if err := New(work).Checkout(context.Background(), "feature"); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if got := currentBranch(t, work); got != "feature" {
		t.Fatalf("branch: %s", got)
	}
	changes, err := New(work).Changes(context.Background())
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if changes.Branch.Upstream != "origin/feature" {
		t.Fatalf("the checkout must set the tracking branch: %+v", changes.Branch)
	}
	if _, err := os.Stat(filepath.Join(work, "f.txt")); err != nil {
		t.Fatalf("the branch's file must be in the working copy: %v", err)
	}
}

func TestCheckoutRefusalLeavesTheWorkingCopyAlone(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "first\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	was := currentBranch(t, dir)
	runGit(t, dir, "switch", "-qc", "other")
	writeAt(t, dir, "a.txt", "other\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "other words")
	writeAt(t, dir, "a.txt", "local, not committed\n")

	if err := New(dir).Checkout(context.Background(), was); err == nil {
		t.Fatal("a switch that would overwrite local changes must be refused")
	}
	if got := currentBranch(t, dir); got != "other" {
		t.Fatalf("the refused switch moved the branch: %s", got)
	}
	content, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil || string(content) != "local, not committed\n" {
		t.Fatalf("the local change must stand untouched: %q %v", content, err)
	}
}

func TestBranchNamesThatReadAsOptionsAreRefused(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	for _, name := range []string{"", "   ", "--force"} {
		if err := New(dir).Checkout(context.Background(), name); err == nil {
			t.Fatalf("checkout must refuse %q", name)
		}
		if err := New(dir).CreateBranch(context.Background(), name); err == nil {
			t.Fatalf("create must refuse %q", name)
		}
	}
}
