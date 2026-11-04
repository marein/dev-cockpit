package git

import (
	"context"
	"testing"
)

func TestSinceComparesTheSplitWithTheWorkingCopy(t *testing.T) {
	dir := t.TempDir()
	compareRepo(t, dir)
	runGit(t, dir, "switch", "-q", "feature")
	writeAt(t, dir, "moved/later.txt", "a\nb\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "outside keep")
	writeAt(t, dir, "keep/added.txt", "new\nmore\n")
	runGit(t, dir, "add", "keep/added.txt")
	writeAt(t, dir, "keep/untracked.txt", "u\nv\n")
	repo := New(dir)

	got, err := repo.Since(context.Background())
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if !got.Repo || got.From.Name != "main" || got.To.Name != "feature" || got.To.SHA != gitOut(t, dir, "rev-parse", "feature") || got.Base != gitOut(t, dir, "merge-base", "main", "feature") {
		t.Fatalf("the sides and the split: %+v", got)
	}
	if got.Commits != 2 || got.Changed != 7 || got.Added != 8 || got.Removed != 3 || got.Truncated || len(got.Files) != 7 {
		t.Fatalf("the counts: %+v", got)
	}
	byPath := changesByPath(got.Files)
	if byPath["keep/added.txt"].Status != "A" || byPath["keep/added.txt"].Added != 2 || byPath["keep/untracked.txt"].Status != "?" || byPath["keep/untracked.txt"].Added != 2 {
		t.Fatalf("the committed and uncommitted changes of one file and an untracked one: %+v", got.Files)
	}

	commits, err := repo.Between(context.Background(), got.Base, got.To.SHA, 1)
	if err != nil || len(commits) != 1 || commits[0].Summary != "outside keep" || commits[0].Author != "t" {
		t.Fatalf("the newest commit alone: %v %+v", err, commits)
	}
}

func TestSinceOnMainMeasuresFromTheLastTag(t *testing.T) {
	dir := t.TempDir()
	compareRepo(t, dir)

	got, err := New(dir).Since(context.Background())
	if err != nil || got.From.Name != "v1" || got.To.Name != "main" || got.Base != got.From.SHA || got.Commits != 1 || got.Changed != 2 {
		t.Fatalf("main since its tag: %v %+v", err, got)
	}
}

func TestSinceOnMainMeasuresFromATagWithBuildMetadata(t *testing.T) {
	dir := t.TempDir()
	compareRepo(t, dir)
	runGit(t, dir, "tag", "-d", "v1")
	runGit(t, dir, "tag", "v1.2.3+build", "HEAD~1")

	got, err := New(dir).Since(context.Background())
	if err != nil || got.From.Name != "v1.2.3+build" || got.Commits != 1 || got.Changed != 2 {
		t.Fatalf("main since a tag git takes with a plus: %v %+v", err, got)
	}
}

func TestSinceStaysInsideASubdirectoryProject(t *testing.T) {
	dir := t.TempDir()
	compareRepo(t, dir)
	runGit(t, dir, "switch", "-q", "feature")
	writeAt(t, dir, "moved/later.txt", "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "outside keep")
	writeAt(t, dir, "moved/untracked.txt", "outside\n")
	writeAt(t, dir, "keep/untracked.txt", "inside\n")
	repo := New(dir + "/keep")

	got, err := repo.Since(context.Background())
	if err != nil || got.Commits != 1 || got.Changed != 6 || changesByPath(got.Files)["untracked.txt"].Status != "?" {
		t.Fatalf("the project's own commits and files: %v %+v", err, got)
	}
	commits, err := repo.Between(context.Background(), got.Base, got.To.SHA, 10)
	if err != nil || len(commits) != 1 || commits[0].Summary != "feature work" {
		t.Fatalf("the project's own history: %v %+v", err, commits)
	}
}

func TestSinceOfOneCommitOrNoRepositoryCountsNothing(t *testing.T) {
	dir := t.TempDir()
	if got, err := New(dir).Since(context.Background()); err != nil || got.Repo {
		t.Fatalf("no repository: %v %+v", err, got)
	}
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "one\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "first")
	got, err := New(dir).Since(context.Background())
	if err != nil || !got.Repo || got.From.SHA != got.To.SHA || got.Base != got.To.SHA || got.Commits != 0 || len(got.Files) != 0 {
		t.Fatalf("one commit compares with itself: %v %+v", err, got)
	}
	writeAt(t, dir, "a.txt", "one\ntwo\n")
	got, err = New(dir).Since(context.Background())
	if err != nil || got.Commits != 0 || got.Changed != 1 || got.Added != 1 {
		t.Fatalf("one commit compares with the working copy: %v %+v", err, got)
	}
}

func TestSinceWithoutSharedHistoryRefusesTheSplit(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	runGit(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	writeAt(t, dir, "a.txt", "one\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "main")
	runGit(t, dir, "switch", "-q", "--orphan", "other")
	writeAt(t, dir, "b.txt", "two\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "other")

	if _, err := New(dir).Since(context.Background()); err != ErrNoSplit {
		t.Fatalf("no split: %v", err)
	}
}

func TestBetweenRefusesWhatIsNoHash(t *testing.T) {
	if _, err := New(t.TempDir()).Between(context.Background(), "--all", "HEAD", 5); err == nil {
		t.Fatal("an option passed as a side")
	}
}
