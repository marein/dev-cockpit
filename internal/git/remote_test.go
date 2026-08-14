package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// remotePair builds a working copy whose first commit is pushed to a local
// bare repository, with the upstream set. Everything a network push would do
// happens on the disk, so no test here ever talks to a real remote.
func remotePair(t *testing.T) (string, string) {
	t.Helper()
	work := t.TempDir()
	commitRepo(t, work)
	writeAt(t, work, "a.txt", "a\n")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-qm", "init")
	remote := t.TempDir()
	runGit(t, remote, "init", "-q", "--bare")
	runGit(t, work, "remote", "add", "origin", remote)
	runGit(t, work, "push", "-q", "-u", "origin", "HEAD")
	return work, remote
}

// cloneOf builds a second working copy of the same bare repository, the
// stand-in for somebody else pushing.
func cloneOf(t *testing.T, remote string) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, work, "clone", "-q", remote, ".")
	runGit(t, work, "config", "user.email", "t2@example.com")
	runGit(t, work, "config", "user.name", "t2")
	runGit(t, work, "config", "commit.gpgsign", "false")
	return work
}

// gitConfig reads one configuration value, empty when it is not set at all:
// what a refused push must not have written is exactly the absent case.
func gitConfig(t *testing.T, dir, key string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command("git", "config", "--get", key)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := New(dir).run(context.Background(), []string{"rev-parse", "HEAD"}, nil)
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestPushSendsTheBranchToItsUpstream(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")

	if err := New(dir).Push(context.Background(), false); err == nil {
		t.Fatal("a push without a destination must be refused")
	}

	remote := t.TempDir()
	runGit(t, remote, "init", "-q", "--bare")
	runGit(t, dir, "remote", "add", "origin", remote)
	runGit(t, dir, "push", "-q", "-u", "origin", "HEAD")

	writeAt(t, dir, "a.txt", "a2\n")
	if _, err := New(dir).Commit(context.Background(), "second", []string{"a.txt"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := New(dir).Push(context.Background(), false); err != nil {
		t.Fatalf("push: %v", err)
	}
	out, err := New(remote).run(context.Background(), []string{"rev-list", "--count", "--all"}, nil)
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if strings.TrimSpace(string(out)) != "2" {
		t.Fatalf("the upstream holds %s commits", strings.TrimSpace(string(out)))
	}
}

// A branch the sheet just created has no upstream, and git refuses that push
// with the line about setting one. The push sets it: the branch lands in the
// bare repository, the configuration says where it came from, and every push
// after it is an ordinary one.
func TestPushSetsTheUpstreamOfANewBranch(t *testing.T) {
	work, remote := remotePair(t)
	runGit(t, work, "switch", "-qc", "feature")
	writeAt(t, work, "f.txt", "f\n")
	if _, err := New(work).Commit(context.Background(), "feature", []string{"f.txt"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := New(work).Push(context.Background(), false); err != nil {
		t.Fatalf("push: %v", err)
	}

	if got := gitOut(t, remote, "rev-parse", "refs/heads/feature"); got != headOf(t, work) {
		t.Fatalf("the branch did not arrive: %s", got)
	}
	if got := gitConfig(t, work, "branch.feature.remote"); got != "origin" {
		t.Fatalf("branch.feature.remote is %q", got)
	}
	if got := gitConfig(t, work, "branch.feature.merge"); got != "refs/heads/feature" {
		t.Fatalf("branch.feature.merge is %q", got)
	}

	writeAt(t, work, "f.txt", "f2\n")
	if _, err := New(work).Commit(context.Background(), "second", []string{"f.txt"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := New(work).Push(context.Background(), false); err != nil {
		t.Fatalf("the second push: %v", err)
	}
	if got := gitOut(t, remote, "rev-parse", "refs/heads/feature"); got != headOf(t, work) {
		t.Fatalf("the second push did not arrive: %s", got)
	}
}

// The upstream is set where there is none and nowhere else, so what the two
// cases put on the command line is the whole test: a branch that has one
// pushes where it is configured to, and nothing of ours is added to that call.
func TestPushSetsAnUpstreamOnlyWhereThereIsNone(t *testing.T) {
	pushArgs := func(t *testing.T, upstream string) string {
		t.Helper()
		dir := t.TempDir()
		args := filepath.Join(dir, "args")
		// A git that answers the two reads this path makes and writes down how
		// the push was called. The two leading arguments are the -c
		// core.quotepath=false every call carries.
		fakeGit(t, `shift 2
case "$1" in
  status) printf '# branch.head feature\000`+upstream+`' ;;
  remote) echo origin ;;
  push) echo "$@" > `+args+` ;;
esac
`)

		if err := New(dir).Push(context.Background(), false); err != nil {
			t.Fatalf("push: %v", err)
		}
		out, err := os.ReadFile(args)
		if err != nil {
			t.Fatalf("read the arguments: %v", err)
		}
		return strings.TrimSpace(string(out))
	}

	if got := pushArgs(t, ""); got != "push -u origin HEAD" {
		t.Fatalf("a branch without an upstream must get one: %q", got)
	}
	if got := pushArgs(t, `# branch.upstream origin/feature\000`); got != "push" {
		t.Fatalf("a branch that has an upstream pushes as it always did: %q", got)
	}
}

// Which remote a new branch belongs on is a decision about where somebody's
// work goes, so several remotes without an origin among them are left to git,
// and a refused push has written nothing.
func TestPushLeavesAnAmbiguousRemoteToGit(t *testing.T) {
	work, remote := remotePair(t)
	runGit(t, work, "remote", "rename", "origin", "first")
	runGit(t, work, "remote", "add", "second", t.TempDir())
	runGit(t, work, "switch", "-qc", "feature")
	writeAt(t, work, "f.txt", "f\n")
	if _, err := New(work).Commit(context.Background(), "feature", []string{"f.txt"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := New(work).Push(context.Background(), false); err == nil {
		t.Fatal("a branch without an upstream and without one remote to name must be refused")
	}
	if got := gitConfig(t, work, "branch.feature.remote"); got != "" {
		t.Fatalf("the refused push configured %q", got)
	}
	if got := gitOut(t, remote, "rev-list", "--count", "--all"); got != "1" {
		t.Fatalf("the refused push moved the remote: %s commits", got)
	}

	// origin among several is the one that is not a guess.
	runGit(t, work, "remote", "add", "origin", remote)
	if err := New(work).Push(context.Background(), false); err != nil {
		t.Fatalf("push: %v", err)
	}
	if got := gitOut(t, remote, "rev-parse", "refs/heads/feature"); got != headOf(t, work) {
		t.Fatalf("the branch did not arrive: %s", got)
	}
	if got := gitConfig(t, work, "branch.feature.remote"); got != "origin" {
		t.Fatalf("branch.feature.remote is %q", got)
	}
}

// A detached HEAD is no branch, so there is nothing to configure and nothing
// is: git refuses it in its own words like it always did.
func TestPushOnADetachedHeadSetsNothing(t *testing.T) {
	work, remote := remotePair(t)
	runGit(t, work, "checkout", "-q", "--detach")
	before := gitOut(t, work, "config", "--local", "--list")

	if err := New(work).Push(context.Background(), false); err == nil {
		t.Fatal("a detached HEAD must be refused")
	}
	if got := gitOut(t, work, "config", "--local", "--list"); got != before {
		t.Fatalf("the refused push wrote configuration:\n%s", got)
	}
	if got := gitOut(t, remote, "rev-list", "--count", "--all"); got != "1" {
		t.Fatalf("the refused push moved the remote: %s commits", got)
	}
}

func TestForcePushKeepsTheLease(t *testing.T) {
	work, remote := remotePair(t)

	// A rewritten tip is no fast forward, so the plain push refuses and the
	// force one, with the remote exactly where this repository saw it last,
	// goes through.
	runGit(t, work, "commit", "-q", "--amend", "-m", "rewritten")
	if err := New(work).Push(context.Background(), false); err == nil {
		t.Fatal("a plain push must refuse a rewritten tip")
	}
	if err := New(work).Push(context.Background(), true); err != nil {
		t.Fatalf("force push: %v", err)
	}

	// Somebody else pushes; this repository has not fetched it, so the lease
	// has to refuse the overwrite and the remote keeps their work.
	other := cloneOf(t, remote)
	writeAt(t, other, "theirs.txt", "theirs\n")
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-qm", "theirs")
	runGit(t, other, "push", "-q")
	theirs := headOf(t, other)

	writeAt(t, work, "a.txt", "mine\n")
	if _, err := New(work).Commit(context.Background(), "mine", []string{"a.txt"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := New(work).Push(context.Background(), true); err == nil {
		t.Fatal("the lease must refuse to overwrite work this repository has not seen")
	}
	if got := headOf(t, remote); got != theirs {
		t.Fatalf("the remote lost their work: %s", got)
	}
}

func TestFetchCountsAheadAndBehind(t *testing.T) {
	work, remote := remotePair(t)

	// One commit of our own, one of theirs: ahead moves without any network,
	// behind only once the fetch has brought their ref home.
	writeAt(t, work, "mine.txt", "mine\n")
	if _, err := New(work).Commit(context.Background(), "mine", []string{"mine.txt"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}
	other := cloneOf(t, remote)
	writeAt(t, other, "theirs.txt", "theirs\n")
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-qm", "theirs")
	runGit(t, other, "push", "-q")

	before, err := New(work).Changes(context.Background())
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if !before.Branch.Counted || before.Branch.Ahead != 1 || before.Branch.Behind != 0 {
		t.Fatalf("before the fetch: %+v", before.Branch)
	}
	if ran, err := New(work).Fetch(context.Background()); err != nil || !ran {
		t.Fatalf("fetch: ran=%v err=%v", ran, err)
	}
	after, err := New(work).Changes(context.Background())
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if after.Branch.Ahead != 1 || after.Branch.Behind != 1 {
		t.Fatalf("after the fetch: %+v", after.Branch)
	}
	if after.Branch.Upstream == "" || after.Branch.Detached {
		t.Fatalf("branch: %+v", after.Branch)
	}
}

func TestFetchWithoutARemoteIsANormalState(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	// Nothing to fetch is a state, not a failure, and it has to say that it
	// did nothing: the route publishes a git event on a fetch that ran.
	if ran, err := New(dir).Fetch(context.Background()); err != nil || ran {
		t.Fatalf("fetch without a remote: ran=%v err=%v", ran, err)
	}
	if ran, err := New(dir).FetchIfStale(context.Background(), 0); err != nil || ran {
		t.Fatalf("nothing to fetch: ran=%v err=%v", ran, err)
	}
}

func TestFetchIfStaleSkipsAFreshFetch(t *testing.T) {
	work, _ := remotePair(t)

	ran, err := New(work).FetchIfStale(context.Background(), 0)
	if err != nil || !ran {
		t.Fatalf("a never fetched repository is stale: ran=%v err=%v", ran, err)
	}
	ran, err = New(work).FetchIfStale(context.Background(), time.Hour)
	if err != nil || ran {
		t.Fatalf("a fetch a moment old is fresh: ran=%v err=%v", ran, err)
	}
}

// FetchIfStale asks whether there is a remote at all before it looks at
// FETCH_HEAD's age, so the fetch behind it must not ask again: that is a
// second git process on a path the git sheet and the branch list take on
// every open.
func TestFetchIfStaleAsksForTheRemotesOnce(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	// A git that answers the three calls this path makes and writes down which
	// subcommand it was asked for. The two leading arguments are the -c
	// core.quotepath=false every call carries.
	fakeGit(t, `shift 2
echo "$1" >> `+calls+`
case "$1" in
  rev-parse) printf '%s\n\n' `+filepath.Join(dir, ".git")+` ;;
  remote) echo origin ;;
esac
`)

	ran, err := New(dir).FetchIfStale(context.Background(), time.Hour)

	if err != nil || !ran {
		t.Fatalf("a repository that never fetched is stale: ran=%v err=%v", ran, err)
	}
	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("read the calls: %v", err)
	}
	if want := "rev-parse\nremote\nfetch\n"; string(got) != want {
		t.Fatalf("the calls were\n%q\ninstead of\n%q", got, want)
	}
}

func TestCloneFillsAnEmptyDirectoryAndOnlyThat(t *testing.T) {
	_, remote := remotePair(t)

	empty := t.TempDir()
	if err := New(empty).Clone(context.Background(), remote); err != nil {
		t.Fatalf("clone: %v", err)
	}
	changes, err := New(empty).Changes(context.Background())
	if err != nil || !changes.Repo || len(changes.Worktree) != 0 {
		t.Fatalf("the clone is not a clean repository: %+v %v", changes, err)
	}

	taken := t.TempDir()
	writeAt(t, taken, "keep.txt", "keep\n")
	if err := New(taken).Clone(context.Background(), remote); err == nil {
		t.Fatal("a directory that holds anything must be refused")
	}
	if err := New(taken).Clone(context.Background(), ""); err == nil {
		t.Fatal("an empty URL must be refused")
	}

	if err := New(t.TempDir()).Clone(context.Background(), "--upload-pack=/bin/true"); err == nil {
		t.Fatal("a URL that reads as an option must be refused")
	}
}

// The dangerous transports are schemes and not options, so the "--" in front
// of the URL does nothing about them: ext:: runs whatever command the URL
// carries. Whether git refuses that on its own is the host's configuration,
// and that is exactly what must not be the answer here, so the whitelist
// rides on every call. The host is made permissive on purpose below, the way
// one that enables an ext helper for its own reasons would be.
func TestCloneRefusesATransportThatRunsCommands(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.ext.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")

	err := New(t.TempDir()).Clone(context.Background(), "ext::touch "+marker)

	if err == nil {
		t.Fatal("a clone over ext:: must be refused")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the transport ran the command in the URL")
	}
}

func TestPullFastForwardsAndNothingElse(t *testing.T) {
	work, remote := remotePair(t)
	other := cloneOf(t, remote)
	writeAt(t, other, "theirs.txt", "theirs\n")
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-qm", "theirs")
	runGit(t, other, "push", "-q")

	if err := New(work).Pull(context.Background()); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got := headOf(t, work); got != headOf(t, remote) {
		t.Fatalf("the pull did not arrive: %s", got)
	}

	// Diverged: their next commit and ours share no line. The pull must
	// refuse instead of merging, and HEAD must stand where it stood.
	writeAt(t, other, "theirs.txt", "more\n")
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-qm", "more")
	runGit(t, other, "push", "-q")
	writeAt(t, work, "mine.txt", "mine\n")
	if _, err := New(work).Commit(context.Background(), "mine", []string{"mine.txt"}, false); err != nil {
		t.Fatalf("commit: %v", err)
	}
	before := headOf(t, work)
	if err := New(work).Pull(context.Background()); err == nil {
		t.Fatal("a diverged branch must not be pulled")
	}
	if got := headOf(t, work); got != before {
		t.Fatalf("the refused pull moved HEAD: %s", got)
	}
}
