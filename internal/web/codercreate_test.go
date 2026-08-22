package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	codercopilot "github.com/marein/dev-cockpit/internal/coder/copilot"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// createCoder posts the create form the way the local API does, JSON accepted.
func createCoder(t *testing.T, s *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/coders/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	c.Request = req
	s.handleCoderCreate(c)
	return rec
}

// A done-when the watcher would refuse is refused before the session
// exists. Refused afterwards it would leave a running coder without its job,
// and whoever repeats the command then has two sessions on the same task. The
// refusal is the watcher's own rule (assistant.ValidateDoneWhen), not a copy
// of it.
func TestCoderCreateChecksTheDoneWhenBeforeTheSessionExists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	projects := project.NewRepository(root, recent.New(filepath.Join(t.TempDir(), "recent.json")))
	s := &Server{
		coders:   []*coder.Manager{coder.NewManager(config.Config{}, tmux.New(), codercopilot.New(), projects)},
		projects: projects,
	}

	// Over the bound by construction, whatever the bound is: the rule itself
	// says when it refuses, so this test does not carry a copy of the number.
	long := strings.Repeat("y", 512)
	for {
		if _, err := assistant.ValidateDoneWhen(long); err != nil {
			break
		}
		long += long
	}
	form := url.Values{
		"name":      {"probe"},
		"project":   {filepath.Join(root, "missing")},
		"done_when": {long},
	}
	rec := createCoder(t, s, form)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want the create refused, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "too long") {
		t.Fatalf("want the watcher's own refusal before anything starts, got %q", rec.Body.String())
	}

	// The counterpart proves the order: a criterion the watcher takes reaches
	// the session start, and only fails there, on the missing project.
	form.Set("done_when", "the tests pass")
	rec = createCoder(t, s, form)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want the start refused on the project, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "too long") {
		t.Fatalf("a valid criterion must reach the session start, got %q", rec.Body.String())
	}
}
