package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

// A pipeline or review trigger names a project the cockpit binds to a
// forge: one it does not know, one that is no repository and one bound to
// nothing are refused, and a branch is a branch name. The form offers the
// bound projects alone and opens a change on the project and the branch
// that stand, and the row reads by the branch in its project.
func TestAPipelineTriggerNamesABoundProject(t *testing.T) {
	root := t.TempDir()
	app := worktreeSourceRepo(t, root, "app")
	worktreeGit(t, app, "remote", "add", "origin", "git@github.com:octo/app.git")
	worktreeSourceRepo(t, root, "lone")
	if err := os.Mkdir(filepath.Join(root, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, raw := ownerPages(t, worktreeProjects(t, root))
	s.SetForge(forge.New(t.TempDir(), []forge.Provider{github.Provider{}, gitlab.Provider{}}, github.Builtin(), gitlab.Builtin()))
	owner, err := s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	specOf := func(stood assistant.Trigger, fields url.Values) (assistant.TriggerSpec, error) {
		t.Helper()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, assistantTriggersPath, strings.NewReader(fields.Encode()))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return s.assistantTriggerSpec(c, stood)
	}
	spec := func(fields url.Values) (assistant.TriggerSpec, error) {
		t.Helper()
		return specOf(assistant.Trigger{}, fields)
	}
	for project, refusal := range map[string]string{
		"nope":                   `No project is called "nope".`,
		"plain":                  `"plain" is no git repository, it binds to no forge.`,
		"lone":                   `"lone" is bound to no forge`,
		strings.Repeat("a", 256): "A project name has at most 255 bytes.",
	} {
		for _, event := range []string{"forge-pipeline-failed", "forge-review-comment"} {
			if _, err := spec(url.Values{"event": {event}, "project": {project}}); err == nil || !strings.Contains(err.Error(), refusal) {
				t.Fatalf("the project %.20q of %s answered %v", project, event, err)
			}
		}
	}
	for _, branch := range []string{"a b", "a\tb", strings.Repeat("b", forge.MaxBranch+1)} {
		if _, err := spec(url.Values{"event": {"forge-pipeline-failed"}, "project": {"app"}, "branch": {branch}}); err == nil {
			t.Fatalf("the branch %.20q was taken", branch)
		}
	}
	got, err := spec(url.Values{"event": {"forge-pipeline-failed"}, "project": {"app"}, "branch": {" feature "}, "task": {"look"}})
	if err != nil || got.Project != "app" || got.Branch != "feature" || !got.BranchSet {
		t.Fatalf("a bound project answered %+v %v", got, err)
	}
	got.Owner = owner.ID
	trigger, err := s.assistants.Events().Add(got)
	if err != nil {
		t.Fatal(err)
	}
	if view := s.assistantTriggerView(trigger, false); view.Where != "feature in app" || view.Heading != "Pipeline failed, feature in app" || view.TargetURL != "" {
		t.Fatalf("the row reads %+v", view)
	}
	if !triggerCarries(trigger, "feature") || !triggerCarries(trigger, "app") {
		t.Fatal("a search does not find the trigger by its branch and its project")
	}
	worktreeGit(t, app, "remote", "remove", "origin")
	if _, err := specOf(trigger, url.Values{"project": {"app"}, "task": {"again"}}); err != nil {
		t.Fatalf("a change of a trigger whose project lost its forge answered %v", err)
	}
	if _, err := specOf(trigger, url.Values{"project": {"lone"}}); err == nil {
		t.Fatal("a change onto a project bound to nothing was taken")
	}
	worktreeGit(t, app, "remote", "add", "origin", "git@github.com:octo/app.git")

	form := raw(http.MethodGet, assistantTriggersPath+"?form=new&modal=1&assistant="+owner.ID, "", "").Body.String()
	if !strings.Contains(form, `<option value="app">app</option>`) || strings.Contains(form, `value="lone"`) || strings.Contains(form, `value="plain"`) {
		t.Fatalf("the form offers the projects\n%s", form)
	}
	if !strings.Contains(form, `<option value="forge-pipeline-failed" data-source="forge"`) || !strings.Contains(form, `<option value="forge-review-comment" data-source="forge"`) {
		t.Fatalf("the form offers no pipeline or review event\n%s", form)
	}
	edit := raw(http.MethodGet, assistantTriggersPath+"?form=edit&modal=1&id="+trigger.ID, "", "").Body.String()
	if !strings.Contains(edit, `<option value="app" selected>app</option>`) || !strings.Contains(edit, `value="feature"`) {
		t.Fatalf("the change opens on\n%s", edit)
	}
}
