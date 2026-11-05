package gitfacts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestWorkingCopiesFromEveryMember(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "main")
	writeMainRepo(t, mainDir)
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	writeLinkedWorktree(t, mainDir, a, "a")
	writeLinkedWorktree(t, mainDir, b, "b")
	want := []string{mainDir, a, b}
	for _, dir := range want {
		got := WorkingCopies(dir)
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Errorf("WorkingCopies(%s) = %v, want %v", dir, got, want)
		}
	}
	if got := WorkingCopies(filepath.Join(root, "none")); len(got) != 0 {
		t.Errorf("WorkingCopies without a repository = %v, want none", got)
	}
}

func TestWorkingCopiesOfBareMainHoldWorktreesOnly(t *testing.T) {
	root := t.TempDir()
	bareDir := filepath.Join(root, "bare.git")
	gitDir := filepath.Join(bareDir, "worktrees", "feature")
	wtDir := filepath.Join(root, "feature")
	writeTestFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feature\n")
	writeTestFile(t, filepath.Join(gitDir, "commondir"), "../..\n")
	writeTestFile(t, filepath.Join(gitDir, "gitdir"), filepath.Join(wtDir, ".git")+"\n")
	writeTestFile(t, filepath.Join(wtDir, ".git"), "gitdir: "+gitDir+"\n")

	got := WorkingCopies(wtDir)
	if len(got) != 1 || got[0] != wtDir {
		t.Errorf("WorkingCopies = %v, want the worktree alone, a bare main has no working copy", got)
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
		{"git@github.com:marein/x", "github.com/marein/x", "https://github.com/marein/x"},
		{"https://github.com/marein/x.git", "github.com/marein/x", "https://github.com/marein/x"},
		{"https://github.com/marein/x", "github.com/marein/x", "https://github.com/marein/x"},
		{"ssh://git@github.com/marein/x.git", "github.com/marein/x", "https://github.com/marein/x"},
		{"git@gitlab.example.com:group/sub/deeper/repo.git", "gitlab.example.com/group/sub/deeper/repo", "https://gitlab.example.com/group/sub/deeper/repo"},
		{"ssh://git@gitlab.example.com:2222/group/repo.git", "gitlab.example.com/group/repo", "https://gitlab.example.com/group/repo"},
		{"ssh://gitlab.example.com:2222/group/repo", "gitlab.example.com/group/repo", "https://gitlab.example.com/group/repo"},
		{"https://user@gitlab.example.com:8443/group/sub/repo.git", "gitlab.example.com/group/sub/repo", "https://gitlab.example.com:8443/group/sub/repo"},
		{"https://oauth2:secret@gitlab.example.com/group/repo.git", "gitlab.example.com/group/repo", "https://gitlab.example.com/group/repo"},
		{"http://127.0.0.1:8080/x.git", "127.0.0.1/x", "http://127.0.0.1:8080/x"},
		{"git://git.example.com/group/repo.git/", "git.example.com/group/repo", "https://git.example.com/group/repo"},
		{"git+ssh://git@git.example.com/group/repo.git", "git.example.com/group/repo", "https://git.example.com/group/repo"},
		{"deploy@git.example.com:/srv/group/repo.git", "git.example.com/srv/group/repo", "https://git.example.com/srv/group/repo"},
		{"git.example.com:group/repo.git", "git.example.com/group/repo", "https://git.example.com/group/repo"},
		{"ssh://git@[2001:db8::1]:2222/group/repo.git", "2001:db8::1/group/repo", "https://[2001:db8::1]/group/repo"},
		{"  git@GitHub.com:marein/x.git\n", "github.com/marein/x", "https://github.com/marein/x"},
		{"/srv/git/repo.git", "/srv/git/repo", ""},
		{"../repo", "../repo", ""},
		{"./a:b", "./a:b", ""},
		{"file:///srv/git/repo.git", "file:///srv/git/repo", ""},
		{"https://github.com", "https://github.com", ""},
		{"git@github.com:", "git@github.com:", ""},
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

func TestParseRemoteParts(t *testing.T) {
	cases := []struct {
		raw  string
		want Remote
	}{
		{"git@host:group/sub/repo.git", Remote{Scheme: "ssh", Host: "host", Path: "group/sub/repo"}},
		{"ssh://git@host:2222/group/repo.git", Remote{Scheme: "ssh", Host: "host", Port: "2222", Path: "group/repo"}},
		{"https://user@host:8443/group/sub/repo.git", Remote{Scheme: "https", Host: "host", Port: "8443", Path: "group/sub/repo"}},
		{"HTTPS://Host/Group/Repo", Remote{Scheme: "https", Host: "host", Path: "Group/Repo"}},
	}
	for _, c := range cases {
		got, ok := ParseRemote(c.raw)
		if !ok || got != c.want {
			t.Errorf("ParseRemote(%q) = %+v, %v, want %+v", c.raw, got, ok, c.want)
		}
	}
	for _, raw := range []string{"", "/srv/repo.git", "file:///srv/repo.git", "./a:b", "host:", "https://host/", "ssh://git@host:~me/repo"} {
		if got, ok := ParseRemote(raw); ok {
			t.Errorf("ParseRemote(%q) = %+v, want no remote", raw, got)
		}
	}
}

// The stamp moves with HEAD, the config, the packed refs and the loose
// tracking ref of HEAD's branch, in a linked worktree read from the main's
// shared directory, and stays put while a file beside them changes.
func TestRefStampMovesWithTheRefsOfHEADsBranch(t *testing.T) {
	mainDir := filepath.Join(t.TempDir(), "main")
	writeMainRepo(t, mainDir)
	wtDir := filepath.Join(t.TempDir(), "wt")
	writeLinkedWorktree(t, mainDir, wtDir, "wt")
	if got := RefStamp(t.TempDir(), "origin"); got != "" {
		t.Fatalf("a directory without a repository stamps %q", got)
	}
	at := time.Now().Add(-time.Hour)
	touch := func(path string) {
		t.Helper()
		writeTestFile(t, path, "0123456789abcdef0123456789abcdef01234567\n")
		at = at.Add(time.Second)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	git := filepath.Join(mainDir, ".git")
	for _, c := range []struct {
		dir, path string
		moves     bool
	}{
		{mainDir, filepath.Join(git, "refs", "remotes", "origin", "master"), true},
		{mainDir, filepath.Join(git, "refs", "remotes", "origin", "other"), false},
		{mainDir, filepath.Join(git, "refs", "remotes", "upstream", "master"), false},
		{mainDir, filepath.Join(git, "packed-refs"), true},
		{mainDir, filepath.Join(git, "HEAD"), true},
		{mainDir, filepath.Join(git, "FETCH_HEAD"), false},
		{wtDir, filepath.Join(git, "refs", "remotes", "origin", "feature"), true},
		{wtDir, filepath.Join(git, "refs", "remotes", "origin", "master"), false},
		{wtDir, filepath.Join(git, "worktrees", "wt", "HEAD"), true},
		{wtDir, filepath.Join(git, "config"), true},
	} {
		before := RefStamp(c.dir, "origin")
		touch(c.path)
		if moved := RefStamp(c.dir, "origin") != before; moved != c.moves {
			t.Errorf("writing %s moved the stamp of %s: %v", c.path, c.dir, moved)
		}
	}
}

// A branch that tracks a branch of another name on the remote moves the stamp
// with that branch's loose ref, one tracking another remote does not.
func TestRefStampMovesWithTheUpstreamOfAnotherName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	git := filepath.Join(dir, ".git")
	writeTestFile(t, filepath.Join(git, "HEAD"), "ref: refs/heads/fix\n")
	writeTestFile(t, filepath.Join(git, "config"), "[remote \"origin\"]\n\turl = git@github.com:marein/main.git\n"+
		"[branch \"fix\"]\n\tremote = origin\n\tmerge = refs/heads/team/fix-login\n"+
		"[branch \"other\"]\n\tremote = fork\n\tmerge = refs/heads/team/other\n")
	at := time.Now().Add(-time.Hour)
	for _, c := range []struct {
		head, path string
		moves      bool
	}{
		{"fix", filepath.Join(git, "refs", "remotes", "origin", "team", "fix-login"), true},
		{"fix", filepath.Join(git, "refs", "remotes", "origin", "fix"), true},
		{"fix", filepath.Join(git, "refs", "remotes", "fork", "team", "fix-login"), false},
		{"other", filepath.Join(git, "refs", "remotes", "origin", "team", "other"), false},
	} {
		writeTestFile(t, filepath.Join(git, "HEAD"), "ref: refs/heads/"+c.head+"\n")
		before := RefStamp(dir, "origin")
		writeTestFile(t, c.path, "0123456789abcdef0123456789abcdef01234567\n")
		at = at.Add(time.Second)
		if err := os.Chtimes(c.path, at, at); err != nil {
			t.Fatal(err)
		}
		if moved := RefStamp(dir, "origin") != before; moved != c.moves {
			t.Errorf("on %s writing %s moved the stamp: %v", c.head, c.path, moved)
		}
	}
}
