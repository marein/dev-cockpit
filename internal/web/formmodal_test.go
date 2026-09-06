package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder"
	codercopilot "github.com/marein/dev-cockpit/internal/coder/copilot"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// refuseCreate posts a create the server has to turn down, from the dialog: the
// marker rides the query, exactly as the form's action carries it.
func refuseCreate(t *testing.T, handler gin.HandlerFunc, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request = req
	handler(c)
	return rec
}

// A refusal inside the create dialog comes back as the message and nothing
// else. A redirect would take the dialog down and throw away everything that
// was typed into it, which is the whole reason the marker exists. The marker
// alone decides it, no Accept header involved.
func TestCreateInTheDialogAnswersTheRefusalInsteadOfRedirecting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	projects := project.NewRepository(root, recent.New(filepath.Join(t.TempDir(), "recent.json")))
	s := &Server{
		coders:   []*coder.Manager{coder.NewManager(config.Config{}, tmux.New(), codercopilot.New(), projects)},
		projects: projects,
	}
	missing := filepath.Join(root, "missing")

	cases := map[string]struct {
		handler gin.HandlerFunc
		path    string
		form    url.Values
	}{
		"coder": {s.handleCoderCreate, "/coders/new?modal=1", url.Values{"name": {"probe"}, "project": {missing}}},
		"shell": {s.handleShellCreate, "/shells/new?modal=1", url.Values{"project": {missing}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := refuseCreate(t, tc.handler, tc.path, tc.form)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want the create refused, got %d: %s", rec.Code, rec.Body.String())
			}
			if location := rec.Header().Get("Location"); location != "" {
				t.Fatalf("the dialog must not be sent anywhere, got a redirect to %q", location)
			}
			var answer struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
				t.Fatalf("the dialog needs a message it can show, got %q: %v", rec.Body.String(), err)
			}
			if strings.TrimSpace(answer.Error) == "" {
				t.Fatalf("the refusal carries no message: %q", rec.Body.String())
			}
		})
	}
}
