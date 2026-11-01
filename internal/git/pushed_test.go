package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pushedRepo is a clone of a bare remote: main and feature/login pushed with
// their upstreams, topic pushed under another name, unpushed never, and
// theirs pushed from another clone and only fetched here. feature/login has
// one commit more than it pushed. It answers the clone and the commits by
// name.
func pushedRepo(t *testing.T) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	remote, work, other := filepath.Join(root, "remote.git"), filepath.Join(root, "work"), filepath.Join(root, "other")
	runGit(t, root, "init", "-q", "--bare", "-b", "main", remote)
	runGit(t, root, "clone", "-q", remote, work)
	commitRepo(t, work)
	runGit(t, work, "symbolic-ref", "HEAD", "refs/heads/main")
	commit := func(dir, name string) string {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "add", name)
		runGit(t, dir, "commit", "-q", "-m", name)
		return strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	}
	shas := map[string]string{"main": commit(work, "a")}
	runGit(t, work, "push", "-q", "-u", "origin", "main")
	runGit(t, work, "switch", "-q", "-c", "feature/login")
	shas["feature/login"] = commit(work, "b")
	runGit(t, work, "push", "-q", "-u", "origin", "feature/login")
	shas["feature/login local"] = commit(work, "c")
	runGit(t, work, "switch", "-q", "-c", "topic", "main")
	shas["topic"] = commit(work, "d")
	runGit(t, work, "push", "-q", "-u", "origin", "topic:topic-remote")
	runGit(t, work, "switch", "-q", "-c", "unpushed", "main")
	shas["unpushed"] = commit(work, "e")
	runGit(t, root, "clone", "-q", "-b", "main", remote, other)
	commitRepo(t, other)
	runGit(t, other, "switch", "-q", "-c", "theirs")
	shas["theirs"] = commit(other, "f")
	runGit(t, other, "push", "-q", "origin", "theirs")
	runGit(t, work, "fetch", "-q")
	return work, shas
}

// The pushed head is the remote tracking ref the upstream names, or the one
// of the same name without an upstream there, never the local branch; and a
// name is matched whole, feature never answering for feature/login.
func TestPushedHeadReadsTheRemoteTrackingRef(t *testing.T) {
	work, shas := pushedRepo(t)
	r := New(work)
	for _, c := range []struct {
		branch string
		want   Pushed
	}{
		{"feature/login", Pushed{Branch: "feature/login", Remote: "feature/login", Head: shas["feature/login"], Local: shas["feature/login local"]}},
		{"topic", Pushed{Branch: "topic", Remote: "topic-remote", Head: shas["topic"], Local: shas["topic"]}},
		{"unpushed", Pushed{Branch: "unpushed", Remote: "unpushed", Local: shas["unpushed"]}},
		{"theirs", Pushed{Branch: "theirs", Remote: "theirs", Head: shas["theirs"]}},
		{"feature", Pushed{Branch: "feature", Remote: "feature"}},
		{"", Pushed{Branch: "unpushed", Remote: "unpushed", Local: shas["unpushed"]}},
	} {
		got, err := r.PushedHead(context.Background(), "origin", c.branch)
		if err != nil || got != c.want {
			t.Errorf("%q: %+v, %v, want %+v", c.branch, got, err, c.want)
		}
	}
	if _, err := r.PushedHead(context.Background(), "origin", "-c"); err == nil {
		t.Fatal("a branch that reads as an option went to git")
	}
	runGit(t, work, "switch", "-q", "--detach", "main")
	if _, err := r.PushedHead(context.Background(), "origin", ""); !errors.Is(err, ErrDetached) {
		t.Fatalf("a detached HEAD: %v", err)
	}
	if got, err := r.PushedHead(context.Background(), "origin", "main"); err != nil || got.Head != shas["main"] {
		t.Fatalf("a named branch beside a detached HEAD: %+v, %v", got, err)
	}
}

// A branch ahead of the pushed head, behind it, and gone apart from it each
// count the commits on either side; what is no full hash never reaches git.
func TestApartCountsTheCommitsOnEitherSide(t *testing.T) {
	work, shas := pushedRepo(t)
	r := New(work)
	for _, c := range []struct {
		a, b          string
		ahead, behind int
	}{
		{"feature/login local", "feature/login", 1, 0},
		{"feature/login", "feature/login local", 0, 1},
		{"topic", "unpushed", 1, 1},
		{"feature/login local", "unpushed", 2, 1},
		{"main", "main", 0, 0},
	} {
		ahead, behind, err := r.Apart(context.Background(), shas[c.a], shas[c.b])
		if err != nil || ahead != c.ahead || behind != c.behind {
			t.Errorf("%s against %s: %d ahead, %d behind, %v", c.a, c.b, ahead, behind, err)
		}
	}
	if _, _, err := r.Apart(context.Background(), "--all", shas["main"]); err == nil {
		t.Fatal("what reads as an option went to git")
	}
}
