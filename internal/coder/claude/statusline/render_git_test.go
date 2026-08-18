package statusline

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitIn runs git in a repository built for a test, with an identity of its own
// so a machine without one still gets a commit.
func gitIn(t *testing.T, work string, args ...string) {
	t.Helper()
	full := append([]string{"-C", work, "-c", "user.name=e2e", "-c", "user.email=e2e@example.com"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on this machine")
	}
}

func inDir(work string) string {
	return `{"workspace": {"current_dir": "` + work + `"}}`
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRenderReadsTheRepository(t *testing.T) {
	requireGit(t)
	work := t.TempDir()
	gitIn(t, work, "init", "--initial-branch=work")
	writeFile(t, filepath.Join(work, "a.txt"), "one\n")
	gitIn(t, work, "add", "a.txt")
	gitIn(t, work, "commit", "-m", "first")
	writeFile(t, filepath.Join(work, "b.txt"), "two\n")
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "branch", Color: "magenta"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "git_changes", Label: "±", LabelColor: "dim"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "git_age", Label: "age", LabelColor: "dim"},
	})
	want := "\x1b[35mwork\x1b[0m \x1b[2m·\x1b[0m \x1b[2m±\x1b[0m 1 \x1b[2m·\x1b[0m \x1b[2mage\x1b[0m 0s\n"
	if out := draw(t, entries, inDir(work)); out != want {
		t.Fatalf("the git line is %q, want %q", out, want)
	}
	// Outside a repository every one of them falls away, and with them the
	// separators that would lead nowhere.
	if bare := draw(t, entries, inDir(t.TempDir())); bare != "\n" {
		t.Fatalf("outside a repository the line is %q, want it empty", bare)
	}
}

// The branch is the one status names, which exists in a repository without a
// first commit too, where the branch is simply empty; a detached head is on no
// branch at all.
func TestRenderNamesTheBranchOfARepositoryWithoutACommit(t *testing.T) {
	requireGit(t)
	work := t.TempDir()
	gitIn(t, work, "init", "--initial-branch=work")
	entries := Normalize([]Entry{{Kind: KindValue, Value: "branch", Color: "magenta"}})
	if out := draw(t, entries, inDir(work)); out != "\x1b[35mwork\x1b[0m\n" {
		t.Fatalf("a repository without a commit is on %q, want work", out)
	}
	gitIn(t, work, "commit", "--allow-empty", "-m", "first")
	if out := draw(t, entries, inDir(work)); out != "\x1b[35mwork\x1b[0m\n" {
		t.Fatalf("after the first commit the branch is %q, want work", out)
	}
	gitIn(t, work, "checkout", "--detach", "HEAD")
	if out := draw(t, entries, inDir(work)); out != "\n" {
		t.Fatalf("a detached head says %q, want nothing", out)
	}
}

// A folder somebody just wrote files into is that many changed files, not one.
func TestRenderCountsEveryNewFileOfANewFolder(t *testing.T) {
	requireGit(t)
	work := t.TempDir()
	gitIn(t, work, "init", "--initial-branch=work")
	writeFile(t, filepath.Join(work, "a.txt"), "one\n")
	gitIn(t, work, "add", "a.txt")
	gitIn(t, work, "commit", "-m", "first")
	writeFile(t, filepath.Join(work, "a.txt"), "two\n")
	writeFile(t, filepath.Join(work, "sub", "b.txt"), "new\n")
	writeFile(t, filepath.Join(work, "sub", "c.txt"), "new\n")
	entries := Normalize([]Entry{{Kind: KindValue, Value: "git_changes"}})
	if out := draw(t, entries, inDir(work)); out != "3\n" {
		t.Fatalf("one changed file and two new ones in a new folder count as %q, want 3", out)
	}
}

func TestRenderCountsStashesAndDrift(t *testing.T) {
	requireGit(t)
	origin := t.TempDir()
	gitIn(t, origin, "init", "--initial-branch=work")
	gitIn(t, origin, "commit", "--allow-empty", "-m", "first")
	work := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "-q", origin, work).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	gitIn(t, origin, "commit", "--allow-empty", "-m", "upstream")
	gitIn(t, work, "fetch", "-q")
	gitIn(t, work, "commit", "--allow-empty", "-m", "local one")
	gitIn(t, work, "commit", "--allow-empty", "-m", "local two")
	writeFile(t, filepath.Join(work, "a.txt"), "one\n")
	gitIn(t, work, "add", "a.txt")
	gitIn(t, work, "stash")
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "git_stashes", Label: "s", LabelColor: "dim"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "git_ahead_behind", Color: "default"},
	})
	want := "\x1b[2ms\x1b[0m 1 \x1b[2m·\x1b[0m ↑2 ↓1\n"
	if out := draw(t, entries, inDir(work)); out != want {
		t.Fatalf("the stash and the drift read %q, want %q", out, want)
	}
}
