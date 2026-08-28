package project

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeMainRepo lays out a normal repository: .git is a directory holding HEAD
// and a config with an origin remote.
func writeMainRepo(t *testing.T, dir string) {
	t.Helper()
	writeTestFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/master\n")
	writeTestFile(t, filepath.Join(dir, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:marein/main.git\n")
}

// writeLinkedWorktree lays out what `git worktree add` writes: the per-worktree
// git directory under <main>/.git/worktrees/<name> with HEAD, commondir and the
// gitdir file naming the worktree's .git file, and that .git pointer file in
// the worktree itself. The detection itself is gitfacts' and tested there;
// these tests cover the mapping onto projects under the root.
func writeLinkedWorktree(t *testing.T, mainDir, wtDir, name string) {
	t.Helper()
	gitDir := filepath.Join(mainDir, ".git", "worktrees", name)
	writeTestFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feature\n")
	writeTestFile(t, filepath.Join(gitDir, "commondir"), "../..\n")
	writeTestFile(t, filepath.Join(gitDir, "gitdir"), filepath.Join(wtDir, ".git")+"\n")
	writeTestFile(t, filepath.Join(wtDir, ".git"), "gitdir: "+gitDir+"\n")
}

func listByName(t *testing.T, r *Repository) map[string]Project {
	t.Helper()
	out := map[string]Project{}
	for _, p := range r.List() {
		out[p.Name] = p
	}
	return out
}

func TestListDetectsLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	r := NewRepository(root, nil)
	mainDir := filepath.Join(root, "main")
	writeMainRepo(t, mainDir)
	writeLinkedWorktree(t, mainDir, filepath.Join(root, "feature"), "feature")

	projects := listByName(t, r)

	main, ok := projects["main"]
	if !ok {
		t.Fatal("main project missing from list")
	}
	if !main.GitRepo {
		t.Error("main: GitRepo = false, want true")
	}
	if main.GitWorktree {
		t.Error("main: GitWorktree = true, want false")
	}
	if main.GitWorktreeOf != "" {
		t.Errorf("main: GitWorktreeOf = %q, want empty", main.GitWorktreeOf)
	}

	wt, ok := projects["feature"]
	if !ok {
		t.Fatal("feature project missing from list")
	}
	if !wt.GitRepo {
		t.Error("feature: GitRepo = false, want true")
	}
	if !wt.GitWorktree {
		t.Error("feature: GitWorktree = false, want true")
	}
	if wt.GitWorktreeOf != "main" {
		t.Errorf("feature: GitWorktreeOf = %q, want %q", wt.GitWorktreeOf, "main")
	}
	if wt.GitWorktreeMain != mainDir {
		t.Errorf("feature: GitWorktreeMain = %q, want %q", wt.GitWorktreeMain, mainDir)
	}
	if wt.GitBranch != "feature" {
		t.Errorf("feature: GitBranch = %q, want %q", wt.GitBranch, "feature")
	}
	if wt.GitOrigin != "github.com/marein/main" {
		t.Errorf("feature: GitOrigin = %q, want %q", wt.GitOrigin, "github.com/marein/main")
	}
}

func TestListWorktreeOfMainOutsideRoot(t *testing.T) {
	root := t.TempDir()
	r := NewRepository(root, nil)
	mainDir := filepath.Join(t.TempDir(), "elsewhere")
	writeMainRepo(t, mainDir)
	writeLinkedWorktree(t, mainDir, filepath.Join(root, "feature"), "feature")

	wt := listByName(t, r)["feature"]
	if !wt.GitWorktree {
		t.Error("GitWorktree = false, want true")
	}
	if wt.GitWorktreeOf != "" {
		t.Errorf("GitWorktreeOf = %q, want empty", wt.GitWorktreeOf)
	}
	if wt.GitWorktreeMain != mainDir {
		t.Errorf("GitWorktreeMain = %q, want %q", wt.GitWorktreeMain, mainDir)
	}
}

func TestListMainListsWorktrees(t *testing.T) {
	root := t.TempDir()
	r := NewRepository(root, nil)
	mainDir := filepath.Join(root, "main")
	writeMainRepo(t, mainDir)
	writeLinkedWorktree(t, mainDir, filepath.Join(root, "feature"), "feature")
	outside := filepath.Join(t.TempDir(), "elsewhere")
	writeLinkedWorktree(t, mainDir, outside, "away")

	projects := listByName(t, r)

	main := projects["main"]
	if len(main.GitWorktrees) != 2 {
		t.Fatalf("main: GitWorktrees = %+v, want 2 entries", main.GitWorktrees)
	}
	away, inRoot := main.GitWorktrees[0], main.GitWorktrees[1]
	if away.Path != outside || away.Project != "" {
		t.Errorf("outside worktree = %+v, want Path %q and no project", away, outside)
	}
	if inRoot.Path != filepath.Join(root, "feature") || inRoot.Project != "feature" {
		t.Errorf("project worktree = %+v, want Path %q and project %q", inRoot, filepath.Join(root, "feature"), "feature")
	}

	if wt := projects["feature"]; len(wt.GitWorktrees) != 0 {
		t.Errorf("feature: GitWorktrees = %+v, want none", wt.GitWorktrees)
	}
}

func TestRemoveWorktreePrunesRegistration(t *testing.T) {
	root := t.TempDir()
	r := NewRepository(root, nil)
	mainDir := filepath.Join(root, "main")
	writeMainRepo(t, mainDir)
	writeLinkedWorktree(t, mainDir, filepath.Join(root, "feature"), "feature")

	wt, err := r.FindByName("feature")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "feature")); !os.IsNotExist(err) {
		t.Error("worktree directory still there")
	}
	if _, err := os.Stat(filepath.Join(mainDir, ".git", "worktrees", "feature")); !os.IsNotExist(err) {
		t.Error("registration in the main repository still there")
	}
	if _, err := os.Stat(filepath.Join(mainDir, ".git", "HEAD")); err != nil {
		t.Error("main repository was touched")
	}
}

func TestListNormalRepoAndNoRepo(t *testing.T) {
	root := t.TempDir()
	r := NewRepository(root, nil)
	writeMainRepo(t, filepath.Join(root, "repo"))
	if err := os.MkdirAll(filepath.Join(root, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}

	projects := listByName(t, r)

	repo := projects["repo"]
	if !repo.GitRepo {
		t.Error("repo: GitRepo = false, want true")
	}
	if repo.GitWorktree {
		t.Error("repo: GitWorktree = true, want false")
	}
	if repo.GitBranch != "master" {
		t.Errorf("repo: GitBranch = %q, want %q", repo.GitBranch, "master")
	}

	plain := projects["plain"]
	if plain.GitRepo {
		t.Error("plain: GitRepo = true, want false")
	}
	if plain.GitWorktree {
		t.Error("plain: GitWorktree = true, want false")
	}
}
