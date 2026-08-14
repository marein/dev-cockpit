package git

import (
	"context"
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
