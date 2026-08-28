package render

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/project"
)

// renderProjects executes the real projects list with one row.
func renderProjects(t *testing.T, data ProjectsListData) string {
	t.Helper()
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "projects_list.gohtml", data); err != nil {
		t.Fatalf("render projects list: %v", err)
	}
	return out.String()
}

func projectsData(deleting map[string]ProjectDelete) ProjectsListData {
	return ProjectsListData{
		Page:     Page{Title: "Projects"},
		Projects: []project.Project{{Name: "app", Path: "/tmp/app", Label: "app"}},
		Deleting: deleting,
	}
}

// A deletion that runs past its request takes the row's actions: nothing there
// can be pressed twice, and the row says what it is doing instead.
func TestProjectRowSaysItIsBeingDeleted(t *testing.T) {
	out := renderProjects(t, projectsData(map[string]ProjectDelete{"app": {Running: true}}))
	if !strings.Contains(out, "data-project-deleting") || !strings.Contains(out, "Deleting") {
		t.Fatal("the row does not say it is being deleted")
	}
	for _, gone := range []string{`action="/projects/delete"`, "/projects/app/editor", "/coders/new?project=app"} {
		if strings.Contains(out, gone) {
			t.Fatalf("a working row still offers %q", gone)
		}
	}
}

// The failure has nobody left to flash it, the request it belonged to is long
// answered, so it stands on the row until the next attempt.
func TestProjectRowKeepsTheDeleteFailure(t *testing.T) {
	out := renderProjects(t, projectsData(map[string]ProjectDelete{"app": {Failed: "permission denied"}}))
	if !strings.Contains(out, "permission denied") || !strings.Contains(out, "alert-danger") {
		t.Fatal("the failed deletion is not on the row")
	}
	if !strings.Contains(out, `action="/projects/delete"`) {
		t.Fatal("a failed deletion did not give the row its actions back")
	}
	if strings.Contains(out, "data-project-deleting") {
		t.Fatal("a finished deletion still reads as working")
	}
}

func TestDeleteWorktreeNote(t *testing.T) {
	p := project.Project{Name: "main", Path: "/p/main", GitWorktrees: []project.WorktreeRef{
		{Path: "/p/a", Project: "a"},
		{Path: "/p/b", Project: "b"},
		{Path: "/tmp/c"},
		{Path: "/p/main/.worktrees/d"},
	}}
	want := `The projects "a" and "b" are deleted too, because they are worktrees of this one. The worktree at /tmp/c loses its repository.`
	if got := DeleteWorktreeNote(p); got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
	single := project.Project{Name: "main", Path: "/p/main", GitWorktrees: []project.WorktreeRef{{Path: "/p/a", Project: "a"}}}
	if got := DeleteWorktreeNote(single); got != `The project "a" is deleted too, because it is a worktree of this one.` {
		t.Errorf("note = %q", got)
	}
	if got := DeleteWorktreeNote(project.Project{Name: "plain"}); got != "" {
		t.Errorf("note for a project without worktrees = %q, want empty", got)
	}
}

// The confirm of a main with worktree projects names what goes with it.
func TestProjectRowDeleteConfirmNamesWorktrees(t *testing.T) {
	data := projectsData(nil)
	data.Projects[0].GitWorktrees = []project.WorktreeRef{{Path: "/tmp/app-feature", Project: "app-feature"}}
	out := renderProjects(t, data)
	if !strings.Contains(out, "The project &#34;app-feature&#34; is deleted too, because it is a worktree of this one.") {
		t.Fatalf("the delete confirm does not name the worktree:\n%s", out)
	}
}

// Nothing going on is the ordinary case: no deletion state, the ordinary row.
func TestProjectRowWithoutDeletion(t *testing.T) {
	out := renderProjects(t, projectsData(nil))
	if strings.Contains(out, "data-project-deleting") || strings.Contains(out, "alert-danger") {
		t.Fatal("an untouched row renders deletion state")
	}
	if !strings.Contains(out, `action="/projects/delete"`) {
		t.Fatal("the row lost its delete form")
	}
}

// A worktree row shows where it comes from instead of the origin: the origin
// lives in the main's config, so it is the same url the main's row already
// carries. The main's name goes into the row's filter data as well, so
// filtering for a main finds the worktrees that belong to it.
func TestWorktreeRowCarriesItsMainInsteadOfTheOrigin(t *testing.T) {
	data := projectsData(nil)
	p := &data.Projects[0]
	p.GitRepo, p.GitBranch = true, "feature-a"
	p.GitOrigin, p.GitOriginURL = "github.com/marein/app", "https://github.com/marein/app"
	p.GitWorktree, p.GitWorktreeOf, p.GitWorktreeMain = true, "app-main", "/tmp/app-main"
	out := renderProjects(t, data)
	if !strings.Contains(out, `data-project-worktree-of="app-main"`) {
		t.Fatal("the row does not name its main for the filter")
	}
	if !strings.Contains(out, "/projects#project-app-main") {
		t.Fatal("the row does not link to its main")
	}
	if strings.Contains(out, "https://github.com/marein/app") {
		t.Fatal("the worktree row repeats the origin of its main")
	}

	p.GitWorktree, p.GitWorktreeOf, p.GitWorktreeMain = false, "", ""
	out = renderProjects(t, data)
	if strings.Contains(out, "data-project-worktree-of") || strings.Contains(out, "data-project-worktree") {
		t.Fatal("a plain repository renders worktree state")
	}
	if !strings.Contains(out, "https://github.com/marein/app") {
		t.Fatal("a plain repository lost its origin")
	}
	if !strings.Contains(out, "ti-brand-github") {
		t.Fatal("the origin link carries no icon")
	}
}

func TestOriginIconNamesTheForge(t *testing.T) {
	for origin, want := range map[string]string{
		"github.com/marein/app":     "ti-brand-github",
		"gitlab.example.com/team/x": "ti-brand-gitlab",
		"bitbucket.org/team/x":      "ti-brand-bitbucket",
		"git.example.com/team/x":    "ti-world",
		"":                          "ti-world",
	} {
		if got := OriginIcon(origin); got != want {
			t.Errorf("OriginIcon(%q) = %q, want %q", origin, got, want)
		}
	}
}

// renderProjectNew executes the real create form.
func renderProjectNew(t *testing.T, data ProjectNewData) string {
	t.Helper()
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "projects_new.gohtml", data); err != nil {
		t.Fatalf("render create form: %v", err)
	}
	return out.String()
}

func worktreeFormData() ProjectNewData {
	return ProjectNewData{
		Page:         Page{Title: "New Project"},
		Sources:      []ProjectOption{{Name: "app", Active: true, LastUsedUnix: 42}, {Name: "other"}},
		Create:       WorktreeChoice("app"),
		Source:       "app",
		SourceBranch: "master",
		Branches: []BranchChoice{
			{Ref: "master", Name: "master", Taken: "app", Head: true},
			{Ref: "feature", Name: "feature"},
			{Ref: "origin/shared", Name: "shared", Remote: true},
		},
	}
}

// Without a source the form is the plain create it has always been.
func TestCreateFormWithoutASourceOffersNoBranches(t *testing.T) {
	out := renderProjectNew(t, ProjectNewData{Page: Page{Title: "New Project"}, Sources: []ProjectOption{{Name: "app"}}})
	if strings.Contains(out, "data-branch-block") || strings.Contains(out, "branch_mode") {
		t.Fatal("the plain create renders branch fields")
	}
	if !strings.Contains(out, `<option value=""selected>An empty project</option>`) && !strings.Contains(out, `<option value="" selected>An empty project</option>`) {
		t.Fatalf("the source select does not stand on the empty project:\n%s", out)
	}
	if !strings.Contains(out, `name="project_name"`) {
		t.Fatal("the form lost its name field")
	}
}

// The source select is a project listing like every other one: the option
// reads as the project it names, so typing in the select finds it, and it
// carries what the shared sort compares.
func TestCreateFormSourcesReadAsProjectsAndCanBeSorted(t *testing.T) {
	out := renderProjectNew(t, worktreeFormData())
	if !strings.Contains(out, `data-project-name="app" data-project-active="true" data-project-used="42">app (new worktree)</option>`) {
		t.Fatalf("the source option does not carry the sort marks:\n%s", out)
	}
	if !strings.Contains(out, "<dc-project-select") {
		t.Fatal("the source select is not the shared one")
	}
	if strings.Contains(out, "A worktree of") {
		t.Fatal("the option still reads as a sentence instead of a project")
	}
	if !strings.Contains(out, `value="`+WorktreeChoice("app")+`" selected`) {
		t.Fatalf("the picked source is not the selected option:\n%s", out)
	}
}

// The three kinds the one select carries, and a choice that survives the way
// back into the form it came from.
func TestCreateFormOffersTheThreeKinds(t *testing.T) {
	out := renderProjectNew(t, ProjectNewData{Page: Page{Title: "New Project"}, Sources: []ProjectOption{{Name: "app"}}})
	if !strings.Contains(out, `<option value="" selected>An empty project</option>`) {
		t.Fatalf("the empty project is not the default:\n%s", out)
	}
	if !strings.Contains(out, `<option value="`+CreateClone+`">An existing repository</option>`) {
		t.Fatalf("the clone is not offered:\n%s", out)
	}
	if !strings.Contains(out, `value="`+WorktreeChoice("app")+`"`) {
		t.Fatal("the project is not offered as a worktree")
	}

	clone := renderProjectNew(t, ProjectNewData{Page: Page{Title: "New Project"}, Create: CreateClone, Sources: []ProjectOption{{Name: "app"}}})
	if !strings.Contains(clone, `<option value="`+CreateClone+`" selected>`) {
		t.Fatal("the clone choice does not come back selected")
	}
	if !strings.Contains(clone, `name="clone_url"`) || !strings.Contains(clone, "data-clone-url") {
		t.Fatalf("the clone asks for no url:\n%s", clone)
	}
	if strings.Contains(clone, "data-branch-block") {
		t.Fatal("a clone renders branch fields")
	}
}

func TestWorktreeChoiceRoundTrip(t *testing.T) {
	if got := WorktreeSource(WorktreeChoice("app")); got != "app" {
		t.Fatalf("the choice does not name its project: %q", got)
	}
	// A project may carry the name of a kind, which is what the prefix is for.
	if got := WorktreeSource(WorktreeChoice(CreateClone)); got != CreateClone {
		t.Fatalf("a project named like a kind is lost: %q", got)
	}
	for _, choice := range []string{"", CreateClone, "app"} {
		if got := WorktreeSource(choice); got != "" {
			t.Fatalf("%q reads as a source: %q", choice, got)
		}
	}
}

// A branch another working copy holds is in the list, so it is clear it
// exists, and cannot be picked, because git would refuse it.
func TestCreateFormDisablesTakenBranchesAndNamesTheirHolder(t *testing.T) {
	out := renderProjectNew(t, worktreeFormData())
	if !strings.Contains(out, "master (in app)") {
		t.Fatalf("the taken branch does not name its holder:\n%s", out)
	}
	if !strings.Contains(out, `<option value="master" data-branch-name="master" disabled>`) {
		t.Fatalf("the taken branch can be picked:\n%s", out)
	}
	if !strings.Contains(out, `<option value="feature" data-branch-name="feature" selected>`) {
		t.Fatalf("the form does not stand on the first free branch:\n%s", out)
	}
	if !strings.Contains(out, `value="existing" data-branch-mode checked`) {
		t.Fatal("the form does not open on the existing branch")
	}
	if !strings.Contains(out, `<option value="origin/shared" data-branch-name="shared">origin/shared</option>`) {
		t.Fatalf("the remote branch is not offered:\n%s", out)
	}
}

// The ordinary state of a project with one branch: it stands in the project
// itself, so there is nothing to check out and the form opens on the new
// branch instead of on a list where everything is refused.
func TestCreateFormOpensOnANewBranchWhenEveryBranchIsTaken(t *testing.T) {
	data := worktreeFormData()
	data.Branches = []BranchChoice{{Ref: "master", Name: "master", Taken: "app", Head: true}}
	out := renderProjectNew(t, data)
	if !strings.Contains(out, `value="new" data-branch-mode checked`) {
		t.Fatalf("the form does not open on the new branch:\n%s", out)
	}
	if !strings.Contains(out, `<div class="mb-3" data-branch-block="existing" hidden>`) {
		t.Fatal("the branch list is not put away")
	}
	if !strings.Contains(out, `id="start"`) || !strings.Contains(out, `<option value="master" selected>master</option>`) {
		t.Fatalf("the new branch does not start at the source's own branch:\n%s", out)
	}
}

// A refused create comes back as the form it was: everything that was typed
// stands in it again, and a branch that is taken meanwhile is not picked
// again just because it was picked before.
func TestCreateFormComesBackFilled(t *testing.T) {
	data := worktreeFormData()
	data.Fill = ProjectNewFill{Name: "app-wip", Mode: "new", NewBranch: "wip/one", Start: "origin/shared"}
	out := renderProjectNew(t, data)
	if !strings.Contains(out, `value="app-wip"`) {
		t.Fatalf("the name is gone:\n%s", out)
	}
	if !strings.Contains(out, `value="wip/one"`) {
		t.Fatalf("the typed branch is gone:\n%s", out)
	}
	if !strings.Contains(out, `value="new" data-branch-mode checked`) {
		t.Fatal("the form came back on the other branch mode")
	}
	if !strings.Contains(out, `<option value="origin/shared" selected>origin/shared</option>`) {
		t.Fatalf("the starting point is gone:\n%s", out)
	}

	clone := renderProjectNew(t, ProjectNewData{Page: Page{Title: "New Project"}, Create: CreateClone, Fill: ProjectNewFill{Name: "thing", CloneURL: "https://example.com/thing.git"}})
	if !strings.Contains(clone, `value="https://example.com/thing.git"`) || !strings.Contains(clone, `value="thing"`) {
		t.Fatalf("the clone form came back empty:\n%s", clone)
	}

	taken := worktreeFormData()
	taken.Fill = ProjectNewFill{Mode: "existing", Branch: "master"}
	if out := renderProjectNew(t, taken); !strings.Contains(out, `<option value="feature" data-branch-name="feature" selected>`) {
		t.Fatalf("a branch that is taken meanwhile was picked again:\n%s", out)
	}
}

// A repository without a single commit has nothing a worktree could stand on,
// and the form says so instead of offering a button that only fails.
func TestCreateFormSaysWhenTheSourceHasNoBranch(t *testing.T) {
	data := worktreeFormData()
	data.Branches = nil
	out := renderProjectNew(t, data)
	if !strings.Contains(out, "has no branch yet") {
		t.Fatalf("the empty repository is not explained:\n%s", out)
	}
	if !strings.Contains(out, `<button type="submit" class="btn btn-primary" disabled>`) {
		t.Fatalf("the create button is still offered:\n%s", out)
	}
}
