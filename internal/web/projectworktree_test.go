package web

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/internal/web/render"
)

func worktreeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// worktreeSourceRepo makes a project directory that is a repository with one
// commit on master and a second branch, the source every check here forks.
func worktreeSourceRepo(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, dir, "init", "-q", "-b", "master")
	worktreeGit(t, dir, "config", "user.email", "t@example.com")
	worktreeGit(t, dir, "config", "user.name", "t")
	worktreeGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, dir, "add", "-A")
	worktreeGit(t, dir, "commit", "-qm", "init")
	worktreeGit(t, dir, "branch", "feature")
	return dir
}

func worktreeProjects(t *testing.T, root string) *project.Repository {
	t.Helper()
	return project.NewRepository(root, recent.New(filepath.Join(t.TempDir(), "recent.json")))
}

func choiceByRef(choices []render.BranchChoice, ref string) (render.BranchChoice, bool) {
	for _, c := range choices {
		if c.Ref == ref {
			return c, true
		}
	}
	return render.BranchChoice{}, false
}

// The form has to say which branches are unavailable before somebody picks
// one, and it names them by the project that holds them, not by a path.
func TestWorktreeBranchesNameTheProjectThatHoldsABranch(t *testing.T) {
	root := t.TempDir()
	source := worktreeSourceRepo(t, root, "app")
	worktreeGit(t, source, "worktree", "add", filepath.Join(root, "app-feature"), "feature")
	projects := worktreeProjects(t, root)
	src, err := projects.FindByName("app")
	if err != nil {
		t.Fatal(err)
	}

	choices := worktreeBranches(context.Background(), projects, src, "", worktreeBranchLimit, false)
	master, ok := choiceByRef(choices, "master")
	if !ok {
		t.Fatalf("master is not offered: %+v", choices)
	}
	if master.Taken != "app" {
		t.Fatalf("master is held by %q, want the source project", master.Taken)
	}
	if !master.Head {
		t.Fatal("the branch the source stands on is not marked")
	}
	feature, ok := choiceByRef(choices, "feature")
	if !ok {
		t.Fatalf("feature is not offered: %+v", choices)
	}
	if feature.Taken != "app-feature" {
		t.Fatalf("feature is held by %q, want the worktree project", feature.Taken)
	}
}

// A worktree outside the projects root is no project, so it is named by the
// only thing it has, its path.
func TestWorktreeBranchesNameAForeignWorktreeByItsPath(t *testing.T) {
	root := t.TempDir()
	source := worktreeSourceRepo(t, root, "app")
	outside := filepath.Join(t.TempDir(), "elsewhere")
	worktreeGit(t, source, "worktree", "add", outside, "feature")
	projects := worktreeProjects(t, root)
	src, err := projects.FindByName("app")
	if err != nil {
		t.Fatal(err)
	}

	feature, ok := choiceByRef(worktreeBranches(context.Background(), projects, src, "", worktreeBranchLimit, false), "feature")
	if !ok {
		t.Fatal("feature is not offered")
	}
	if feature.Taken != outside {
		t.Fatalf("feature is held by %q, want the path %q", feature.Taken, outside)
	}
}

// A branch that exists on a remote is offered so it can be worked on, but
// only while there is no local branch of that name: the local one is already
// in the list and both would end in the same working copy.
func TestWorktreeBranchesOfferARemoteBranchOnlyWithoutItsLocalOne(t *testing.T) {
	root := t.TempDir()
	origin := worktreeSourceRepo(t, t.TempDir(), "origin")
	worktreeGit(t, origin, "branch", "shared")
	worktreeGit(t, root, "clone", "-q", origin, "app")
	projects := worktreeProjects(t, root)
	src, err := projects.FindByName("app")
	if err != nil {
		t.Fatal(err)
	}

	choices := worktreeBranches(context.Background(), projects, src, "", worktreeBranchLimit, false)
	shared, ok := choiceByRef(choices, "origin/shared")
	if !ok {
		t.Fatalf("the remote branch is not offered: %+v", choices)
	}
	if !shared.Remote || shared.Name != "shared" {
		t.Fatalf("the remote branch does not name its local branch: %+v", shared)
	}
	if _, ok := choiceByRef(choices, "origin/master"); ok {
		t.Fatalf("a remote branch whose local branch exists is offered twice: %+v", choices)
	}
}

// The pickers search on the server, and the match is the token search the
// rest of the app uses: lowercased, split on whitespace, every token
// somewhere in the name. A plain substring match answers nothing for the
// pieces of a name typed in the order they are remembered.
func TestWorktreeBranchesSearchMatchesEveryToken(t *testing.T) {
	root := t.TempDir()
	source := worktreeSourceRepo(t, root, "app")
	worktreeGit(t, source, "branch", "topic/alpha-login")
	worktreeGit(t, source, "branch", "topic/beta-logout")
	projects := worktreeProjects(t, root)
	src, err := projects.FindByName("app")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	hits := worktreeBranches(ctx, projects, src, "LOGIN topic", worktreeBranchPage, false)
	if len(hits) != 1 || hits[0].Ref != "topic/alpha-login" {
		t.Fatalf("the tokens must match in any order and any case: %+v", hits)
	}
	if got := worktreeBranches(ctx, projects, src, "alpha beta", worktreeBranchPage, false); len(got) != 0 {
		t.Fatalf("every token has to be contained: %+v", got)
	}
	if got := worktreeBranches(ctx, projects, src, "topic", worktreeBranchPage, false); len(got) != 2 {
		t.Fatalf("one token is a plain contains: %+v", got)
	}
}

// The rule that keeps a remote branch out while its local one exists cannot be
// read off the hits: under a search the local branch need not have matched,
// and offering the remote would offer a create git refuses.
func TestWorktreeBranchesSearchKeepsARemoteOutWhoseLocalBranchDidNotMatch(t *testing.T) {
	root := t.TempDir()
	origin := worktreeSourceRepo(t, t.TempDir(), "origin")
	worktreeGit(t, origin, "branch", "shared")
	worktreeGit(t, root, "clone", "-q", origin, "app")
	app := filepath.Join(root, "app")
	worktreeGit(t, app, "branch", "shared", "origin/shared")
	projects := worktreeProjects(t, root)
	src, err := projects.FindByName("app")
	if err != nil {
		t.Fatal(err)
	}

	// "origin/shared" matches the remote ref and never the local branch, which
	// is exactly the query the rule has to survive.
	hits := worktreeBranches(context.Background(), projects, src, "origin/shared", worktreeBranchPage, false)
	if _, ok := choiceByRef(hits, "origin/shared"); ok {
		t.Fatalf("the remote branch is offered beside a local branch of that name: %+v", hits)
	}
}

// The starting point is the one list where a remote branch stands beside its
// local one: the two can have drifted apart, and a new branch beginning at one
// is not the same branch as one beginning at the other.
func TestWorktreeBranchesKeepARemoteBesideItsLocalOneForAStart(t *testing.T) {
	root := t.TempDir()
	origin := worktreeSourceRepo(t, t.TempDir(), "origin")
	worktreeGit(t, root, "clone", "-q", origin, "app")
	projects := worktreeProjects(t, root)
	src, err := projects.FindByName("app")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	checkout := worktreeBranches(ctx, projects, src, "master", worktreeBranchPage, false)
	if _, ok := choiceByRef(checkout, "origin/master"); ok {
		t.Fatalf("the checkout list offers a remote branch whose local one exists: %+v", checkout)
	}
	starts := worktreeBranches(ctx, projects, src, "master", worktreeBranchPage, true)
	if _, ok := choiceByRef(starts, "origin/master"); !ok {
		t.Fatalf("the start list drops the remote branch: %+v", starts)
	}
	if _, ok := choiceByRef(starts, "master"); !ok {
		t.Fatalf("the start list drops the local branch: %+v", starts)
	}
}

// A branch row carries how far it stands from the branch it follows, so a
// starting point that is a week old says so before it is picked.
func TestWorktreeBranchesCarryTheDistanceToTheUpstream(t *testing.T) {
	root := t.TempDir()
	origin := worktreeSourceRepo(t, t.TempDir(), "origin")
	worktreeGit(t, root, "clone", "-q", origin, "app")
	app := filepath.Join(root, "app")
	// The remote moves on twice, this working copy fetches but never pulls.
	worktreeGit(t, origin, "commit", "-q", "--allow-empty", "-m", "one")
	worktreeGit(t, origin, "commit", "-q", "--allow-empty", "-m", "two")
	worktreeGit(t, app, "fetch", "-q")
	projects := worktreeProjects(t, root)
	src, err := projects.FindByName("app")
	if err != nil {
		t.Fatal(err)
	}

	choices := worktreeBranches(context.Background(), projects, src, "", worktreeBranchLimit, false)
	master, ok := choiceByRef(choices, "master")
	if !ok {
		t.Fatalf("master is not offered: %+v", choices)
	}
	if master.Upstream != "origin/master" || master.Behind != 2 || master.Ahead != 0 {
		t.Fatalf("the distance to the upstream is wrong: %+v", master)
	}
	// A branch that follows nothing has nothing to say.
	worktreeGit(t, app, "branch", "solo")
	solo, ok := choiceByRef(worktreeBranches(context.Background(), projects, src, "solo", worktreeBranchPage, false), "solo")
	if !ok {
		t.Fatal("solo is not offered")
	}
	if solo.Upstream != "" || solo.Ahead != 0 || solo.Behind != 0 {
		t.Fatalf("a branch without an upstream carries a distance: %+v", solo)
	}
}

func TestWorktreePlanResolvesTheBranchChoice(t *testing.T) {
	root := t.TempDir()
	origin := worktreeSourceRepo(t, t.TempDir(), "origin")
	worktreeGit(t, origin, "branch", "shared")
	worktreeGit(t, root, "clone", "-q", origin, "app")
	projects := worktreeProjects(t, root)
	src, err := projects.FindByName("app")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	plan, err := worktreePlan(ctx, src, projectCreateForm{BranchMode: "existing", Branch: "master"})
	if err != nil || plan.Branch != "master" || plan.Start != "" {
		t.Fatalf("an existing branch answered %+v, %v", plan, err)
	}

	// A remote branch is not checked out as itself, it becomes the local
	// branch that follows it.
	plan, err = worktreePlan(ctx, src, projectCreateForm{BranchMode: "existing", Branch: "origin/shared"})
	if err != nil || plan.Branch != "shared" || plan.Start != "origin/shared" {
		t.Fatalf("a remote branch answered %+v, %v", plan, err)
	}

	plan, err = worktreePlan(ctx, src, projectCreateForm{BranchMode: "new", NewBranch: "wip", Start: "master"})
	if err != nil || plan.Branch != "wip" || plan.Start != "master" {
		t.Fatalf("a new branch answered %+v, %v", plan, err)
	}
}

// Everything the form can name is checked against the repository first, so
// nothing that is gone travels into a git argument as if it stood.
func TestWorktreePlanRefusesWhatTheRepositoryDoesNotHave(t *testing.T) {
	root := t.TempDir()
	worktreeSourceRepo(t, root, "app")
	projects := worktreeProjects(t, root)
	src, err := projects.FindByName("app")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	for _, form := range []projectCreateForm{
		{BranchMode: "existing", Branch: "gone"},
		{BranchMode: "existing"},
		{BranchMode: "new", NewBranch: "wip", Start: "gone"},
		{BranchMode: "new", NewBranch: "wip"},
		{BranchMode: "new", Start: "master"},
	} {
		if plan, err := worktreePlan(ctx, src, form); err == nil {
			t.Fatalf("%+v was accepted as %+v", form, plan)
		}
	}
}

// The checkbox is a wish. A branch that is purely behind is caught up, and
// every other state is left exactly where it is, because moving it would be a
// merge and nobody decides a merge on a create form.
func TestCatchUpWorktreeMovesOnlyABranchThatIsPurelyBehind(t *testing.T) {
	build := func(t *testing.T) (string, string) {
		t.Helper()
		root := t.TempDir()
		origin := worktreeSourceRepo(t, t.TempDir(), "origin")
		worktreeGit(t, root, "clone", "-q", origin, "app")
		app := filepath.Join(root, "app")
		worktreeGit(t, app, "config", "user.email", "t@example.com")
		worktreeGit(t, app, "config", "user.name", "t")
		worktreeGit(t, app, "config", "commit.gpgsign", "false")
		return origin, app
	}
	ctx := context.Background()

	// Purely behind: the remote moved twice, this copy never pulled.
	origin, app := build(t)
	worktreeGit(t, origin, "commit", "-q", "--allow-empty", "-m", "one")
	worktreeGit(t, origin, "commit", "-q", "--allow-empty", "-m", "two")
	worktreeGit(t, app, "fetch", "-q")
	want := strings.TrimSpace(gitOutput(t, app, "rev-parse", "origin/master"))
	onto, err := catchUpWorktree(ctx, app, "master")
	if err != nil || onto != "origin/master" {
		t.Fatalf("a branch that only fell behind was not caught up: %q %v", onto, err)
	}
	if got := strings.TrimSpace(gitOutput(t, app, "rev-parse", "HEAD")); got != want {
		t.Fatalf("the working copy stands at %s, want the upstream %s", got, want)
	}

	// The wish is set but there is nothing to do any more: level with the
	// upstream, which is the state right after the catch up above.
	onto, err = catchUpWorktree(ctx, app, "master")
	if err != nil || onto != "" {
		t.Fatalf("a branch that stands level was moved: %q %v", onto, err)
	}

	// Diverged and ahead both keep their own commits.
	for _, c := range []struct {
		why    string
		remote int
	}{{"ahead only", 0}, {"diverged", 2}} {
		origin, app := build(t)
		for i := 0; i < c.remote; i++ {
			worktreeGit(t, origin, "commit", "-q", "--allow-empty", "-m", "theirs")
		}
		worktreeGit(t, app, "commit", "-q", "--allow-empty", "-m", "mine")
		worktreeGit(t, app, "fetch", "-q")
		before := strings.TrimSpace(gitOutput(t, app, "rev-parse", "HEAD"))
		onto, err := catchUpWorktree(ctx, app, "master")
		if err != nil || onto != "" {
			t.Fatalf("%s: the branch was moved: %q %v", c.why, onto, err)
		}
		if got := strings.TrimSpace(gitOutput(t, app, "rev-parse", "HEAD")); got != before {
			t.Fatalf("%s: the working copy moved from %s to %s", c.why, before, got)
		}
	}

	// A branch that follows nothing has nothing to catch up to.
	_, plain := build(t)
	worktreeGit(t, plain, "branch", "solo")
	worktreeGit(t, plain, "switch", "-q", "solo")
	if onto, err := catchUpWorktree(ctx, plain, "solo"); err != nil || onto != "" {
		t.Fatalf("a branch without an upstream was moved: %q %v", onto, err)
	}
}

// gitOutput runs one read and answers what git printed.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

func TestNewProjectPathCarriesTheWholeForm(t *testing.T) {
	if got := newProjectPath(projectCreateForm{}); got != "/projects/new" {
		t.Fatalf("an empty form is not the plain address: %q", got)
	}
	if got := newProjectPath(projectCreateForm{Create: render.CreateClone}); got != "/projects/new?create=clone" {
		t.Fatalf("the clone choice is not carried: %q", got)
	}
	// A refused create has to come back with what was typed, every field of
	// it, or the refusal costs the whole form.
	got := newProjectPath(projectCreateForm{
		Create:     render.WorktreeChoice("my app"),
		Name:       "app-wip",
		BranchMode: "new",
		NewBranch:  "wip/one",
		Start:      "origin/master",
	})
	for _, want := range []string{"create=worktree%3Amy+app", "project_name=app-wip", "branch_mode=new", "new_branch=wip%2Fone", "start=origin%2Fmaster"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the way back lost %q: %s", want, got)
		}
	}
	if got := newProjectPath(projectCreateForm{Create: render.CreateClone, CloneURL: "https://example.com/x.git"}); !strings.Contains(got, "clone_url=https%3A%2F%2Fexample.com%2Fx.git") {
		t.Fatalf("the url is not carried: %s", got)
	}
}

// Only a main repository can be forked. git would take a worktree too and
// register the new one in the same main, but that is the same sibling under a
// name that reads like a chain, so neither the list nor the create takes one.
func TestOnlyMainRepositoriesAreOffered(t *testing.T) {
	root := t.TempDir()
	source := worktreeSourceRepo(t, root, "app")
	worktreeGit(t, source, "worktree", "add", filepath.Join(root, "app-feature"), "feature")
	if err := os.MkdirAll(filepath.Join(root, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	projects := worktreeProjects(t, root)

	nav := []render.ProjectOption{{Name: "app"}, {Name: "app-feature"}, {Name: "plain"}}
	got := worktreeSources(projects.List(), nav)
	if len(got) != 1 || got[0].Name != "app" {
		t.Fatalf("the form offers %v, want the main repository alone", got)
	}

	worktree, err := projects.FindByName("app-feature")
	if err != nil {
		t.Fatal(err)
	}
	refusal := worktreeSourceRefusal(worktree).Error()
	if !strings.Contains(refusal, `"app-feature" is itself a worktree of "app"`) {
		t.Fatalf("the refusal does not say where the fork belongs: %s", refusal)
	}
}
