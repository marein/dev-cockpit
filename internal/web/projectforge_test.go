package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
	"github.com/marein/dev-cockpit/internal/project"
)

// forgePages lays out the tags fixture's main repository with an origin on
// github.com and its worktree project feature, beside a directory without
// git, and serves the owner's pages over them with the forge service.
func forgePages(t *testing.T) (*Server, func(path string, form url.Values) *httptest.ResponseRecorder) {
	t.Helper()
	projects, root := tagsFixture(t)
	config := "[remote \"origin\"]\n\turl = git@github.com:marein/app.git\n"
	if err := os.WriteFile(filepath.Join(root, "main", ".git", "config"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, raw := ownerPages(t, projects)
	s.SetForge(forge.New(t.TempDir(), []forge.Provider{github.Provider{}, gitlab.Provider{}}, github.Builtin(), gitlab.Builtin()))
	return s, func(path string, form url.Values) *httptest.ResponseRecorder {
		if form == nil {
			return raw(http.MethodGet, path, "", "")
		}
		return raw(http.MethodPost, path, "application/x-www-form-urlencoded", form.Encode())
	}
}

func boundTo(t *testing.T, s *Server, name string) string {
	t.Helper()
	b, ok := s.forgeByProject(s.projects.List())[name]
	if !ok {
		return ""
	}
	return b.WebURL()
}

func forgeForm(how, host, path string) url.Values {
	return url.Values{"how": {how}, "host": {host}, "path": {path}}
}

// A project's forge shows where the origin binds it, with the binding by
// hand filled from the origin, and the dialog saves a binding by hand that
// wins over the origin, its worktree included, then off and back again.
func TestAProjectsForgeIsShownAndSetByHand(t *testing.T) {
	s, send := forgePages(t)
	page := send("/projects/main/forge?modal=1", nil).Body.String()
	for _, want := range []string{
		`action="/projects/main/forge?modal=1"`, `data-form-title="Forge of main"`,
		`<a href="https://github.com/marein/app" target="_blank" rel="noopener" class="text-break" data-forge-bound-link>marein/app</a>`,
		"GitHub at https://github.com", "github.com/marein/app on GitHub",
		`value="origin" checked`, `<option value="github.com" selected>GitHub at https://github.com</option>`, `name="path" value="marein/app"`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the dialog lacks %s:\n%s", want, page)
		}
	}

	rec := send("/projects/main/forge?modal=1", forgeForm("hand", "gitlab.com", "group/sub/app"))
	if message, _ := answered(t, rec); rec.Code != http.StatusOK || message != "Forge saved." {
		t.Fatalf("the save answered %d %q", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"main", "feature"} {
		if got := boundTo(t, s, name); got != "https://gitlab.com/group/sub/app" {
			t.Fatalf("%s is bound to %q", name, got)
		}
	}
	page = send("/projects/main/forge?modal=1", nil).Body.String()
	for _, want := range []string{`value="hand" checked`, `<option value="gitlab.com" selected>`, `name="path" value="group/sub/app"`, "data-forge-bound-link>group/sub/app</a>", "github.com/marein/app on GitHub"} {
		if !strings.Contains(page, want) {
			t.Fatalf("the dialog after the save lacks %s", want)
		}
	}
	list := send("/projects", nil).Body.String()
	if !strings.Contains(list, `<a href="https://gitlab.com/group/sub/app" target="_blank" rel="noopener" class="link-secondary small text-decoration-none text-break" data-project-origin><i class="ti ti-brand-gitlab icon icon-xs" aria-hidden="true"></i> github.com/marein/app</a>`) {
		t.Fatal("the row does not follow the binding by hand")
	}
	if !strings.Contains(list, `data-git-forge="/projects/main/forge"`) || strings.Contains(list, `data-git-forge="/projects/plain/forge"`) {
		t.Fatal("the git menu does not offer the forge of the repositories alone")
	}

	if rec := send("/projects/main/forge", forgeForm("off", "", "")); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/projects#project-main" {
		t.Fatalf("off answered %d to %s", rec.Code, rec.Header().Get("Location"))
	}
	if got := boundTo(t, s, "main"); got != "" {
		t.Fatalf("off left main bound to %s", got)
	}
	if page := send("/projects/main/forge?modal=1", nil).Body.String(); !strings.Contains(page, "data-forge-unbound>No forge.") || !strings.Contains(page, `value="off" checked`) {
		t.Fatal("the dialog does not say the binding is off")
	}
	send("/projects/main/forge?modal=1", forgeForm("origin", "gitlab.com", "x/y"))
	if got := boundTo(t, s, "main"); got != "https://github.com/marein/app" {
		t.Fatalf("back to the origin bound main to %q", got)
	}
}

// A worktree shows its main project's binding and a way there, and changes
// none; a directory without git has no forge.
func TestAWorktreeShowsItsMainProjectsForge(t *testing.T) {
	s, send := forgePages(t)
	if err := s.forge.SetOverride("main", forge.Override{How: forge.ByHand, Host: "gitlab.com", Path: "team/app"}); err != nil {
		t.Fatal(err)
	}
	page := send("/projects/feature/forge?modal=1", nil).Body.String()
	if !strings.Contains(page, "data-forge-bound-link>team/app</a>") || !strings.Contains(page, `A worktree binds like its main project, <a href="/projects/main/forge">main</a>.`) {
		t.Fatalf("the worktree's dialog:\n%s", page)
	}
	if strings.Contains(page, `name="how"`) || strings.Contains(page, "data-forge-binding-save") {
		t.Fatal("the worktree's dialog offers a binding of its own")
	}
	rec := send("/projects/feature/forge?modal=1", forgeForm("off", "", ""))
	if _, refused := answered(t, rec); rec.Code != http.StatusBadRequest || refused != `A worktree binds like its main project "main".` {
		t.Fatalf("the worktree's save answered %d %q", rec.Code, rec.Body.String())
	}
	if got := boundTo(t, s, "feature"); got != "https://gitlab.com/team/app" {
		t.Fatalf("the worktree is bound to %q", got)
	}

	if rec := send("/projects/plain/forge", nil); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `&#34;plain&#34; is no git repository, it binds to no forge.`) {
		t.Fatalf("a directory without git answered %d", rec.Code)
	}
	rec = send("/projects/plain/forge?modal=1", forgeForm("hand", "github.com", "a/b"))
	if _, refused := answered(t, rec); rec.Code != http.StatusConflict || refused != `"plain" is no git repository, it binds to no forge.` {
		t.Fatalf("a directory without git saved: %d %q", rec.Code, rec.Body.String())
	}
	if rec := send("/projects/gone/forge", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("a project that is not there answered %d", rec.Code)
	}
}

// A refusal keeps the dialog over the sentence, the page shows the form again
// under it with what was sent, and nothing is stored. The local socket
// changes no binding.
func TestARefusedBindingIsNotStored(t *testing.T) {
	s, send := forgePages(t)
	rec := send("/projects/main/forge?modal=1", forgeForm("hand", "gitlab.com", "app"))
	if _, refused := answered(t, rec); rec.Code != http.StatusBadRequest || refused != `"app" is no repository path, like group/app.` {
		t.Fatalf("a path without group answered %d %q", rec.Code, rec.Body.String())
	}
	rec = send("/projects/main/forge", forgeForm("hand", "nowhere", "team/<b>app"))
	page := rec.Body.String()
	if rec.Code != http.StatusBadRequest || notice(page) != "That host no longer exists." || !strings.Contains(page, `value="hand" checked`) || !strings.Contains(page, `name="path" value="team/&lt;b&gt;app"`) || !strings.Contains(page, `<option value="" disabled selected>Pick the host</option>`) {
		t.Fatalf("the page refusal answered %d %q", rec.Code, notice(page))
	}
	if rec := send("/projects/main/forge?modal=1", forgeForm("mirror", "", "")); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown way to bind answered %d", rec.Code)
	}
	if got := boundTo(t, s, "main"); got != "https://github.com/marein/app" {
		t.Fatalf("a refusal bound main to %q", got)
	}

	form := forgeForm("off", "", "")
	req := httptest.NewRequest(http.MethodPost, "/projects/main/forge", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
	c.Params = gin.Params{{Key: "name", Value: "main"}}
	s.handleProjectForgeSave(c)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), projectForgeLocalRefusal) {
		t.Fatalf("the socket answered %d %s", rec.Code, rec.Body.String())
	}
	if got := boundTo(t, s, "main"); got != "https://github.com/marein/app" {
		t.Fatalf("the socket bound main to %q", got)
	}
}

// A deletion takes the project's binding with it, in the request and off it,
// and leaves the others.
func TestADeletedProjectTakesItsBindingAlong(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"app", "lab", "other"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakeTmux(t, "11111111-1111-4111-8111-111111111111", t.TempDir())
	projects := project.NewRepository(root, nil)
	s := deletionServer(t, t.TempDir(), projects)
	s.SetForge(forge.New(t.TempDir(), []forge.Provider{github.Provider{}}, github.Builtin()))
	for _, name := range []string{"app", "lab", "other"} {
		if err := s.forge.SetOverride(name, forge.Override{How: forge.Off}); err != nil {
			t.Fatal(err)
		}
	}
	app, _ := projects.FindByName("app")
	s.deletes.start(app.Name, app.Path)
	s.deleteProjectWithCompose(app)
	lab, _ := projects.FindByName("lab")
	if err := s.removeProjectNow(lab); err != nil {
		t.Fatal(err)
	}
	bindings, _ := s.forge.Bindings()
	if bindings.Override("app").How != forge.ByOrigin || bindings.Override("lab").How != forge.ByOrigin || bindings.Override("other").How != forge.Off {
		t.Fatalf("after the deletions: app %+v, lab %+v, other %+v", bindings.Override("app"), bindings.Override("lab"), bindings.Override("other"))
	}
}
