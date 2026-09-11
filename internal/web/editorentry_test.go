package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/recent"
)

// editorEntryServer builds the entry over a throwaway projects root, with the
// two stores the chain reads: the editor's own memory and the cockpit wide
// recent projects order.
func editorEntryServer(t *testing.T, names ...string) (*gin.Engine, *recent.Store, *recent.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	state := t.TempDir()
	projectsRecent := recent.New(filepath.Join(state, "recent-projects.json"))
	editorRecent := recent.NewCapped(filepath.Join(state, "recent-editor-projects.json"), recentEntries)
	s := &Server{projects: project.NewRepository(root, projectsRecent), editorRecent: editorRecent}
	r := gin.New()
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.GET("/editor", s.handleEditorEntry)
	return r, editorRecent, projectsRecent
}

func editorEntry(t *testing.T, r *gin.Engine) (int, string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/editor", nil))
	return rec.Code, rec.Header().Get("Location"), rec.Header().Get("Cache-Control")
}

// The project the editor was last opened on wins, and the answer stays a See
// Other nobody may cache.
func TestEditorEntryOpensTheProjectRememberedLast(t *testing.T) {
	r, editorRecent, projectsRecent := editorEntryServer(t, "alpha", "beta", "gamma")
	projectsRecent.Touch("gamma")
	editorRecent.Touch("beta")

	code, location, cache := editorEntry(t, r)
	if code != http.StatusSeeOther || location != "/projects/beta/editor" {
		t.Fatalf("expected a 303 to beta, got %d %q", code, location)
	}
	if cache != "no-store" {
		t.Fatalf("expected no-store, got %q", cache)
	}
}

// A remembered project that is gone falls through to the next remembered one.
func TestEditorEntryFallsToTheNextRememberedProject(t *testing.T) {
	r, editorRecent, _ := editorEntryServer(t, "alpha", "beta")
	editorRecent.Touch("beta")
	editorRecent.Touch("deleted")

	code, location, _ := editorEntry(t, r)
	if code != http.StatusSeeOther || location != "/projects/beta/editor" {
		t.Fatalf("expected a 303 to beta, got %d %q", code, location)
	}
}

// With nothing remembered that still exists, the project used last anywhere in
// the cockpit stands in.
func TestEditorEntryFallsToTheRecentProjects(t *testing.T) {
	r, editorRecent, projectsRecent := editorEntryServer(t, "alpha", "beta")
	editorRecent.Touch("deleted")
	projectsRecent.Touch("beta")

	code, location, _ := editorEntry(t, r)
	if code != http.StatusSeeOther || location != "/projects/beta/editor" {
		t.Fatalf("expected a 303 to beta, got %d %q", code, location)
	}
}

// Nothing used at all opens the first project of the list.
func TestEditorEntryFallsToTheFirstProject(t *testing.T) {
	r, _, _ := editorEntryServer(t, "alpha", "beta")

	code, location, _ := editorEntry(t, r)
	if code != http.StatusSeeOther || location != "/projects/alpha/editor" {
		t.Fatalf("expected a 303 to alpha, got %d %q", code, location)
	}
}

// Without a single project the entry leads to the projects page, which says
// one has to be created.
func TestEditorEntryWithoutAnyProject(t *testing.T) {
	r, editorRecent, _ := editorEntryServer(t)
	editorRecent.Touch("deleted")

	code, location, _ := editorEntry(t, r)
	if code != http.StatusSeeOther || location != "/projects" {
		t.Fatalf("expected a 303 to the projects page, got %d %q", code, location)
	}
}
