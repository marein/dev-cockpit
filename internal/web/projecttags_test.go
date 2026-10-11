package web

import (
	"encoding/json"
	"fmt"
	"html"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/activity"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/backup"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// tagsFixture lays out a main repository with one linked worktree project and
// hands back a repository that keeps tags.
func tagsFixture(t *testing.T) (*project.Repository, string) {
	t.Helper()
	root := t.TempDir()
	mainDir := filepath.Join(root, "main")
	wtDir := filepath.Join(root, "feature")
	gitDir := filepath.Join(mainDir, ".git", "worktrees", "feature")
	for path, content := range map[string]string{
		filepath.Join(mainDir, ".git", "HEAD"): "ref: refs/heads/master\n",
		filepath.Join(gitDir, "HEAD"):          "ref: refs/heads/feature\n",
		filepath.Join(gitDir, "commondir"):     "../..\n",
		filepath.Join(gitDir, "gitdir"):        filepath.Join(wtDir, ".git") + "\n",
		filepath.Join(wtDir, ".git"):           "gitdir: " + gitDir + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return project.NewRepository(root, nil).UseTags(project.NewTags(t.TempDir())), root
}

func postTags(t *testing.T, s *Server, name string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/projects/"+name+"/tags?modal=1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request = req
	c.Params = gin.Params{{Key: "name", Value: name}}
	s.handleProjectTagsSave(c)
	return rec
}

func tagsOf(t *testing.T, projects *project.Repository, name string) []project.Tag {
	t.Helper()
	p, err := projects.FindByName(name)
	if err != nil {
		t.Fatalf("find %s: %v", name, err)
	}
	return p.Tags
}

func shownTags(tags []project.Tag) []string {
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name+":"+tag.Color)
	}
	return names
}

// The dialog saves a main project's tags with the colors new ones show and
// closes over a message, and the same post for a worktree, or with a color
// outside the palette, is refused with the reason the dialog shows. A
// worktree carries no tags, and the dialog changes no color of a tag in use.
func TestTheTagsDialogSavesAMainAndRefusesAWorktree(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projects, _ := tagsFixture(t)
	s := &Server{projects: projects, bus: eventbus.New()}

	rec := postTags(t, s, "main", url.Values{"new": {"backend", "api"}, "new-color:backend": {"pink"}, "new-color:api": {"teal"}})
	var answer struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); rec.Code != http.StatusOK || err != nil || answer.Message == "" {
		t.Fatalf("the save answered %d %q", rec.Code, rec.Body.String())
	}
	if got := shownTags(tagsOf(t, projects, "main")); !slices.Equal(got, []string{"api:teal", "backend:pink"}) {
		t.Fatalf("the main shows %q", got)
	}
	if got := tagsOf(t, projects, "feature"); len(got) != 0 {
		t.Fatalf("the worktree shows %v", got)
	}
	api := tagsOf(t, projects, "main")[0].ID

	for name, form := range map[string]url.Values{
		"feature": {"new": {"own"}},
		"main":    {"tag": {api}, "new": {"web"}, "new-color:web": {"teal;} body{display:none"}},
	} {
		rec = postTags(t, s, name, form)
		answer.Error = ""
		if err := json.Unmarshal(rec.Body.Bytes(), &answer); rec.Code != http.StatusBadRequest || err != nil || answer.Error == "" {
			t.Fatalf("the save of %s answered %d %q", name, rec.Code, rec.Body.String())
		}
	}
	if got := shownTags(tagsOf(t, projects, "main")); !slices.Equal(got, []string{"api:teal", "backend:pink"}) {
		t.Fatalf("a refused save changed the main's tags to %q", got)
	}

	rec = postTags(t, s, "main", url.Values{"tag": {api}, "color:" + api: {"lime"}})
	if got := tagsOf(t, projects, "main"); rec.Code != http.StatusOK || len(got) != 1 || got[0] != (project.Tag{ID: api, Name: "api", Color: "teal"}) {
		t.Fatalf("a save by id answered %d and left %v, the form changes no color", rec.Code, got)
	}
}

// formFields answers what a rendered tags form posts: its hidden fields as a
// browser sends them, the chip templates and the token left out.
func formFields(t *testing.T, data render.ProjectTagsData) url.Values {
	t.Helper()
	var out strings.Builder
	if err := render.HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil).ExecuteTemplate(&out, "project_tags_form.gohtml", data); err != nil {
		t.Fatal(err)
	}
	page := regexp.MustCompile(`(?s)<template.*?</template>`).ReplaceAllString(out.String(), "")
	form := url.Values{}
	for _, field := range regexp.MustCompile(`<input type="hidden" name="([^"]*)" value="([^"]*)"`).FindAllStringSubmatch(page, -1) {
		if field[1] != "csrf_token" {
			form.Add(html.UnescapeString(field[1]), html.UnescapeString(field[2]))
		}
	}
	return form
}

// A tags form left open while the settings recolor a tag it carries saves
// without setting the color back, it posts no color of a tag in use.
func TestAStaleTagsFormKeepsAColorChangedElsewhere(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projects, root := tagsFixture(t)
	if err := os.MkdirAll(filepath.Join(root, "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{projects: projects, bus: eventbus.New()}
	postTags(t, s, "main", url.Values{"new": {"backend"}, "new-color:backend": {"lime"}})
	postTags(t, s, "beta", url.Values{"new": {"api", "backend"}, "new-color:api": {"pink"}})
	beta := tagsOf(t, projects, "beta")
	all := projects.List()
	form := formFields(t, render.ProjectTagsData{Project: "beta", Tags: beta, InUse: project.TagsInUse(all), Colors: project.TagColors, NextColors: project.NextTagColors(project.TagsInUse(all)), Modal: true})
	backend := beta[1]
	if len(form) != 1 || len(form["tag"]) != 2 {
		t.Fatalf("the form posts %v", form)
	}

	states, err := projects.TagStates()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.ID == backend.ID {
			if _, err := projects.SaveTags([]project.TagChange{{ID: state.ID, Version: state.Version, LoadedName: state.Name, LoadedColor: state.Color, Name: state.Name, Color: "teal"}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if rec := postTags(t, s, "beta", form); rec.Code != http.StatusOK {
		t.Fatalf("the stale form answered %d %s", rec.Code, rec.Body.String())
	}
	if got := shownTags(tagsOf(t, projects, "main")); !slices.Equal(got, []string{"backend:teal"}) {
		t.Fatalf("the stale form set the color back: %q", got)
	}
}

// A refusal on the page shows the form again under the reason with
// everything it sent, a tag of any spelling in its own color field and a
// color outside the palette dropped, and writes nothing. The address stays
// the form's, nothing sent rides a redirect.
func TestARefusedSaveKeepsWhatWasSent(t *testing.T) {
	projects, _ := tagsFixture(t)
	send := tagsRouter(t, projects)
	send("/projects/main/tags?modal=1", url.Values{"new": {"api"}})
	api := tagsOf(t, projects, "main")[0].ID
	rec := send("/projects/main/tags", url.Values{"tag": {api}, "new": {"a b", "c:d"}, "new-color:a b": {"teal"}, "new-color:c:d": {"teal;}"}})
	if rec.Code != http.StatusOK || rec.Header().Get("Location") != "" || !strings.Contains(notice(rec.Body.String()), "none of the tag colors") {
		t.Fatalf("the refusal answered %d %q", rec.Code, notice(rec.Body.String()))
	}
	form := pageFields(t, rec.Body.String())
	if !slices.Equal(form["tag"], []string{api}) || !slices.Equal(form["new"], []string{"a b", "c:d"}) || form.Get("new-color:a b") != "teal" || form.Get("new-color:c:d") != "" {
		t.Fatalf("the refused form posts %v", form)
	}
	if got := shownTags(tagsOf(t, projects, "main")); len(got) != 1 {
		t.Fatalf("a refused save wrote %q", got)
	}
}

// Deleting a worktree through the cockpit leaves its main's tags, deleting
// the main takes them, on the in request path and on the compose one alike.
func TestDeletingAProjectTakesItsTags(t *testing.T) {
	projects, root := tagsFixture(t)
	fakeTmux(t, "22222222-2222-4222-8222-222222222222", t.TempDir())
	s := deletionServer(t, t.TempDir(), projects)
	main, err := projects.FindByName("main")
	if err != nil {
		t.Fatal(err)
	}
	if err := projects.SetTags(main, project.TagPick{Names: []string{"backend"}}); err != nil {
		t.Fatal(err)
	}
	feature, err := projects.FindByName("feature")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.removeProjectNow(feature); err != nil {
		t.Fatalf("delete the worktree: %v", err)
	}
	if got := tagsOf(t, projects, "main"); len(got) != 1 || got[0].Name != "backend" {
		t.Fatalf("deleting the worktree left the main with %q", got)
	}

	main, _ = projects.FindByName("main")
	s.deletes.start(main.Name, main.Path)
	s.deleteProjectWithCompose(main)
	if err := os.MkdirAll(filepath.Join(root, "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := tagsOf(t, projects, "main"); len(got) != 0 {
		t.Fatalf("a project made under the deleted name carries %q", got)
	}
}

// tagsRouter is the full route table over a repository that keeps tags, with
// a signed in session and its CSRF token, so a post reaches its handler the
// way a browser's does. send answers what the router said.
func tagsRouter(t *testing.T, projects *project.Repository) func(path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	raw := tagsSession(t, projects)
	return func(path string, form url.Values) *httptest.ResponseRecorder {
		if form == nil {
			return raw(http.MethodGet, path, "", "")
		}
		return raw(http.MethodPost, path, "application/x-www-form-urlencoded", form.Encode())
	}
}

// tagsSession sends any request in a signed in session, a post with its
// CSRF token in the header, so the body can be of any shape.
func tagsSession(t *testing.T, projects *project.Repository) func(method, path, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	assets, err := newStaticAssetManifest(nil)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	s := deletionServer(t, t.TempDir(), projects)
	s.cfg.AuthUsername = "admin"
	s.assets = assets
	stateDir := s.cfg.StateDir
	assistants, _, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	s.assistants = assistants
	s.assistantRecent = recent.NewCapped(filepath.Join(stateDir, "recent-assistants.json"), recentEntries)
	s.watcher = assistant.NewWatcher(assistants, assistant.NewJobs(assistant.NewStore(stateDir)), nil, nil)
	s.settings = settings.New(filepath.Join(stateDir, "settings.json"))
	s.backups = backup.New(stateDir, projects.Root, "test")
	s.activity = activity.NewTracker()
	r := gin.New()
	r.SetHTMLTemplate(render.HTMLTemplate(assets.assetPath, "test", "test", nil, nil))
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.GET("/test/sign-in", func(c *gin.Context) {
		ginsessions.Default(c).Set(sessionUserKey, "admin")
		c.String(http.StatusOK, s.csrfToken(c))
	})
	s.registerRoutes(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test/sign-in", nil))
	session, _, _ := strings.Cut(rec.Header().Get("Set-Cookie"), ";")
	token := rec.Body.String()
	if session == "" || token == "" {
		t.Fatal("the sign-in answered no session")
	}
	return func(method, path, contentType, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("Cookie", session)
		req.Header.Set("X-CSRF-Token", token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusSeeOther {
			if next, _, _ := strings.Cut(rec.Header().Get("Set-Cookie"), ";"); next != "" {
				session = next
			}
		}
		return rec
	}
}

// notice answers the text of the notice a page shows on top, "" without one.
func notice(page string) string {
	m := regexp.MustCompile(`(?s)dc-notice-text">(.*?)</div>`).FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	return html.UnescapeString(strings.TrimSpace(m[1]))
}

func answered(t *testing.T, rec *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var answer struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("answered %d %q", rec.Code, rec.Body.String())
	}
	return answer.Message, answer.Error
}

// pageFields answers what a rendered page's form posts as it stands: its
// hidden fields and text fields as a browser sends them, the token left out.
func pageFields(t *testing.T, page string) url.Values {
	t.Helper()
	page = regexp.MustCompile(`(?s)<template.*?</template>`).ReplaceAllString(page, "")
	form := url.Values{}
	for _, field := range regexp.MustCompile(`<input type="(?:hidden|text)"(?: class="[^"]*")? name="([^"]*)" value="([^"]*)"`).FindAllStringSubmatch(page, -1) {
		if field[1] != "csrf_token" {
			form.Add(html.UnescapeString(field[1]), html.UnescapeString(field[2]))
		}
	}
	return form
}

// tagsPage renders the settings page as the server would after a save that
// sent sent and was refused with refused, nil for a plain load, and answers
// it with the message on top.
func tagsPage(t *testing.T, projects *project.Repository, sent url.Values, refused error) (string, string) {
	t.Helper()
	s := &Server{projects: projects}
	var changes []project.TagChange
	if sent != nil {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, settingsTagsPath, strings.NewReader(sent.Encode()))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		changes = sentTagChanges(c)
	}
	data, problem := s.settingsTagsData(changes, refused)
	data.Page = render.Page{CSRFToken: "tok"}
	var out strings.Builder
	if err := render.HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil).ExecuteTemplate(&out, "settings_projects_tags.gohtml", data); err != nil {
		t.Fatal(err)
	}
	return out.String(), problem
}

func tagLines(t *testing.T, projects *project.Repository, name string) []string {
	t.Helper()
	var got []string
	for _, tag := range tagsOf(t, projects, name) {
		got = append(got, tag.ID+":"+tag.Name+":"+tag.Color)
	}
	return got
}

// Settings › Projects › Tags lists every tag with the fields its save posts
// and saves what the page staged in one request: a rename, a recolor, a
// merge and a removal land at once on every project and the page answers
// with what was done. The routes the tags had before are gone, and a
// project named like them still reaches its own tags form.
func TestTheTagSettingsSaveEveryChangeAtOnce(t *testing.T) {
	projects, root := tagsFixture(t)
	if err := os.MkdirAll(filepath.Join(root, "tags"), 0o755); err != nil {
		t.Fatal(err)
	}
	send := tagsRouter(t, projects)
	for _, save := range []struct {
		name string
		form url.Values
	}{
		{"tags", url.Values{"new": {"api", "go"}, "new-color:api": {"pink"}, "new-color:go": {"azure"}}},
		{"main", url.Values{"new": {"api", "backend", "golang", "old"}, "new-color:backend": {"lime"}, "new-color:golang": {"green"}, "new-color:old": {"red"}}},
	} {
		name, form := save.name, save.form
		rec := send("/projects/"+name+"/tags?modal=1", form)
		if message, _ := answered(t, rec); rec.Code != http.StatusOK || message != fmt.Sprintf("Tags of %q saved.", name) {
			t.Fatalf("the tags of %s answered %d %q", name, rec.Code, message)
		}
	}
	ids := map[string]string{}
	for _, tag := range append(tagsOf(t, projects, "main"), tagsOf(t, projects, "tags")...) {
		ids[tag.Name] = tag.ID
	}

	page, problem := tagsPage(t, projects, nil, nil)
	if problem != "" || !strings.Contains(page, `<dc-tags-settings class="dc-work" role="main" data-next-colors="`) || !strings.Contains(page, `href="/projects?tag=`+ids["api"]+`" title="Show the projects tagged api">2 projects</a>`) {
		t.Fatalf("the settings say %q in %s", problem, page)
	}
	form := pageFields(t, page)
	if len(form["tag"]) != 5 || form.Get("was:"+ids["golang"]) != "golang" || form.Get("color:"+ids["go"]) != "azure" || form.Get("version:"+ids["api"]) == "" {
		t.Fatalf("the page posts %v", form)
	}
	form.Set("name:"+ids["api"], "gateway")
	form.Set("color:"+ids["backend"], "orange")
	form.Set("name:"+ids["golang"], "go")
	form.Set("remove:"+ids["old"], "1")
	rec := send("/settings/projects/tags", form)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/projects/tags" {
		t.Fatalf("the save answered %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	for name, want := range map[string][]string{
		"main": {ids["backend"] + ":backend:orange", ids["api"] + ":gateway:pink", ids["go"] + ":go:azure"},
		"tags": {ids["api"] + ":gateway:pink", ids["go"] + ":go:azure"},
	} {
		if got := tagLines(t, projects, name); !slices.Equal(got, want) {
			t.Fatalf("%s shows %q, want %q", name, got, want)
		}
	}
	if got := tagsSavedMessage(project.TagsSaved{Renamed: 1, Recolored: 1, Merged: 1, Removed: 1}); got != "Tags saved: 1 renamed, 1 recolored, 1 merged, 1 removed." {
		t.Fatalf("the message reads %q", got)
	}

	r := gin.New()
	(&Server{}).registerRoutes(r)
	for _, route := range r.Routes() {
		if strings.HasPrefix(route.Path, "/projects/tags") {
			t.Fatalf("the route %s %s is still there", route.Method, route.Path)
		}
	}
	rec = send("/projects/tags/tags?modal=1", url.Values{"tag": {ids["go"]}})
	if message, _ := answered(t, rec); rec.Code != http.StatusOK || message != `Tags of "tags" saved.` {
		t.Fatalf("the project named tags answered %d %q", rec.Code, message)
	}
}

// A page that loaded the tags before they changed elsewhere saves nothing:
// it comes back with the tags as they are now, what it sent standing in the
// fields of every other tag and the reason on top. A name that is no name is
// refused the same way.
func TestAStaleTagSettingsPageChangesNothing(t *testing.T) {
	projects, _ := tagsFixture(t)
	send := tagsRouter(t, projects)
	send("/projects/main/tags?modal=1", url.Values{"new": {"api", "web"}, "new-color:api": {"pink"}, "new-color:web": {"blue"}})
	ids := map[string]string{}
	for _, tag := range tagsOf(t, projects, "main") {
		ids[tag.Name] = tag.ID
	}
	loaded, _ := tagsPage(t, projects, nil, nil)
	form, other := pageFields(t, loaded), pageFields(t, loaded)
	other.Set("color:"+ids["web"], "teal")
	if rec := send("/settings/projects/tags", other); rec.Code != http.StatusSeeOther {
		t.Fatalf("the other page's save answered %d", rec.Code)
	}
	before := tagLines(t, projects, "main")

	form.Set("name:"+ids["api"], "gateway")
	form.Set("color:"+ids["web"], "lime")
	_, err := projects.SaveTags(sentChanges(t, form))
	page, problem := tagsPage(t, projects, form, err)
	if problem != "Changed elsewhere since this page loaded: web. Nothing was saved, the page shows them as they are now." {
		t.Fatalf("the stale save says %q", problem)
	}
	back := pageFields(t, page)
	if back.Get("name:"+ids["api"]) != "gateway" || back.Get("was:"+ids["api"]) != "api" || back.Get("color:"+ids["web"]) != "teal" || back.Get("was-color:"+ids["web"]) != "teal" {
		t.Fatalf("the refused page posts %v", back)
	}
	if got := tagLines(t, projects, "main"); !slices.Equal(got, before) {
		t.Fatalf("the stale save changed the tags to %q", got)
	}

	back.Set("name:"+ids["api"], "  ")
	_, err = projects.SaveTags(sentChanges(t, back))
	if _, problem := tagsPage(t, projects, back, err); problem != "A tag needs a name." {
		t.Fatalf("a nameless tag says %q", problem)
	}
	back.Set("name:"+ids["api"], "gateway")
	if rec := send("/settings/projects/tags", back); rec.Code != http.StatusSeeOther {
		t.Fatalf("the save after the refusal answered %d %s", rec.Code, rec.Body.String())
	}
	if got := tagLines(t, projects, "main"); !slices.Equal(got, []string{ids["api"] + ":gateway:pink", ids["web"] + ":web:teal"}) {
		t.Fatalf("the save after the refusal made %q", got)
	}
}

// A refused save shows every tag the page left alone as it is now, so a
// rename made elsewhere meanwhile survives the next save, while an edit of a
// tag nobody else touched stands for it. An edit of a tag that moved
// elsewhere is dropped and named, on a refusal for another reason too, and
// no save afterwards puts an older value back.
func TestARefusedSaveNeverBringsBackAnOlderValue(t *testing.T) {
	projects, _ := tagsFixture(t)
	send := tagsRouter(t, projects)
	send("/projects/main/tags?modal=1", url.Values{"new": {"api", "ops", "web", "wip"}, "new-color:api": {"pink"}, "new-color:ops": {"red"}, "new-color:web": {"blue"}, "new-color:wip": {"lime"}})
	ids := map[string]string{}
	for _, tag := range tagsOf(t, projects, "main") {
		ids[tag.Name] = tag.ID
	}
	loaded, _ := tagsPage(t, projects, nil, nil)
	form, elsewhere := pageFields(t, loaded), pageFields(t, loaded)
	elsewhere.Set("name:"+ids["wip"], "in-progress")
	elsewhere.Set("color:"+ids["web"], "teal")
	if rec := send(settingsTagsPath, elsewhere); rec.Code != http.StatusSeeOther {
		t.Fatalf("the save elsewhere answered %d", rec.Code)
	}

	form.Set("name:"+ids["api"], "gateway")
	form.Set("color:"+ids["web"], "lime")
	_, err := projects.SaveTags(sentChanges(t, form))
	page, problem := tagsPage(t, projects, form, err)
	if !strings.HasPrefix(problem, "Changed elsewhere since this page loaded: web.") {
		t.Fatalf("the refusal says %q", problem)
	}
	back := pageFields(t, page)
	if back.Get("name:"+ids["wip"]) != "in-progress" || back.Get("was:"+ids["wip"]) != "in-progress" {
		t.Fatalf("the refused page shows wip as %q over %q", back.Get("name:"+ids["wip"]), back.Get("was:"+ids["wip"]))
	}
	if back.Get("name:"+ids["api"]) != "gateway" || back.Get("color:"+ids["web"]) != "teal" {
		t.Fatalf("the refused page posts %v", back)
	}
	if rec := send(settingsTagsPath, back); rec.Code != http.StatusSeeOther {
		t.Fatalf("the next save answered %d %s", rec.Code, rec.Body.String())
	}
	if got := tagLines(t, projects, "main"); !slices.Equal(got, []string{ids["api"] + ":gateway:pink", ids["wip"] + ":in-progress:lime", ids["ops"] + ":ops:red", ids["web"] + ":web:teal"}) {
		t.Fatalf("the next save made %q", got)
	}

	loaded, _ = tagsPage(t, projects, nil, nil)
	form, elsewhere = pageFields(t, loaded), pageFields(t, loaded)
	elsewhere.Set("name:"+ids["ops"], "ops2")
	if rec := send(settingsTagsPath, elsewhere); rec.Code != http.StatusSeeOther {
		t.Fatalf("the second save elsewhere answered %d", rec.Code)
	}
	form.Set("name:"+ids["ops"], "opsx")
	form.Set("name:"+ids["api"], " ")
	_, err = projects.SaveTags(sentChanges(t, form))
	page, problem = tagsPage(t, projects, form, err)
	if problem != "A tag needs a name. Changed elsewhere since this page loaded: ops. Nothing was saved, the page shows them as they are now." {
		t.Fatalf("the refusal says %q", problem)
	}
	back = pageFields(t, page)
	if back.Get("name:"+ids["ops"]) != "ops2" || back.Get("name:"+ids["api"]) != " " {
		t.Fatalf("the refused page posts %v", back)
	}
	back.Set("name:"+ids["api"], "gate")
	if rec := send(settingsTagsPath, back); rec.Code != http.StatusSeeOther {
		t.Fatalf("the save after the fix answered %d %s", rec.Code, rec.Body.String())
	}
	if got := tagLines(t, projects, "main"); !slices.Equal(got, []string{ids["api"] + ":gate:pink", ids["wip"] + ":in-progress:lime", ids["ops"] + ":ops2:red", ids["web"] + ":web:teal"}) {
		t.Fatalf("the save after the fix made %q", got)
	}
}

func sentChanges(t *testing.T, form url.Values) []project.TagChange {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, settingsTagsPath, strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return sentTagChanges(c)
}

// A page that loaded tags another page then removed, the last ones, is
// refused as stale by the names it loaded, and comes back with no list but
// the reason, while a file that cannot be read says so.
func TestASaveOverAnEmptiedFileSaysTheTagsChanged(t *testing.T) {
	projects, _ := tagsFixture(t)
	send := tagsRouter(t, projects)
	send("/projects/main/tags?modal=1", url.Values{"new": {"api", "web"}, "new-color:api": {"pink"}, "new-color:web": {"blue"}})
	ids := map[string]string{}
	for _, tag := range tagsOf(t, projects, "main") {
		ids[tag.Name] = tag.ID
	}
	loaded, _ := tagsPage(t, projects, nil, nil)
	form, elsewhere := pageFields(t, loaded), pageFields(t, loaded)
	elsewhere.Set("remove:"+ids["api"], "1")
	elsewhere.Set("remove:"+ids["web"], "1")
	if rec := send(settingsTagsPath, elsewhere); rec.Code != http.StatusSeeOther {
		t.Fatalf("the removal elsewhere answered %d", rec.Code)
	}
	form.Set("name:"+ids["api"], "gateway")
	_, err := projects.SaveTags(sentChanges(t, form))
	page, problem := tagsPage(t, projects, form, err)
	if problem != "Changed elsewhere since this page loaded: api. Nothing was saved, the page shows them as they are now." {
		t.Fatalf("the stale save says %q", problem)
	}
	if !strings.Contains(page, "No tags yet.") {
		t.Fatalf("the refused page shows %s", page)
	}

	stateDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(stateDir, "project-tags.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	unreadable := project.NewRepository(projects.Root, nil).UseTags(project.NewTags(stateDir))
	_, err = unreadable.SaveTags(sentChanges(t, form))
	if _, problem := tagsPage(t, unreadable, form, err); problem != "The tags could not be read, nothing was changed." {
		t.Fatalf("a save over an unreadable file says %q", problem)
	}
}

// The settings make new tags from the rows the page added, by new-name and
// new-color in the order they stand: they stand with no project and the
// projects' form offers them. A refused save comes back with the new rows it
// sent, their colors only from the palette.
func TestTheTagSettingsMakeNewTags(t *testing.T) {
	projects, _ := tagsFixture(t)
	send := tagsRouter(t, projects)
	loaded, _ := tagsPage(t, projects, nil, nil)
	if !strings.Contains(loaded, "No tags yet.") || !strings.Contains(loaded, "data-tags-add") {
		t.Fatalf("an empty page offers no new tag: %s", loaded)
	}
	form := pageFields(t, loaded)
	form["new-name"] = []string{"backend", "", "ops"}
	form["new-color"] = []string{"teal", "red", ""}
	if rec := send(settingsTagsPath, form); rec.Code != http.StatusSeeOther {
		t.Fatalf("the creation answered %d %s", rec.Code, rec.Body.String())
	}
	if got := tagsSavedMessage(project.TagsSaved{Created: 2}); got != "Tags saved: 2 created." {
		t.Fatalf("the message reads %q", got)
	}
	states, err := projects.TagStates()
	if err != nil || len(states) != 2 || states[0].Name != "backend" || states[0].Color != "teal" || states[1].Name != "ops" || len(states[1].Projects) != 0 {
		t.Fatalf("the new tags are %+v, %v", states, err)
	}
	offered := render.ProjectTagsData{InUse: projects.KnownTags(projects.List())}.Offered()
	if len(offered) != 2 {
		t.Fatalf("the projects' form offers %+v", offered)
	}

	refused := pageFields(t, loaded)
	refused["new-name"] = []string{"fresh", "worse"}
	refused["new-color"] = []string{"lime", "teal;}"}
	_, err = projects.SaveTags(sentChanges(t, refused))
	page, problem := tagsPage(t, projects, refused, err)
	if !strings.Contains(problem, "none of the tag colors") || !strings.Contains(page, `name="new-name" value="fresh"`) || !strings.Contains(page, `name="new-name" value="worse"`) || strings.Contains(page, "teal;}") {
		t.Fatalf("the refusal says %q and shows %s", problem, page)
	}
}

// A tags file that cannot be read shows the reason and no list, never an
// empty one, and nothing to save.
func TestTheTagSettingsOfAnUnreadableFileShowNoList(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(stateDir, "project-tags.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	page, problem := tagsPage(t, project.NewRepository(t.TempDir(), nil).UseTags(project.NewTags(stateDir)), nil, nil)
	if problem != "The tags could not be read, nothing was changed." || strings.Contains(page, "data-tags-form") || strings.Contains(page, "No tags yet") {
		t.Fatalf("the unreadable tags say %q in %s", problem, page)
	}
}

// The server checks every input of the tag routes itself, the forms' own
// checks are only a convenience. Hand built requests with names of any length
// or character, too many tags, ids of no tag, colors outside the palette,
// bodies that are no form and fields left out are each refused with a 4xx or
// the page's own refusal, never a 500, under a message that stays well inside
// what a session cookie holds and never quotes more than a tag may hold, and
// leave the file as it was.
func TestHandBuiltTagRequestsAreRefusedCleanly(t *testing.T) {
	projects, _ := tagsFixture(t)
	raw := tagsSession(t, projects)
	const form = "application/x-www-form-urlencoded"
	if rec := raw(http.MethodPost, "/projects/main/tags?modal=1", form, url.Values{"new": {"api"}}.Encode()); rec.Code != http.StatusOK {
		t.Fatalf("the first save answered %d %s", rec.Code, rec.Body.String())
	}
	states, err := projects.TagStates()
	if err != nil || len(states) != 1 {
		t.Fatalf("the tags are %+v, %v", states, err)
	}
	api := states[0]
	long := strings.Repeat("x", 5000)
	many := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s%d", prefix, i)
		}
		return out
	}
	row := func(change url.Values) string {
		values := url.Values{"tag": {api.ID}, "version:" + api.ID: {api.Version}, "was:" + api.ID: {api.Name}, "was-color:" + api.ID: {api.Color}, "name:" + api.ID: {api.Name}, "color:" + api.ID: {api.Color}}
		for field, value := range change {
			values[field] = value
		}
		return values.Encode()
	}
	stale := url.Values{}
	for _, id := range many("gone", 2000) {
		stale.Add("tag", id)
		stale.Set("was:"+id, strings.Repeat("y", 100))
		stale.Set("name:"+id, "renamed")
		stale.Set("color:"+id, "red")
	}
	multipartBody := func(field, value string, whole bool) (string, string) {
		var body strings.Builder
		w := multipart.NewWriter(&body)
		if err := w.WriteField(field, value); err != nil {
			t.Fatal(err)
		}
		if whole {
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
		}
		return w.FormDataContentType(), body.String()
	}

	type request struct{ name, method, path, contentType, body string }
	var requests []request
	for _, path := range []string{"/projects/main/tags?modal=1", "/projects/main/tags"} {
		for name, values := range map[string]url.Values{
			"an overlong name":            {"new": {long}},
			"a control character":         {"new": {"bell\x07"}},
			"a direction override":        {"new": {"rlo\u202eevil"}},
			"a zero width space":          {"new": {"zw\u200bsp"}},
			"a direction isolate":         {"new": {"iso\u2066x"}},
			"a byte order mark":           {"new": {"bom\ufeff"}},
			"bytes that are no text":      {"new": {"a\xffb"}},
			"too many new names":          {"new": many("t", 21)},
			"too many ids":                {"tag": many("id", 21)},
			"an id of no tag":             {"tag": {"nope"}},
			"an overlong id":              {"tag": {long}},
			"a markup id":                 {"tag": {`"><img src=x>`}},
			"an overlong color":           {"new": {"web"}, "new-color:web": {long}},
			"a color outside the palette": {"new": {"web"}, "new-color:web": {"teal;}"}},
			"an overlong color field":     {"new": {"web"}, "new-color:" + long: {"teal"}},
		} {
			requests = append(requests, request{name, http.MethodPost, path, form, values.Encode()})
		}
		kind, body := multipartBody("new", long, true)
		requests = append(requests,
			request{"a multipart overlong name", http.MethodPost, path, kind, body},
			request{"JSON", http.MethodPost, path, "application/json", `{"new":["web"]}`},
			request{"plain text", http.MethodPost, path, "text/plain", "new=web"},
			request{"no body", http.MethodPost, path, "", ""},
		)
		kind, body = multipartBody("new", "web", false)
		requests = append(requests, request{"a multipart body that breaks off", http.MethodPost, path, kind, body})
	}
	for _, path := range []string{"/projects/" + long + "/tags", "/projects/a%00" + long + "/tags"} {
		requests = append(requests,
			request{"an overlong project", http.MethodPost, path, form, url.Values{"new": {"web"}}.Encode()},
			request{"an overlong project in the dialog", http.MethodPost, path + "?modal=1", form, url.Values{"new": {"web"}}.Encode()},
			request{"the form of an overlong project", http.MethodGet, path, "", ""},
		)
	}
	requests = append(requests, request{"a worktree", http.MethodPost, "/projects/feature/tags?modal=1", form, url.Values{"new": {"web"}}.Encode()})
	for name, body := range map[string]string{
		"a rename to an overlong name":            row(url.Values{"name:" + api.ID: {long}}),
		"a rename to a direction override":        row(url.Values{"name:" + api.ID: {"rlo\u202eevil"}}),
		"a crafted loaded name kept on a recolor": row(url.Values{"was:" + api.ID: {long}, "name:" + api.ID: {long}, "color:" + api.ID: {"lime"}}),
		"a recolor outside the palette":           row(url.Values{"color:" + api.ID: {long}}),
		"a row without its fields":                url.Values{"tag": {api.ID}}.Encode(),
		"a new overlong name":                     row(url.Values{"new-name": {long}}),
		"a new name with a zero width space":      row(url.Values{"new-name": {"zw\u200bsp"}}),
		"a new color outside the palette":         row(url.Values{"new-name": {"web"}, "new-color": {long}}),
		"thousands of rows of tags there are not": stale.Encode(),
		"more new tags than one save makes":       url.Values{"new-name": many("n", 21)}.Encode(),
	} {
		requests = append(requests, request{name, http.MethodPost, settingsTagsPath, form, body})
	}
	kind, body := multipartBody("new-name", "a\xffb", true)
	requests = append(requests,
		request{"settings: a multipart name of bytes that are no text", http.MethodPost, settingsTagsPath, kind, body},
		request{"settings: JSON", http.MethodPost, settingsTagsPath, "application/json", `{"tag":["x"]}`},
		request{"settings: no body", http.MethodPost, settingsTagsPath, "", ""},
	)

	before := tagLines(t, projects, "main")
	for _, req := range requests {
		rec := raw(req.method, req.path, req.contentType, req.body)
		var message string
		switch rec.Code {
		case http.StatusBadRequest:
			_, message = answered(t, rec)
		case http.StatusOK:
			message = notice(rec.Body.String())
		case http.StatusSeeOther:
			message = notice(raw(http.MethodGet, settingsTagsPath, "", "").Body.String())
		default:
			t.Errorf("%s to %.60s answered %d", req.name, req.path, rec.Code)
			continue
		}
		if message == "" || len(message) > 2048 || strings.Contains(message, strings.Repeat("x", 41)) || strings.Contains(message, strings.Repeat("y", 41)) || !utf8.ValidString(message) || strings.ContainsAny(message, "\u202e\u200b\u2066\ufeff\x07") {
			t.Errorf("%s to %.60s answered %d with %q", req.name, req.path, rec.Code, message)
		}
		if got := tagLines(t, projects, "main"); !slices.Equal(got, before) {
			t.Fatalf("%s to %.60s changed the tags to %q", req.name, req.path, got)
		}
	}
	if states, err := projects.TagStates(); err != nil || len(states) != 1 {
		t.Fatalf("the refused requests left the tags %+v, %v", states, err)
	}
}
