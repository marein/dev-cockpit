package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/activity"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/backup"
	"github.com/marein/dev-cockpit/internal/coder"
	coderclaude "github.com/marein/dev-cockpit/internal/coder/claude"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/tmux"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// modelSettingsServer renders the two settings pages the defaults are edited
// on, over a throwaway state directory, with one claude coder whose
// repository keeps its added names in that directory's settings store. PATH
// is emptied so no page render can reach a tmux on this machine.
func modelSettingsServer(t *testing.T) (*gin.Engine, *Server) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	assistants, _, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	serves, err := pluginhost.ConfigureServe(nil, "", "", nil, nil)
	if err != nil {
		t.Fatalf("ConfigureServe() = %v", err)
	}
	assets, err := newStaticAssetManifest(serves)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	projectsDir := t.TempDir()
	projects := project.NewRepository(projectsDir, recent.New(filepath.Join(stateDir, "recent-projects.json")))
	store := settings.New(filepath.Join(stateDir, "settings.json"))
	s := &Server{
		cfg:             config.Config{StateDir: stateDir},
		assistants:      assistants,
		assistantRecent: recent.NewCapped(filepath.Join(stateDir, "recent-assistants.json"), recentEntries),
		assets:          assets,
		plugins:         serves,
		projects:        projects,
		settings:        store,
		coders:          []*coder.Manager{coder.NewManager(config.Config{}, tmux.New(), coderclaude.New("", store), projects)},
	}
	s.shells = shell.NewShells(config.Config{}, tmux.New(), projects, func() bool { return false })
	s.notifier = notify.NewService(filepath.Join(stateDir, "notifications.json"), nil)
	s.watcher = assistant.NewWatcher(s.assistants, assistant.NewJobs(assistant.NewStore(stateDir)), nil, nil)
	s.backups = backup.New(stateDir, projectsDir, "test")
	s.activity = activity.NewTracker()
	r := gin.New()
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.SetHTMLTemplate(render.HTMLTemplate(assets.assetPath, "test", "test", serves))
	r.GET("/settings/assistant/models", s.handleSettingsAssistantModels)
	r.POST("/settings/assistant/models", s.handleSettingsAssistantModelsSave)
	co := s.coders[0]
	base := s.coderBase(co)
	r.GET(base+"/models", s.handleCoderModels(co))
	r.POST(base+"/models", s.handleCoderModelsSave(co))
	r.POST(base+"/models/add", s.handleCoderModelAdd(co))
	r.POST(base+"/models/delete", s.handleCoderModelDelete(co))
	return r, s
}

func postForm(t *testing.T, r *gin.Engine, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(rec, req)
	return rec
}

func getPage(t *testing.T, r *gin.Engine, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// The Models tab round trips: a save stores the three keys of the coder, a
// typed name among them, and the next render shows them selected, with the
// Chat pick's empty entry naming the coder's start default once one stands,
// the Checks and Triggers picks' reading Same as chat, and the tab's one line
// saying it applies to new assistants. A refused name stores nothing.
func TestTheModelsTabRoundTrips(t *testing.T) {
	r, s := modelSettingsServer(t)
	page := getPage(t, r, "/settings/assistant/models")
	for _, want := range []string{
		`<option value="" selected>Coder default (CLI)</option>`,
		`<option value="" selected>Same as chat</option>`,
		"Applies to new assistants.",
		"Aliases, always the newest of each family.",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the fresh tab misses %q", want)
		}
	}

	rec := postForm(t, r, "/settings/assistant/models", url.Values{
		"chat-claude": {" haiku "}, "check-claude": {""}, "trigger-claude": {"claude-haiku-4-5"},
	})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/assistant/models" {
		t.Fatalf("want the save to land back on the tab, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := assistant.ModelDefaultsFor(s.settings, "claude"); got != (assistant.ModelDefaults{Chat: "haiku", Trigger: "claude-haiku-4-5"}) {
		t.Fatalf("want the three keys stored cleaned, got %+v", got)
	}
	page = getPage(t, r, "/settings/assistant/models")
	for _, want := range []string{
		`<option value="haiku" selected>haiku</option>`,
		`<option value="claude-haiku-4-5" selected>claude-haiku-4-5</option>`,
		`<option value="" selected>Same as chat</option>`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the tab after the save misses %q", want)
		}
	}
	if !coder.ModelRepositoryFor(s.coders[0].Coder()).Exists("claude-haiku-4-5") {
		t.Fatal("want the typed name remembered by the coder's repository")
	}

	rec = postForm(t, r, "/settings/assistant/models", url.Values{"chat-claude": {"two words"}})
	if rec.Code != http.StatusSeeOther || assistant.ModelDefaultsFor(s.settings, "claude").Chat != "haiku" {
		t.Fatalf("want a refused name to store nothing, got %d and %+v", rec.Code, assistant.ModelDefaultsFor(s.settings, "claude"))
	}

	postForm(t, r, "/settings/coders/claude/models", url.Values{"start": {"fable"}})
	postForm(t, r, "/settings/assistant/models", url.Values{"chat-claude": {""}})
	page = getPage(t, r, "/settings/assistant/models")
	if !strings.Contains(page, `<option value="" selected>Coder default (fable)</option>`) {
		t.Fatal("want the Chat pick's empty entry to name the coder's start default")
	}
}

// The coder page round trips: a save stores the start key and the next
// render shows it selected, the added names control says so while it holds
// nothing, an added name stands as a row with its remove form afterwards and
// the line comes back once it is removed, and a CLI name is refused with the
// repository's own sentence, stored nowhere.
func TestTheCoderStartKeyRoundTrips(t *testing.T) {
	r, s := modelSettingsServer(t)
	page := getPage(t, r, "/settings/coders/claude/models")
	for _, want := range []string{
		`<option value="" selected>Default (CLI)</option>`,
		`data-added-models-empty>No names added yet.<`,
		`action="/settings/coders/claude/models/add"`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the fresh coder page misses %q", want)
		}
	}

	rec := postForm(t, r, "/settings/coders/claude/models", url.Values{"start": {" opus "}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/coders/claude/models" {
		t.Fatalf("want the save to land back on the page, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := assistant.ModelDefaultsFor(s.settings, "claude").Start; got != "opus" {
		t.Fatalf("want the start key stored cleaned, got %q", got)
	}
	page = getPage(t, r, "/settings/coders/claude/models")
	if !strings.Contains(page, `<option value="opus" selected>opus</option>`) {
		t.Fatal("the page after the save does not show the stored start default selected")
	}

	postForm(t, r, "/settings/coders/claude/models/add", url.Values{"name": {"claude-haiku-4-5"}})
	page = getPage(t, r, "/settings/coders/claude/models")
	if !strings.Contains(page, `data-added-model="claude-haiku-4-5"`) || strings.Contains(page, "No names added yet.") {
		t.Fatal("want the added name as a row and the empty line gone")
	}
	if !strings.Contains(page, `<option value="claude-haiku-4-5">claude-haiku-4-5</option>`) {
		t.Fatal("want the added name in the Start pick's list")
	}

	postForm(t, r, "/settings/coders/claude/models/delete", url.Values{"name": {"opus"}})
	if !coder.ModelRepositoryFor(s.coders[0].Coder()).Exists("opus") {
		t.Fatal("a CLI name must survive a delete")
	}
	postForm(t, r, "/settings/coders/claude/models/delete", url.Values{"name": {"claude-haiku-4-5"}})
	page = getPage(t, r, "/settings/coders/claude/models")
	if strings.Contains(page, `data-added-model=`) || !strings.Contains(page, "No names added yet.") {
		t.Fatal("want the removed name gone and the empty line back")
	}
}
