package git

import (
	"context"
	"strings"
	"testing"
)

// commitRepo builds a repository with an identity of its own, so the commits
// this package makes do not depend on the machine's global configuration.
func commitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "config", "commit.gpgsign", "false")
}

func worktreeByPath(t *testing.T, dir string) map[string]FileStatus {
	t.Helper()
	changes, err := New(dir).Changes(context.Background())
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	byPath := map[string]FileStatus{}
	for _, entry := range changes.Worktree {
		byPath[entry.Path] = entry.FileStatus
	}
	return byPath
}

func TestCommitPicksOnlyTheChosenPaths(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "a\n")
	writeAt(t, dir, "b.txt", "b\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	writeAt(t, dir, "a.txt", "a2\n")
	writeAt(t, dir, "b.txt", "b2\n")

	result, err := New(dir).Commit(context.Background(), "pick a", []string{"a.txt"}, false)

	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if result.Hash == "" || result.Subject != "pick a" {
		t.Fatalf("result: %+v", result)
	}
	left := worktreeByPath(t, dir)
	if _, ok := left["a.txt"]; ok {
		t.Fatal("a.txt was picked and must be committed")
	}
	if _, ok := left["b.txt"]; !ok {
		t.Fatal("b.txt was not picked and must stay a change")
	}
}

func TestChangesListEveryFileOfAnUntrackedDirectory(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "kept.txt", "kept\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	writeAt(t, dir, "fresh/one.txt", "one\n")
	writeAt(t, dir, "fresh/deep/two.txt", "two\n")

	left := worktreeByPath(t, dir)

	if _, ok := left["fresh/"]; ok {
		t.Fatalf("the folder is one collapsed line, its files cannot be picked: %+v", left)
	}
	if _, ok := left["fresh/one.txt"]; !ok {
		t.Fatalf("fresh/one.txt is not listed: %+v", left)
	}
	if _, ok := left["fresh/deep/two.txt"]; !ok {
		t.Fatalf("fresh/deep/two.txt is not listed: %+v", left)
	}
}

func TestCommitTakesAnUntrackedFileThroughIntentToAdd(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "kept.txt", "kept\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	writeAt(t, dir, "fresh/new.txt", "new\n")

	if _, err := New(dir).Commit(context.Background(), "take the folder", []string{"fresh/"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if left := worktreeByPath(t, dir); len(left) != 0 {
		t.Fatalf("nothing may be left: %+v", left)
	}
}

func TestCommitCarriesTheRenameSourceAlong(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "old.txt", "same content, long enough to pair\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	runGit(t, dir, "mv", "old.txt", "new.txt")

	if _, err := New(dir).Commit(context.Background(), "rename", []string{"new.txt"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if left := worktreeByPath(t, dir); len(left) != 0 {
		t.Fatalf("the source's deletion must travel with the rename: %+v", left)
	}
}

func TestCommitLeavesForeignStagedWorkStaged(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "mine.txt", "mine\n")
	writeAt(t, dir, "theirs.txt", "theirs\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	writeAt(t, dir, "theirs.txt", "theirs staged\n")
	runGit(t, dir, "add", "theirs.txt")
	writeAt(t, dir, "mine.txt", "mine changed\n")

	if _, err := New(dir).Commit(context.Background(), "only mine", []string{"mine.txt"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}

	left := worktreeByPath(t, dir)
	if got := left["theirs.txt"]; got.Index != "M" {
		t.Fatalf("the coder's staged work must stay staged: %+v", left)
	}
}

func TestCommitAmendRewritesTheTip(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "first words")
	writeAt(t, dir, "a.txt", "a2\n")

	result, err := New(dir).Commit(context.Background(), "better words", []string{"a.txt"}, true)

	if err != nil {
		t.Fatalf("amend: %v", err)
	}
	if result.Subject != "better words" {
		t.Fatalf("result: %+v", result)
	}
	repo := New(dir)
	out, err := repo.run(context.Background(), []string{"rev-list", "--count", "HEAD"}, nil)
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if strings.TrimSpace(string(out)) != "1" {
		t.Fatalf("an amend must not add a commit: %s", out)
	}
}

func TestCommitAmendWithoutPathsRewritesOnlyTheMessage(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "a\n")
	writeAt(t, dir, "staged.txt", "staged\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "typo in the messge")
	writeAt(t, dir, "staged.txt", "the coder's work\n")
	runGit(t, dir, "add", "staged.txt")

	result, err := New(dir).Commit(context.Background(), "typo in the message", nil, true)

	if err != nil {
		t.Fatalf("amend: %v", err)
	}
	if result.Subject != "typo in the message" {
		t.Fatalf("result: %+v", result)
	}
	repo := New(dir)
	out, err := repo.run(context.Background(), []string{"rev-list", "--count", "HEAD"}, nil)
	if err != nil || strings.TrimSpace(string(out)) != "1" {
		t.Fatalf("an amend must not add a commit: %s %v", out, err)
	}
	shown, err := repo.run(context.Background(), []string{"cat-file", "blob", "HEAD:./staged.txt"}, nil)
	if err != nil || string(shown) != "staged\n" {
		t.Fatalf("the staged work must stay out of the tip: %q %v", shown, err)
	}
	left := worktreeByPath(t, dir)
	if got := left["staged.txt"]; got.Index != "M" {
		t.Fatalf("the coder's staged work must stay staged: %+v", left)
	}

	if _, err := New(dir).Commit(context.Background(), "no amend, no paths", nil, false); err == nil {
		t.Fatal("without an amend an empty pick must be refused")
	}
}

func TestCommitInASubdirectoryProjectStaysInsideIt(t *testing.T) {
	root := t.TempDir()
	commitRepo(t, root)
	writeAt(t, root, "outside.txt", "outside\n")
	writeAt(t, root, "app/inner.txt", "inner\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-qm", "init")
	writeAt(t, root, "outside.txt", "outside changed\n")
	writeAt(t, root, "app/inner.txt", "inner changed\n")

	if _, err := New(root+"/app").Commit(context.Background(), "inner only", []string{"inner.txt"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}

	left := worktreeByPath(t, root)
	if _, ok := left["app/inner.txt"]; ok {
		t.Fatal("the project's own file must be committed")
	}
	if _, ok := left["outside.txt"]; !ok {
		t.Fatal("a file outside the project must stay a change")
	}
}

func TestCommitInfoNamesBranchAndLastMessage(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)

	empty, err := New(dir).CommitInfo(context.Background())
	if err != nil {
		t.Fatalf("info on unborn: %v", err)
	}
	if !empty.Repo || empty.HasCommit || empty.Branch == "" {
		t.Fatalf("unborn: %+v", empty)
	}

	writeAt(t, dir, "a.txt", "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "the last words")

	info, err := New(dir).CommitInfo(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if !info.HasCommit || info.LastMessage != "the last words" {
		t.Fatalf("info: %+v", info)
	}

	without, err := New(t.TempDir()).CommitInfo(context.Background())
	if err != nil {
		t.Fatalf("info without repository: %v", err)
	}
	if without.Repo {
		t.Fatalf("no repository: %+v", without)
	}
}
