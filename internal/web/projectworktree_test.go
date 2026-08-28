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

	choices := worktreeBranches(context.Background(), projects, src)
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

	feature, ok := choiceByRef(worktreeBranches(context.Background(), projects, src), "feature")
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

	choices := worktreeBranches(context.Background(), projects, src)
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
