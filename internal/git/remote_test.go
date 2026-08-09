package git

import (
	"context"
	"os"
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
