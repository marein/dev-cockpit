package git

import (
	"context"
	"slices"
	"testing"
)

func TestLogPagesThroughAFileHistory(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	for i, content := range []string{"one\n", "two\n", "three\n"} {
		writeAt(t, dir, "a.txt", content)
		runGit(t, dir, "add", "-A")
		runGit(t, dir, "commit", "-qm", content[:len(content)-1])
		if i == 0 {
			writeAt(t, dir, "b.txt", "b\n")
			runGit(t, dir, "add", "-A")
			runGit(t, dir, "commit", "-qm", "only b")
		}
	}

	page, err := New(dir).Log(context.Background(), "a.txt", 0, 2)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if !page.Repo || len(page.Commits) != 2 || !page.More {
		t.Fatalf("first page: %+v", page)
	}
	if page.Commits[0].Summary != "three" || page.Commits[1].Summary != "two" {
		t.Fatalf("newest first: %+v", page.Commits)
	}
	if page.Commits[0].SHA == "" || page.Commits[0].Author != "t" || page.Commits[0].Time == 0 {
		t.Fatalf("commit details: %+v", page.Commits[0])
	}

	rest, err := New(dir).Log(context.Background(), "a.txt", 2, 2)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(rest.Commits) != 1 || rest.More || rest.Commits[0].Summary != "one" {
		t.Fatalf("last page: %+v", rest)
	}

	all, err := New(dir).Log(context.Background(), "", 0, 10)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(all.Commits) != 4 || all.More {
		t.Fatalf("the whole history: %+v", all)
	}
}

func TestLogInASubdirectoryProjectAsksAboutItsOwnFile(t *testing.T) {
	root := t.TempDir()
	commitRepo(t, root)
	writeAt(t, root, "inner.txt", "at the root\n")
	writeAt(t, root, "app/inner.txt", "in the app\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-qm", "both")
	writeAt(t, root, "inner.txt", "root again\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-qm", "root only")

	page, err := New(root+"/app").Log(context.Background(), "inner.txt", 0, 10)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(page.Commits) != 1 || page.Commits[0].Summary != "both" {
		t.Fatalf("the app's file has one commit: %+v", page.Commits)
	}
}

func TestLogWithoutRepositoryOrCommitIsEmptyAndNoError(t *testing.T) {
	page, err := New(t.TempDir()).Log(context.Background(), "", 0, 10)
	if err != nil || page.Repo || len(page.Commits) != 0 {
		t.Fatalf("no repository: %+v %v", page, err)
	}

	dir := t.TempDir()
	commitRepo(t, dir)
	unborn, err := New(dir).Log(context.Background(), "", 0, 10)
	if err != nil || !unborn.Repo || len(unborn.Commits) != 0 || unborn.More {
		t.Fatalf("unborn: %+v %v", unborn, err)
	}
}

// A history names each commit's own first parent, also where it lists one
// file and a commit between them touched another, and the root commit none.
func TestLogNamesEachCommitsOwnFirstParent(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	for _, step := range []struct{ file, message string }{{"a.txt", "root"}, {"b.txt", "only b"}, {"a.txt", "a again"}} {
		writeAt(t, dir, step.file, step.message+"\n")
		runGit(t, dir, "add", "-A")
		runGit(t, dir, "commit", "-qm", step.message)
	}

	all, err := New(dir).Log(context.Background(), "", 0, 10)
	if err != nil || len(all.Commits) != 3 {
		t.Fatalf("log: %+v %v", all, err)
	}
	again, onlyB, root := all.Commits[0], all.Commits[1], all.Commits[2]
	if root.Parent != "" || onlyB.Parent != root.SHA || again.Parent != onlyB.SHA {
		t.Fatalf("parents: %+v", all.Commits)
	}

	file, err := New(dir).Log(context.Background(), "a.txt", 0, 10)
	if err != nil || len(file.Commits) != 2 {
		t.Fatalf("log: %+v %v", file, err)
	}
	if file.Commits[0].Parent != onlyB.SHA || file.Commits[1].Parent != "" {
		t.Fatalf("file history parents: %+v", file.Commits)
	}
}

// What a branch carries since the target as the remote has it is read oldest
// first and counted, the oldest ones where it carries more than the limit,
// each by its subject and a lone one with its body; without the target's
// remote tracking ref the head alone answers.
func TestCarriedReadsTheCommitsSinceTheTarget(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	runGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	base := gitOut(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "Add a", "-m", "Why a.\n\nAnd more.")
	first := gitOut(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "commit", "-q", "--allow-empty", "--allow-empty-message", "-m", "")
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "Fix a", "-m", "Why fix.")
	head := gitOut(t, dir, "rev-parse", "HEAD")
	ctx := context.Background()
	for _, c := range []struct {
		target, head string
		limit        int
		want         []Message
		count        int
		measured     bool
	}{
		{"main", head, 10, []Message{{"Add a", ""}, {"", ""}, {"Fix a", ""}}, 3, true},
		{"main", head, 2, []Message{{"Add a", ""}, {"", ""}}, 3, true},
		{"main", first, 10, []Message{{"Add a", "Why a.\n\nAnd more."}}, 1, true},
		{"release", head, 10, []Message{{"Fix a", "Why fix."}}, 1, false},
		{"main", base, 10, nil, 0, true},
	} {
		got, count, measured, err := New(dir).Carried(ctx, "origin", c.target, c.head, c.limit)
		if err != nil || count != c.count || measured != c.measured || !slices.Equal(got, c.want) {
			t.Fatalf("%s at %s carries %+v, %d, %t, %v", c.target, c.head, got, count, measured, err)
		}
	}
	if _, _, _, err := New(dir).Carried(ctx, "origin", "main", "--all", 10); err == nil {
		t.Fatal("an option passed for a commit")
	}
}
