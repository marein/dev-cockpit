package render

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/project"
)

// renderProjects executes the real projects list with one row.
func renderProjects(t *testing.T, data ProjectsListData) string {
	t.Helper()
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test")
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
