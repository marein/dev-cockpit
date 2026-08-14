package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/editorintelligence"
	"github.com/local/dev-cockpit/internal/project"
)

// lspSourceServer builds a server over a throwaway projects root and state
// directory, with a module cache holding one dependency the way a language
// server would have downloaded it.
func lspSourceServer(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	projectsRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectsRoot, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	dep := filepath.Join(editorintelligence.CacheRoot(stateDir), "dev-cockpit-gopls-demo", "mod", "example.com", "dep@v1.0.0")
	if err := os.MkdirAll(dep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dep, "dep.go"), []byte("package dep\n\nfunc Do() {}\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: config.Config{StateDir: stateDir}, projects: project.NewRepository(projectsRoot, nil)}
	r := gin.New()
	r.GET("/projects/:name/editor/lsp/source", s.handleEditorLSPSource)
	return r, filepath.Join(dep, "dep.go")
}

func getLSPSource(t *testing.T, r *gin.Engine, path string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/demo/editor/lsp/source?path="+url.QueryEscape(path), nil)
	req.Header.Set("Accept", "application/json")
	r.ServeHTTP(rec, req)
	body := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// The route answers a file inside the project's module cache and refuses
// everything the allowlist does not name, whatever the path looks like.
func TestEditorLSPSourceServesOnlyTheSourceRoots(t *testing.T) {
	r, depFile := lspSourceServer(t)

	code, body := getLSPSource(t, r, depFile)
	if code != http.StatusOK || body["content"] != "package dep\n\nfunc Do() {}\n" {
		t.Fatalf("dependency read: %d %v", code, body)
	}
	if body["readOnly"] != true {
		t.Fatalf("the answer must say it cannot be written back: %v", body)
	}
	// No version travels with it: there is no save path this could ride
	// back on.
	if _, ok := body["version"]; ok {
		t.Fatalf("the answer must carry no version: %v", body)
	}

	cache := filepath.Dir(filepath.Dir(filepath.Dir(depFile)))
	for _, name := range []string{
		"/etc/passwd",
		"",
		// The project's own files have their own route.
		"main.go",
		// The cache directory beside the module downloads, the server's
		// own index, is no source root.
		filepath.Join(filepath.Dir(cache), "cache", "gopls", "x"),
		// Traversal out of a root, spelled out so it arrives unclean and
		// is refused rather than repaired, and the root itself.
		cache + "/../../../../etc/passwd",
		cache + "/mod/../../../../etc/passwd",
		cache,
		// Another project's cache.
		filepath.Join(filepath.Dir(filepath.Dir(cache)), "dev-cockpit-gopls-other", "mod", "x.go"),
	} {
		if code, body := getLSPSource(t, r, name); code != http.StatusBadRequest {
			t.Errorf("%q answered %d %v, want a refusal", name, code, body)
		}
	}
}

// A navigation request carries the file the cursor stands in, and that is
// a path of the project or a source outside it. The same allowlist decides
// both, and it decides before anything reaches a language server.
func TestValidLSPTargetTakesProjectPathsAndSourceRoots(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projectsRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectsRoot, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	s := &Server{cfg: config.Config{StateDir: stateDir}, projects: project.NewRepository(projectsRoot, nil)}
	p := project.Project{Name: "demo", Path: filepath.Join(projectsRoot, "demo")}
	cache := filepath.Join(editorintelligence.CacheRoot(stateDir), "dev-cockpit-gopls-demo")

	cases := []struct {
		name   string
		path   string
		client string
		want   bool
	}{
		{"a file of the project", "internal/app/main.go", "c1", true},
		{"a dependency's sources", cache + "/mod/example.com/dep@v1.0.0/dep.go", "c1", true},
		{"the standard library", "/usr/local/go/src/strings/strings.go", "c1", true},
		{"a stub", "/usr/local/lib/node_modules/intelephense/lib/stub/standard/standard_1.php", "c1", true},
		{"a file of this machine", "/etc/passwd", "c1", false},
		{"the server's own file cache beside the sources", cache + "/cache/gopls/x.go", "c1", false},
		{"another project's cache", filepath.Join(editorintelligence.CacheRoot(stateDir), "dev-cockpit-gopls-other", "mod", "x.go"), "c1", false},
		{"a traversal out of a root", cache + "/mod/../../../../etc/passwd", "c1", false},
		{"a path that walks out of the project", "../../etc/passwd", "c1", false},
		{"no path", "", "c1", false},
		{"no client", "main.go", "", false},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/projects/demo/editor/lsp/definition", nil)
		if got := s.validLSPTarget(c, p, tc.client, tc.path); got != tc.want {
			t.Errorf("%s: validLSPTarget(%q) = %v, want %v", tc.name, tc.path, got, tc.want)
		} else if !tc.want && rec.Code != http.StatusBadRequest {
			t.Errorf("%s: refused with %d, want 400", tc.name, rec.Code)
		}
	}
}

// The refusal is the route's answer too, and it happens before a language
// server is asked anything: this server has no intelligence service at all,
// so reaching one would panic rather than answer.
func TestEditorLSPNavigateRefusesAPathOutsideTheAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projectsRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectsRoot, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: config.Config{StateDir: t.TempDir()}, projects: project.NewRepository(projectsRoot, nil)}
	r := gin.New()
	r.POST("/projects/:name/editor/lsp/definition", s.handleEditorLSPDefinition)
	r.POST("/projects/:name/editor/lsp/references", s.handleEditorLSPReferences)
	r.POST("/projects/:name/editor/lsp/close", s.handleEditorLSPClose)

	for _, route := range []string{"definition", "references", "close"} {
		body := `{"client":"c1","path":"/etc/passwd","content":"x","position":{"line":0,"character":0}}`
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/projects/demo/editor/lsp/"+route, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d, want 400: %s", route, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "outside the language servers") {
			t.Errorf("%s: refusal says %q", route, rec.Body.String())
		}
	}
}

// A file that is in a root but not on the disk is a plain miss, not a way
// to learn anything about the machine.
func TestEditorLSPSourceMissingFile(t *testing.T) {
	r, depFile := lspSourceServer(t)
	code, body := getLSPSource(t, r, filepath.Join(filepath.Dir(depFile), "gone.go"))
	if code != http.StatusBadRequest || body["error"] == "" {
		t.Fatalf("missing file: %d %v", code, body)
	}
}
