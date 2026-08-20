package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/docker"
	"github.com/local/dev-cockpit/internal/project"
)

// dockerRunRouter is the three routes of one compose run, on a server whose
// projects root holds one project and whose run register knows no run at all,
// which is exactly what a notification of a deleted project points at.
func dockerRunRouter(t *testing.T) *gin.Engine {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		projects: project.NewRepository(root, nil),
		docker:   docker.NewService(t.TempDir(), func() string { return "" }),
	}
	r := gin.New()
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.GET("/projects/:name/docker/runs/:id", s.handleDockerRun)
	r.GET("/projects/:name/docker/runs/:id/output", s.handleDockerRunOutput)
	r.POST("/projects/:name/docker/runs/:id/stop", s.handleDockerRunStop)
	return r
}

// The run page is what a notification links at, and the notification of a
// deleted project outlives its run. A page route answering JSON hands pe.js a
// body with no page in it, which ends as the literal word null on screen, so
// the refusal is the house pattern: redirect to the projects page with a
// flash.
func TestTheRunPageOfADeadProjectRedirectsWithAFlash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := dockerRunRouter(t)

	for _, path := range []string{
		"/projects/ghost/docker/runs/r1",
		"/projects/app/docker/runs/r1",
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s answered %d, want a redirect", path, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/projects" {
			t.Fatalf("%s redirects to %q", path, loc)
		}
	}
}

// The fetch endpoints under the page stay JSON: their caller is the page's
// own script, and a redirect there would hand it the projects page as data.
func TestTheRunFetchEndpointsStayJSONOnADeadProject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := dockerRunRouter(t)

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/projects/ghost/docker/runs/r1/output", nil),
		httptest.NewRequest(http.MethodPost, "/projects/ghost/docker/runs/r1/stop", nil),
		httptest.NewRequest(http.MethodGet, "/projects/app/docker/runs/r1/output", nil),
		httptest.NewRequest(http.MethodPost, "/projects/app/docker/runs/r1/stop", nil),
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s answered %d, want 404", req.Method, req.URL.Path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("%s %s answered content type %q, want JSON", req.Method, req.URL.Path, ct)
		}
	}
}
