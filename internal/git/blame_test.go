package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBlameListsEachCommitOnceAndOneIndexPerLine(t *testing.T) {
	// The porcelain format writes a commit's details the first time it appears
	// and only the header line after that; the content of the line itself is the
	// one line that starts with a tab.
	out := strings.Join([]string{
		"1111111111111111111111111111111111111111 1 1 2",
		"author Ada",
		"author-time 1690000000",
		"summary the first commit",
		"filename a.txt",
		"\tone",
		"1111111111111111111111111111111111111111 2 2",
		"\ttwo",
		"2222222222222222222222222222222222222222 9 3 1",
		"author Grace",
		"author-time 1690000900",
		"summary a later change",
		"filename a.txt",
		"\tthree",
		"1111111111111111111111111111111111111111 3 4",
		"\tfour",
	}, "\n")

	commits, lines := parseBlame([]byte(out))

	if len(commits) != 2 {
		t.Fatalf("commits: %+v", commits)
	}
	if commits[0].Author != "Ada" || commits[0].Summary != "the first commit" || commits[0].Time != 1690000000 {
		t.Fatalf("first commit: %+v", commits[0])
	}
	if commits[0].Short != "1111111" {
		t.Fatalf("short: %q", commits[0].Short)
	}
	if commits[1].Author != "Grace" {
		t.Fatalf("second commit: %+v", commits[1])
	}
	if len(lines) != 4 {
		t.Fatalf("lines: %v", lines)
	}
	// One index per line, and the repeated commit is not listed twice.
	if lines[0] != 0 || lines[1] != 0 || lines[2] != 1 || lines[3] != 0 {
		t.Fatalf("line to commit: %v", lines)
	}
}

// A line that only exists in the working copy has no commit to point at, and
// git says so with an all zero one. Reading it as a real commit would put a
// meaningless sha in the gutter.
func TestParseBlameMarksTheUncommittedCommitAsPending(t *testing.T) {
	out := strings.Join([]string{
		"0000000000000000000000000000000000000000 1 1 1",
		"author Not Committed Yet",
		"author-time 1690001000",
		"summary Version of a.txt from a.txt",
		"\tjust typed",
	}, "\n")

	commits, lines := parseBlame([]byte(out))

	if len(commits) != 1 || !commits[0].Pending {
		t.Fatalf("commits: %+v", commits)
	}
	if len(lines) != 1 || lines[0] != 0 {
		t.Fatalf("lines: %v", lines)
	}
}

// The porcelain format costs a multiple of the file it describes, a header
// line per line of it, so a file the editor still opens can outgrow the output
// cap. Truncated output must not become a blame: the head of the file would
// carry its commits and the rest would read like a part nobody ever touched.
func TestBlameOverTheOutputCapAnswersLargeAndNoLines(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	line := strings.Repeat("x", 40) + "\n"
	writeAt(t, dir, "big.txt", strings.Repeat(line, 120000))
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "big")

	blame, err := New(dir).Blame(context.Background(), "big.txt")

	if err != nil {
		t.Fatalf("blame: %v", err)
	}
	if !blame.Large {
		t.Fatal("a blame that filled the output cap must say so")
	}
	if len(blame.Lines) != 0 || len(blame.Commits) != 0 {
		t.Fatalf("half a blame must not travel: %d lines, %d commits", len(blame.Lines), len(blame.Commits))
	}
}

// And a file that stays under it answers the whole blame, so the cap is a
// ceiling and not a second limit on what the gutter shows.
func TestBlameUnderTheOutputCapIsWholeAndNotLarge(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "small.txt", "one\ntwo\nthree\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "small")

	blame, err := New(dir).Blame(context.Background(), "small.txt")

	if err != nil {
		t.Fatalf("blame: %v", err)
	}
	if blame.Large || len(blame.Lines) != 3 || len(blame.Commits) != 1 {
		t.Fatalf("blame: %+v", blame)
	}
}

// Every commit names its first parent and the root commit none: the line menu
// shows a commit against its parent and has to know when there is none.
func TestBlameNamesEachCommitsFirstParent(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "p.txt", "one\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "root")
	writeAt(t, dir, "p.txt", "one\ntwo\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "second")

	blame, err := New(dir).Blame(context.Background(), "p.txt")

	if err != nil {
		t.Fatalf("blame: %v", err)
	}
	if len(blame.Commits) != 2 {
		t.Fatalf("commits: %+v", blame.Commits)
	}
	root, second := blame.Commits[0], blame.Commits[1]
	if root.Summary != "root" {
		root, second = second, root
	}
	if root.Parent != "" {
		t.Fatalf("the root commit names a parent: %q", root.Parent)
	}
	if second.Parent != root.SHA {
		t.Fatalf("the second commit's parent is %q, the root is %q", second.Parent, root.SHA)
	}
}

// The newest commit is the one that landed last and only one is marked, even
// where two commits share the author and the day, and a file of a single
// commit marks none.
func TestBlameMarksOneNewestCommitAndNoneForASingleCommit(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "one.txt", "one\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "only")
	single, err := New(dir).Blame(context.Background(), "one.txt")
	if err != nil {
		t.Fatalf("blame: %v", err)
	}
	if len(single.Commits) != 1 || single.Commits[0].Newest {
		t.Fatalf("a single commit is marked newest: %+v", single.Commits)
	}

	writeAt(t, dir, "two.txt", "one\n")
	runGit(t, dir, "add", "-A")
	runGitEnv(t, dir, []string{"GIT_AUTHOR_DATE=2024-01-02T10:00:00Z", "GIT_COMMITTER_DATE=2024-01-02T10:00:00Z"}, "commit", "-qm", "older")
	writeAt(t, dir, "two.txt", "one\ntwo\n")
	runGit(t, dir, "add", "-A")
	runGitEnv(t, dir, []string{"GIT_AUTHOR_DATE=2024-01-02T09:00:00Z", "GIT_COMMITTER_DATE=2024-01-02T11:00:00Z"}, "commit", "-qm", "rebased")
	blame, err := New(dir).Blame(context.Background(), "two.txt")
	if err != nil {
		t.Fatalf("blame: %v", err)
	}
	marked := []string{}
	for _, commit := range blame.Commits {
		if commit.Newest {
			marked = append(marked, commit.Summary)
		}
	}
	if len(marked) != 1 || marked[0] != "rebased" {
		t.Fatalf("newest by commit time must be the rebased commit alone, marked: %v", marked)
	}
}

// blame follows a rename, and the commit that wrote a line before it names
// the path the file had then, relative to the project, with the path of the
// version before it where there is one.
func TestBlameNamesThePathAFileHadInEachCommit(t *testing.T) {
	root := t.TempDir()
	commitRepo(t, root)
	writeAt(t, root, "sub/old.txt", "one\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-qm", "create")
	writeAt(t, root, "sub/old.txt", "one\ntwo\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-qm", "grow")
	runGit(t, root, "mv", "sub/old.txt", "sub/new.txt")
	runGit(t, root, "commit", "-qm", "rename")

	blame, err := New(filepath.Join(root, "sub")).Blame(context.Background(), "new.txt")

	if err != nil {
		t.Fatalf("blame: %v", err)
	}
	bySummary := map[string]Commit{}
	for _, commit := range blame.Commits {
		bySummary[commit.Summary] = commit
	}
	grow, ok := bySummary["grow"]
	if !ok {
		t.Fatalf("commits: %+v", blame.Commits)
	}
	if grow.Path != "old.txt" || grow.PreviousPath != "old.txt" {
		t.Fatalf("grow names %q before %q, want old.txt relative to the project", grow.Path, grow.PreviousPath)
	}
	if create := bySummary["create"]; create.Path != "old.txt" || create.PreviousPath != "" {
		t.Fatalf("create names %q before %q", create.Path, create.PreviousPath)
	}
}

func TestBlameWithoutRepositoryIsEmptyAndNoError(t *testing.T) {
	dir := t.TempDir()

	blame, err := New(dir).Blame(context.Background(), "a.txt")
	if err != nil {
		t.Fatalf("blame must not error without a repository: %v", err)
	}
	if blame.Repo || blame.Commits == nil || blame.Lines == nil {
		t.Fatalf("blame: %+v", blame)
	}
}

// Both routes take a path from the browser, and both hand it to git behind the
// "--" separator. A path that tries to leave the project is cleaned back into
// it rather than reaching git as something else.
func TestRepoPathStaysInsideTheProject(t *testing.T) {
	for _, in := range []string{"../../etc/passwd", "/etc/passwd", "a/../../../etc/passwd"} {
		got, err := repoPath(in)
		if err != nil {
			continue
		}
		if strings.Contains(got, "..") || strings.HasPrefix(got, "/") {
			t.Fatalf("%q became %q", in, got)
		}
	}
	if _, err := repoPath("  "); err == nil {
		t.Fatal("an empty path was accepted")
	}
	if got, _ := repoPath("sub/file.txt"); got != "./sub/file.txt" {
		t.Fatalf("plain path: %q", got)
	}
}

// runGitEnv is runGit with extra environment, the dates a commit is made with.
func runGitEnv(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}
