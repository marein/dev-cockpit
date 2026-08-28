package gitfacts

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
// the worktree itself.
func writeLinkedWorktree(t *testing.T, mainDir, wtDir, name string) {
	t.Helper()
	gitDir := filepath.Join(mainDir, ".git", "worktrees", name)
	writeTestFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feature\n")
	writeTestFile(t, filepath.Join(gitDir, "commondir"), "../..\n")
	writeTestFile(t, filepath.Join(gitDir, "gitdir"), filepath.Join(wtDir, ".git")+"\n")
	writeTestFile(t, filepath.Join(wtDir, ".git"), "gitdir: "+gitDir+"\n")
}

func TestReadNormalRepo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "main")
	writeMainRepo(t, dir)

	got := Read(dir)
	if !got.Repo || got.Worktree || got.WorktreeMain != "" {
		t.Errorf("Read = %+v, want a plain repo", got)
	}
	if got.Branch != "master" {
		t.Errorf("Branch = %q, want %q", got.Branch, "master")
	}
	if got.Origin != "git@github.com:marein/main.git" {
		t.Errorf("Origin = %q", got.Origin)
	}
	if len(got.WorktreePaths) != 0 {
		t.Errorf("WorktreePaths = %v, want none", got.WorktreePaths)
	}
}

func TestReadNoRepo(t *testing.T) {
	got := Read(t.TempDir())
	if got.Repo || got.Worktree || got.Branch != "" || got.Origin != "" || len(got.WorktreePaths) != 0 {
		t.Errorf("Read = %+v, want zero", got)
	}
}

func TestReadLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "main")
	wtDir := filepath.Join(root, "feature")
	writeMainRepo(t, mainDir)
	writeLinkedWorktree(t, mainDir, wtDir, "feature")

	got := Read(wtDir)
	if !got.Repo || !got.Worktree {
		t.Fatalf("Read = %+v, want a worktree", got)
	}
	if got.WorktreeMain != mainDir {
		t.Errorf("WorktreeMain = %q, want %q", got.WorktreeMain, mainDir)
	}
	if got.Branch != "feature" {
		t.Errorf("Branch = %q, want %q", got.Branch, "feature")
	}
	if got.Origin != "git@github.com:marein/main.git" {
		t.Errorf("Origin = %q, want the main repository's config answered through commondir", got.Origin)
	}
}

func TestReadDanglingPointerIsNoWorktree(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gone")
	writeTestFile(t, filepath.Join(dir, ".git"), "gitdir: "+filepath.Join(root, "missing", ".git", "worktrees", "gone")+"\n")

	got := Read(dir)
	if !got.Repo {
		t.Error("Repo = false, want true, the .git file exists")
	}
	if got.Worktree || got.WorktreeMain != "" {
		t.Errorf("Read = %+v, want no worktree facts", got)
	}
}

func TestReadSubmodulePointerIsNoWorktree(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "main")
	writeMainRepo(t, mainDir)
	moduleGitDir := filepath.Join(mainDir, ".git", "modules", "sub")
	writeTestFile(t, filepath.Join(moduleGitDir, "HEAD"), "ref: refs/heads/master\n")
	writeTestFile(t, filepath.Join(root, "sub", ".git"), "gitdir: "+moduleGitDir+"\n")

	got := Read(filepath.Join(root, "sub"))
	if !got.Repo || got.Worktree {
		t.Errorf("Read = %+v, want a repo that is no worktree", got)
	}
}

func TestReadWorktreeOfBareMain(t *testing.T) {
	root := t.TempDir()
	bareDir := filepath.Join(root, "bare.git")
	gitDir := filepath.Join(bareDir, "worktrees", "feature")
	wtDir := filepath.Join(root, "feature")
	writeTestFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feature\n")
	writeTestFile(t, filepath.Join(gitDir, "commondir"), "../..\n")
	writeTestFile(t, filepath.Join(wtDir, ".git"), "gitdir: "+gitDir+"\n")

	got := Read(wtDir)
	if !got.Worktree {
		t.Fatal("Worktree = false, want true")
	}
	if got.WorktreeMain != bareDir {
		t.Errorf("WorktreeMain = %q, want the bare directory %q", got.WorktreeMain, bareDir)
	}
}

func TestReadListsRegisteredWorktrees(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "main")
	writeMainRepo(t, mainDir)
	writeLinkedWorktree(t, mainDir, filepath.Join(root, "feature"), "feature")
	gone := filepath.Join(root, "gone")
	writeLinkedWorktree(t, mainDir, gone, "gone")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(mainDir, ".git", "worktrees", "empty", "HEAD"), "ref: refs/heads/x\n")

	got := Read(mainDir)
	want := []string{filepath.Join(root, "feature")}
	if len(got.WorktreePaths) != 1 || got.WorktreePaths[0] != want[0] {
		t.Errorf("WorktreePaths = %v, want %v, the deleted and the half written entry drop out", got.WorktreePaths, want)
	}
}

func TestPruneWorktreeRegistration(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "main")
	wtDir := filepath.Join(root, "feature")
	writeMainRepo(t, mainDir)
	writeLinkedWorktree(t, mainDir, wtDir, "feature")

	PruneWorktreeRegistration(wtDir)
	if _, err := os.Stat(filepath.Join(mainDir, ".git", "worktrees", "feature")); !os.IsNotExist(err) {
		t.Error("registration still there")
	}
	if _, err := os.Stat(filepath.Join(mainDir, ".git", "HEAD")); err != nil {
		t.Error("main repository was touched")
	}
}

func TestPruneLeavesForeignRegistration(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "main")
	wtDir := filepath.Join(root, "feature")
	writeMainRepo(t, mainDir)
	writeLinkedWorktree(t, mainDir, wtDir, "feature")
	gitDir := filepath.Join(mainDir, ".git", "worktrees", "feature")
	writeTestFile(t, filepath.Join(gitDir, "gitdir"), filepath.Join(root, "somewhere-else", ".git")+"\n")

	PruneWorktreeRegistration(wtDir)
	if _, err := os.Stat(gitDir); err != nil {
		t.Error("registration naming another worktree was removed")
	}
}

func TestOriginForms(t *testing.T) {
	cases := []struct{ raw, name, web string }{
		{"git@github.com:marein/x.git", "github.com/marein/x", "https://github.com/marein/x"},
		{"https://github.com/marein/x.git", "github.com/marein/x", "https://github.com/marein/x"},
		{"ssh://git@github.com/marein/x.git", "github.com/marein/x", "https://github.com/marein/x"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := OriginName(c.raw); got != c.name {
			t.Errorf("OriginName(%q) = %q, want %q", c.raw, got, c.name)
		}
		if got := OriginWebURL(c.raw); got != c.web {
			t.Errorf("OriginWebURL(%q) = %q, want %q", c.raw, got, c.web)
		}
	}
}
