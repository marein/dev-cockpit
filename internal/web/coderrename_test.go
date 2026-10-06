package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// Only a running coder has a CLI that can take the command. An inactive one is
// refused with a sentence of its own, a session nobody knows as gone.
func TestCoderRenameRefusesAnInactiveAndAnUnknownCoder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("PATH", t.TempDir())
	const inactive = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	projects := project.NewRepository(t.TempDir(), recent.New(filepath.Join(t.TempDir(), "recent.json")))
	co := storedCoder{sessions: &storedSessions{list: []coder.Session{{SessionID: inactive, Name: "old"}}}}
	s := &Server{
		coders:   []*coder.Manager{coder.NewManager(config.Config{}, tmux.New(), co, projects)},
		projects: projects,
	}

	for id, want := range map[string]int{inactive: http.StatusConflict, "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee": http.StatusGone} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/coders/"+id+"/rename", strings.NewReader(url.Values{"name": {"new"}}.Encode()))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		c.Params = gin.Params{{Key: "id", Value: id}}
		s.handleCoderRename(c)
		if rec.Code != want {
			t.Fatalf("%s: want %d, got %d: %s", id, want, rec.Code, rec.Body.String())
		}
	}
}
