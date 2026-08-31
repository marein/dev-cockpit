package git

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func refByName(refs []Ref, name string) (Ref, bool) {
	for _, ref := range refs {
		if ref.Name == name {
			return ref, true
		}
	}
	return Ref{}, false
}

// allKinds is what the revision picker asks for; the branch picker leaves the
// tags and the commits out.
var allKinds = []string{KindBranch, KindRemote, KindTag, KindCommit}

// seedRepo is a repository with one commit in it, which is what every name
// here hangs off: a branch cannot be created before the first one.
func seedRepo(t *testing.T, dir string) {
	t.Helper()
	commitRepo(t, dir)
	writeAt(t, dir, "seed.txt", "seed\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "seed")
}

// commitAt records an empty commit with a date of its own, which is what the
// recency sort reads. Two commits made in the same second are a tie, and a
// check that needs an order cannot be built on one.
func commitAt(t *testing.T, dir, when, message string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command("git", "commit", "-q", "--allow-empty", "-m", message)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
}

func TestRefsListsBranchesRemotesAndTags(t *testing.T) {
	work, remote := remotePair(t)
	runGit(t, work, "tag", "v1.0")
	runGit(t, work, "push", "-q", "origin", "v1.0")
	runGit(t, work, "switch", "-qc", "feature/deep")
	writeAt(t, work, "f.txt", "f\n")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-qm", "feature")
	runGit(t, work, "push", "-q", "-u", "origin", "feature/deep")
	// The clone carries origin/HEAD, the symbolic ref the list must not show.
	other := cloneOf(t, remote)

	found, err := New(other).Refs(context.Background(), RefSearch{Kinds: allKinds, Limit: 100})
	if err != nil {
		t.Fatalf("refs: %v", err)
	}
	if _, ok := refByName(found.Refs, "origin/HEAD"); ok {
		t.Fatalf("origin/HEAD is symbolic and stays out: %+v", found.Refs)
	}
	deep, ok := refByName(found.Refs, "origin/feature/deep")
	if !ok || deep.Kind != KindRemote || deep.Branch != "feature/deep" {
		t.Fatalf("the remote branch carries its local name: %+v", found.Refs)
	}
	tag, ok := refByName(found.Refs, "v1.0")
	if !ok || tag.Kind != KindTag {
		t.Fatalf("the tag is missing: %+v", found.Refs)
	}
	head := false
	for _, ref := range found.Refs {
		if ref.Head && ref.Kind == KindBranch {
			head = true
		}
	}
	if !head {
		t.Fatalf("the current branch carries the head mark: %+v", found.Refs)
	}
	// Nothing typed is the picker as it opens: the names it stands on, and no
	// commits at all.
	if len(found.Commits) != 0 {
		t.Fatalf("an empty search answers names only: %+v", found.Commits)
	}

	none, err := New(t.TempDir()).Refs(context.Background(), RefSearch{Kinds: allKinds, Limit: 100})
	if err != nil || len(none.Refs) != 0 || len(none.Commits) != 0 {
		t.Fatalf("no repository: %+v %v", none, err)
	}
}

// The limit belongs to one kind of name and not to all of them together: a
// repository full of tags must not push the branches out of the branch picker.
// An annotated tag rides along, because it is a tag object with no committer
// date, and sorting by that one would have parked it behind every lightweight
// tag whatever its age.
func TestRefsLimitsEachKindOnItsOwn(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	runGit(t, dir, "switch", "-qc", "wanted")
	for i := 0; i < 6; i++ {
		runGit(t, dir, "tag", "old-"+strconv.Itoa(i))
	}
	runGit(t, dir, "tag", "-a", "newest", "-m", "the annotated one")

	found, err := New(dir).Refs(context.Background(), RefSearch{Kinds: allKinds, Limit: 2})
	if err != nil {
		t.Fatalf("refs: %v", err)
	}
	kinds := map[string]int{}
	for _, ref := range found.Refs {
		kinds[ref.Kind]++
	}
	if kinds[KindBranch] != 2 {
		t.Fatalf("the branches must fill their own count, not compete with the tags: %+v", found.Refs)
	}
	if kinds[KindTag] != 2 {
		t.Fatalf("the tags carry the same count on their own: %+v", found.Refs)
	}
	if _, ok := refByName(found.Refs, "wanted"); !ok {
		t.Fatalf("the branch is missing behind the tags: %+v", found.Refs)
	}
	if _, ok := refByName(found.Refs, "newest"); !ok {
		t.Fatalf("the annotated tag is the newest and must be in: %+v", found.Refs)
	}
}

// The kinds are the caller's, so the branch picker never has to throw tags
// away that it asked for by accident.
func TestRefsAnswersOnlyTheAskedKinds(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir)
	runGit(t, dir, "tag", "v9")

	found, err := New(dir).Refs(context.Background(), RefSearch{Kinds: []string{KindBranch, KindRemote}, Limit: 50})
	if err != nil {
		t.Fatalf("refs: %v", err)
	}
	if _, ok := refByName(found.Refs, "v9"); ok {
		t.Fatalf("a tag came back for a picker that asked for branches: %+v", found.Refs)
	}
}

// The search is git's answer and not the client's filter, and it has to reach
// a name below the cap: that is the whole reason it moved to the server.
func TestRefsSearchFindsBeyondTheCap(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	// A branch sorts by the date of the commit it points at, so the noise has
	// to sit on younger commits, not merely be created later: branches made
	// from one commit all carry that commit's date and sort as a tie.
	commitAt(t, dir, "2020-01-01T00:00:00Z", "the old one")
	runGit(t, dir, "branch", "feature/needle")
	for i := 0; i < 8; i++ {
		commitAt(t, dir, "2021-01-0"+strconv.Itoa(i+1)+"T00:00:00Z", "noise "+strconv.Itoa(i))
		runGit(t, dir, "branch", "noise-"+strconv.Itoa(i))
	}

	open, err := New(dir).Refs(context.Background(), RefSearch{Kinds: allKinds, Limit: 3})
	if err != nil {
		t.Fatalf("refs: %v", err)
	}
	if _, ok := refByName(open.Refs, "feature/needle"); ok {
		t.Fatalf("the oldest branch must sit outside the cap for this check: %+v", open.Refs)
	}

	// "needle" sits behind a slash, which for-each-ref's own pattern would
	// never match: its "*" does not cross a directory separator.
	found, err := New(dir).Refs(context.Background(), RefSearch{Text: "NEEDLE", Kinds: allKinds, Limit: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if _, ok := refByName(found.Refs, "feature/needle"); !ok {
		t.Fatalf("the search must find a name outside the cap, case insensitively: %+v", found.Refs)
	}
	for _, ref := range found.Refs {
		if !strings.Contains(strings.ToLower(ref.Name), "needle") {
			t.Fatalf("only the hits come back: %+v", found.Refs)
		}
	}
}

// The name match is the token search the whole app shares: lowercased, the
// query split on whitespace, every token contained somewhere. A branch name is
// a path, and somebody typing the pieces they remember must not be answered
// with nothing because they typed them apart.
func TestRefsSearchMatchesEveryToken(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir)
	runGit(t, dir, "branch", "feature/alpha-login")
	runGit(t, dir, "branch", "release/beta-logout")

	found, err := New(dir).Refs(context.Background(), RefSearch{Text: "  LOGIN   feature ", Kinds: []string{KindBranch}, Limit: 20})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found.Refs) != 1 || found.Refs[0].Name != "feature/alpha-login" {
		t.Fatalf("the tokens must match in any order, any case, whatever the spacing: %+v", found.Refs)
	}

	// Every token has to be there. A query that only half fits is a miss, not
	// the half that fits.
	miss, err := New(dir).Refs(context.Background(), RefSearch{Text: "alpha logout", Kinds: []string{KindBranch}, Limit: 20})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(miss.Refs) != 0 {
		t.Fatalf("a token that matches nothing must take the whole query with it: %+v", miss.Refs)
	}
}

// The cap belongs to the matches and stays there with the token match: a
// picker asks for a page of names and gets a page.
func TestRefsSearchCapsTheMatches(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir)
	for i := 0; i < 6; i++ {
		runGit(t, dir, "branch", "wip/needle-"+strconv.Itoa(i))
	}

	found, err := New(dir).Refs(context.Background(), RefSearch{Text: "needle wip", Kinds: []string{KindBranch}, Limit: 4})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found.Refs) != 4 {
		t.Fatalf("the cap applies to the matches: %+v", found.Refs)
	}
}

// A commit is the case the picker was missing: found by a piece of its
// subject, found by its hash prefix, and answered with what a row shows.
func TestRefsSearchFindsCommits(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir)
	writeAt(t, dir, "a.txt", "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "Teach the picker about commits")
	head := gitOut(t, dir, "rev-parse", "HEAD")

	bySubject, err := New(dir).Refs(context.Background(), RefSearch{Text: "picker about", Kinds: allKinds, Limit: 20})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(bySubject.Commits) != 1 || bySubject.Commits[0].SHA != head {
		t.Fatalf("the subject search missed the commit: %+v", bySubject.Commits)
	}
	one := bySubject.Commits[0]
	if one.Short != head[:7] || one.Author == "" || one.Time == 0 || one.Summary == "" {
		t.Fatalf("a commit row shows short hash, subject, author and date: %+v", one)
	}

	byHash, err := New(dir).Refs(context.Background(), RefSearch{Text: head[:8], Kinds: allKinds, Limit: 20})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(byHash.Commits) != 1 || byHash.Commits[0].SHA != head {
		t.Fatalf("a hash prefix must resolve to its commit: %+v", byHash.Commits)
	}

	// The text is somebody's typing and goes into a git argument: it may be
	// read neither as an option nor as a pattern, and a miss is an empty
	// answer and never an error.
	for _, text := range []string{"--all", "-i", "Teach.the.picker", "^(", "zzzz"} {
		miss, err := New(dir).Refs(context.Background(), RefSearch{Text: text, Kinds: allKinds, Limit: 20})
		if err != nil {
			t.Fatalf("search %q: %v", text, err)
		}
		if len(miss.Commits) != 0 {
			t.Fatalf("search %q found something: %+v", text, miss.Commits)
		}
	}

	// A picker that does not ask for commits never gets any.
	names, err := New(dir).Refs(context.Background(), RefSearch{Text: "picker about", Kinds: []string{KindBranch, KindRemote}, Limit: 20})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(names.Commits) != 0 {
		t.Fatalf("the branch picker stays with names: %+v", names.Commits)
	}
}

// A repository without a first commit has nothing to search and says so with
// an empty answer, like every history question here.
func TestRefsSearchOnUnbornRepository(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)

	found, err := New(dir).Refs(context.Background(), RefSearch{Text: "anything", Kinds: allKinds, Limit: 20})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found.Refs) != 0 || len(found.Commits) != 0 {
		t.Fatalf("an unborn repository answers empty: %+v", found)
	}
}

// BranchNames answers the whole namespace and never a page of it: it is what
// a caller has to know about every local branch, and a cap would make its
// answer a maybe.
func TestBranchNamesAnswersEveryLocalBranch(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir)
	for _, name := range []string{"alpha", "wip/beta", "wip/gamma"} {
		runGit(t, dir, "branch", name)
	}

	names, err := New(dir).BranchNames(context.Background())
	if err != nil {
		t.Fatalf("branch names: %v", err)
	}
	have := map[string]bool{}
	for _, name := range names {
		have[name] = true
	}
	for _, want := range []string{"alpha", "wip/beta", "wip/gamma"} {
		if !have[want] {
			t.Fatalf("%q is missing: %v", want, names)
		}
	}
	// The branch the first commit landed on is in there too, whatever this
	// git calls it.
	if len(names) != 4 {
		t.Fatalf("the head's own branch is missing: %v", names)
	}

	// A directory without a repository answers none and no error, like Refs.
	none, err := New(t.TempDir()).BranchNames(context.Background())
	if err != nil || len(none) != 0 {
		t.Fatalf("a plain directory answered %v, %v", none, err)
	}
}
