package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/activity"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/backup"
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

// The ids are what the store accepts, one per letter the checks read them by.
const (
	assistantA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	assistantB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	assistantC = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

// assistantEntryServer builds the area's entry over a throwaway state dir,
// holding the assistants in the order they are given, which is the order the
// list is sorted into.
func assistantEntryServer(t *testing.T, ids ...string) (*gin.Engine, *Server, *recent.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	store := assistant.NewStore(stateDir)
	// Save prepends, so the last one saved stands first: saving backwards
	// leaves the list in the order the ids were given.
	for i := len(ids) - 1; i >= 0; i-- {
		store.Save(assistant.Instance{Summary: assistant.Summary{ID: ids[i], CoderID: "claude"}})
	}
	assistants, _, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	opened := recent.NewCapped(filepath.Join(stateDir, "recent-assistants.json"), recentEntries)
	s := &Server{cfg: config.Config{StateDir: stateDir}, assistants: assistants, assistantRecent: opened}
	r := gin.New()
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.GET("/assistants", s.handleAssistantsEntry)
	return r, s, opened
}

func assistantEntry(t *testing.T, r *gin.Engine) (int, string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assistants", nil))
	return rec.Code, rec.Header().Get("Location"), rec.Header().Get("Cache-Control")
}

// The assistant looked at last is what the area opens, and the answer stays a
// See Other nobody may cache: a permanent one would pin the browser to the
// assistant of the first click forever.
func TestAssistantsEntryOpensTheOneLookedAtLast(t *testing.T) {
	r, _, opened := assistantEntryServer(t, assistantA, assistantB, assistantC)
	opened.Touch(assistantC)

	code, location, cache := assistantEntry(t, r)
	if code != http.StatusSeeOther || location != "/assistants/"+assistantC {
		t.Fatalf("expected a 303 to ccc, got %d %q", code, location)
	}
	if cache != "no-store" {
		t.Fatalf("expected no-store, got %q", cache)
	}
}

// One that was deleted since falls through to the next one remembered, and
// with none of them left to the first row of the list.
func TestAssistantsEntryFallsToTheOrderOfTheList(t *testing.T) {
	r, _, opened := assistantEntryServer(t, assistantA, assistantB)
	opened.Touch(assistantB)
	opened.Touch("99999999-9999-4999-8999-999999999999")

	code, location, _ := assistantEntry(t, r)
	if code != http.StatusSeeOther || location != "/assistants/"+assistantB {
		t.Fatalf("expected a 303 to bbb, got %d %q", code, location)
	}

	fresh, _, _ := assistantEntryServer(t, assistantA, assistantB)
	code, location, _ = assistantEntry(t, fresh)
	if code != http.StatusSeeOther || location != "/assistants/"+assistantA {
		t.Fatalf("expected a 303 to the first row, got %d %q", code, location)
	}
}

// With no assistant at all there is nothing to open: the entry names none and
// the area renders itself, the empty state whose button makes the first one.
func TestAssistantsEntryNamesNoneWithNoAssistant(t *testing.T) {
	_, s, _ := assistantEntryServer(t)
	if target := s.assistantEntryTarget(); target != "" {
		t.Fatalf("an empty area named %q", target)
	}
}

// assistantPageServer is the entry server with the whole shell behind it, so
// the page of one assistant renders for real: the page handler builds the
// layout, and the layout asks the server for everything a page shows.
func assistantPageServer(t *testing.T, ids ...string) (*gin.Engine, *recent.Store) {
	t.Helper()
	r, s, opened := assistantEntryServer(t, ids...)
	stateDir := s.cfg.StateDir
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
	s.assets = assets
	s.plugins = serves
	s.projects = projects
	s.shells = shell.NewShells(config.Config{}, tmux.New(), projects, func() bool { return false })
	s.settings = settings.New(filepath.Join(stateDir, "settings.json"))
	s.notifier = notify.NewService(filepath.Join(stateDir, "notifications.json"), nil)
	s.watcher = assistant.NewWatcher(s.assistants, assistant.NewJobs(assistant.NewStore(stateDir)), nil, nil)
	s.backups = backup.New(stateDir, projectsDir, "test")
	s.activity = activity.NewTracker()
	r.SetHTMLTemplate(render.HTMLTemplate(assets.assetPath, "test", "test", serves))
	r.GET("/assistants/:id", s.handleAssistantPage)
	return r, opened
}

// The page pulls itself again on every assistant event, in background windows
// too, and a pull is not a look: a window left open on one assistant must not
// make it the one looked at last while the user reads another. The pull says
// so on its header, and only the render without it moves the recency.
func TestAPullOfTheAssistantPageIsNotALook(t *testing.T) {
	r, opened := assistantPageServer(t, assistantA, assistantB)
	opened.Touch(assistantB)

	fetch := func(pull bool) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/assistants/"+assistantA, nil)
		if pull {
			req.Header.Set(pullHeader, "1")
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := fetch(true); code != http.StatusOK {
		t.Fatalf("the pull did not render the page: %d", code)
	}
	if _, looked := opened.Times()[assistantA]; looked {
		t.Fatalf("a pull of the page counted as a look: %v", opened.Names())
	}
	if names := opened.Names(); len(names) != 1 || names[0] != assistantB {
		t.Fatalf("the pull moved the recency: %v", names)
	}

	if code := fetch(false); code != http.StatusOK {
		t.Fatalf("the page did not render: %d", code)
	}
	if _, looked := opened.Times()[assistantA]; !looked {
		t.Fatalf("opening the page did not count as a look: %v", opened.Names())
	}
}
