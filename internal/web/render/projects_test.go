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

// The two branch fields are autocompletes over the source repository, not
// lists the page was rendered with: the form carries the picked value in a
// hidden field, the visible one is the query, and the rows come from the
// server round by round. What it does render is where the pickers open: the
// first free branch, never a taken one.
func TestCreateFormRendersTheBranchPickers(t *testing.T) {
	out := renderProjectNew(t, worktreeFormData())
	if strings.Contains(out, "<option value=\"feature\"") || strings.Contains(out, "On a remote") {
		t.Fatalf("the branches are still rendered into the page:\n%s", out)
	}
	if !strings.Contains(out, `data-branch-picker="branch" data-branch-source="app" data-branch-marks-taken="true"`) {
		t.Fatalf("the checkout field is no picker:\n%s", out)
	}
	if !strings.Contains(out, `name="branch" value="feature" data-branch-value data-branch-name="feature"`) {
		t.Fatalf("the form does not post the first free branch:\n%s", out)
	}
	if !strings.Contains(out, `data-branch-picker="start"`) || !strings.Contains(out, `name="start" value="master" data-branch-value data-branch-name="master" disabled`) {
		t.Fatalf("the starting point is no picker on the source's own branch:\n%s", out)
	}
	// No prose in the form: what the rows and the labels say is what there is.
	if strings.Contains(out, "form-hint") {
		t.Fatalf("the form still explains itself in paragraphs:\n%s", out)
	}
	// The two modes are one select, the same control the create choice above
	// them is, and the names the POST carries are unchanged.
	if !strings.Contains(out, `<select class="form-select" id="branch_mode" name="branch_mode" data-branch-mode>`) {
		t.Fatalf("the branch mode is no select:\n%s", out)
	}
	if strings.Contains(out, "form-selectgroup") {
		t.Fatalf("the branch mode is still a radio group:\n%s", out)
	}
	if !strings.Contains(out, `<option value="existing" selected>Existing branch</option>`) {
		t.Fatalf("the form does not open on the existing branch:\n%s", out)
	}
	// The name field is the name and nothing else: no paragraph under it and no
	// path in front of it either.
	if !strings.Contains(out, `<input class="form-control" id="project_name" name="project_name"`) {
		t.Fatalf("the name field is not a plain field:\n%s", out)
	}
	if strings.Contains(out, "input-group") {
		t.Fatalf("the name field carries a directory prefix:\n%s", out)
	}
}

// The resync belongs to the list it changes, so it is a row of each picker's
// menu: its head, outside the part that scrolls, and there whether anything
// matched or not. Both fields get one, and the plain create gets none.
func TestCreateFormOffersTheResyncInsideBothPickers(t *testing.T) {
	out := renderProjectNew(t, worktreeFormData())
	if got := strings.Count(out, "data-branch-resync>"); got != 2 {
		t.Fatalf("the pickers carry %d resync rows, want one each:\n%s", got, out)
	}
	if got := strings.Count(out, `data-branch-source="app"`); got != 2 {
		t.Fatalf("a picker does not know the project it fetches:\n%s", out)
	}
	// The hits scroll, the row above them does not: only the list inside the
	// menu is the scrolling box, so the row keeps its place however far
	// somebody scrolled.
	if got := strings.Count(out, `class="overflow-auto" style="max-height: min(14rem, 40vh)" data-branch-list`); got != 2 {
		t.Fatalf("the hits and the resync scroll together:\n%s", out)
	}
	// It is the head of the menu, so it stands before the hits in both.
	for _, menu := range strings.Split(out, "data-branch-menu>")[1:] {
		row := strings.Index(menu, "data-branch-resync>")
		list := strings.Index(menu, "data-branch-list")
		if row < 0 || list < 0 || row > list {
			t.Fatalf("the resync does not head its menu (row %d, list %d):\n%s", row, list, out)
		}
	}
	// The running state is the icon turning, so the row carries the icon it
	// spins and nothing that only exists while it runs: a node swapped in for
	// the busy state brings a box of its own and moves the rows below it.
	if !strings.Contains(out, `data-branch-resync-icon`) || strings.Contains(out, "spinner-border") {
		t.Fatalf("the resync has no icon to turn, or still swaps a spinner in:\n%s", out)
	}
	if !strings.Contains(out, "data-branch-fetched") {
		t.Fatalf("the resync does not say when it last fetched:\n%s", out)
	}
	// Nothing outside the menus explains the connection any more, the row is
	// the connection.
	if strings.Contains(out, "Resync fetches the remotes") {
		t.Fatalf("the form still explains the resync in prose:\n%s", out)
	}
	plain := renderProjectNew(t, ProjectNewData{Page: Page{Title: "New Project"}, Sources: []ProjectOption{{Name: "app"}}})
	if strings.Contains(plain, "data-branch-resync") {
		t.Fatal("the plain create renders a resync")
	}
}

// A new branch starts where the source project stands, except when standing
// there only means standing behind: a head that has fallen behind its upstream
// and holds nothing of its own starts at the upstream instead, because that is
// the same history further along. The moment the head is ahead or has
// diverged, its own commits exist nowhere else and it keeps the start, or
// branching would drop them without a word.
func TestDefaultStartLeavesAHeadThatOnlyFellBehind(t *testing.T) {
	behind := func(ahead, behindBy int, upstream string) ProjectNewData {
		return ProjectNewData{Branches: []BranchChoice{
			{Ref: "master", Name: "master", Head: true, Upstream: upstream, Ahead: ahead, Behind: behindBy},
			{Ref: "origin/master", Name: "master", Remote: true},
		}}
	}
	for _, c := range []struct {
		why  string
		data ProjectNewData
		want string
	}{
		{"purely behind starts at the upstream", behind(0, 3, "origin/master"), "origin/master"},
		{"level stays on the branch", behind(0, 0, "origin/master"), "master"},
		{"ahead keeps its own commits", behind(2, 0, "origin/master"), "master"},
		{"diverged keeps its own commits", behind(2, 3, "origin/master"), "master"},
		{"no upstream, nothing to compare", behind(0, 0, ""), "master"},
	} {
		if got := c.data.DefaultStart(); got != c.want {
			t.Errorf("%s: DefaultStart() = %q, want %q", c.why, got, c.want)
		}
	}

	// What somebody already typed outranks all of it, a refused create comes
	// back on the start it was refused with.
	filled := behind(0, 3, "origin/master")
	filled.Fill = ProjectNewFill{Start: "master"}
	if got := filled.DefaultStart(); got != "master" {
		t.Fatalf("the refused form did not come back on its own start: %q", got)
	}
}

// The ordinary state of a project with one branch: it stands in the project
// itself, so there is nothing to check out and the form opens on the new
// branch instead of on a list where everything is refused.
func TestCreateFormOpensOnANewBranchWhenEveryBranchIsTaken(t *testing.T) {
	data := worktreeFormData()
	data.Branches = []BranchChoice{{Ref: "master", Name: "master", Taken: "app", Head: true}}
	out := renderProjectNew(t, data)
	if !strings.Contains(out, `<option value="new" selected>New branch</option>`) {
		t.Fatalf("the form does not open on the new branch:\n%s", out)
	}
	// A select that offers a half with nothing behind it leads into an empty
	// list, so that half is closed while every branch stands somewhere.
	if !strings.Contains(out, `<option value="existing" disabled>Existing branch</option>`) {
		t.Fatalf("the existing branch can be picked with nothing to check out:\n%s", out)
	}
	if !strings.Contains(out, `<div class="mb-3" data-branch-block="existing" hidden>`) {
		t.Fatal("the branch list is not put away")
	}
	if !strings.Contains(out, `id="start"`) || !strings.Contains(out, `name="start" value="master" data-branch-value`) {
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
	if !strings.Contains(out, `<option value="new" selected>New branch</option>`) {
		t.Fatal("the form came back on the other branch mode")
	}
	if !strings.Contains(out, `name="start" value="origin/shared" data-branch-value`) {
		t.Fatalf("the starting point is gone:\n%s", out)
	}

	clone := renderProjectNew(t, ProjectNewData{Page: Page{Title: "New Project"}, Create: CreateClone, Fill: ProjectNewFill{Name: "thing", CloneURL: "https://example.com/thing.git"}})
	if !strings.Contains(clone, `value="https://example.com/thing.git"`) || !strings.Contains(clone, `value="thing"`) {
		t.Fatalf("the clone form came back empty:\n%s", clone)
	}

	taken := worktreeFormData()
	taken.Fill = ProjectNewFill{Mode: "existing", Branch: "master"}
	if out := renderProjectNew(t, taken); !strings.Contains(out, `name="branch" value="feature"`) {
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
