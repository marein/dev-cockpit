package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/settings"
)

// lspClassNameServer builds a server with PHP pinned to the Docker way and
// no intelligence service, which answers like a server that is not there.
func lspClassNameServer(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	projectsRoot, stateDir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectsRoot, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		cfg:      config.Config{StateDir: stateDir},
		projects: project.NewRepository(projectsRoot, nil),
		docker:   docker.NewService(stateDir, func() string { return "" }),
		settings: settings.New(filepath.Join(stateDir, "settings.json")),
	}
	s.settings.Set(editorLSPServerKey("php"), "intelephense-docker")
	s.settings.Set(editorLSPServerKey("go"), "off")
	s.settings.Set(editorLSPServerKey("typescript"), "off")
	r := gin.New()
	r.POST("/projects/:name/editor/lsp/class-name", s.handleEditorLSPClassName)
	return s, r
}

func postClassName(t *testing.T, r *gin.Engine, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/demo/editor/lsp/class-name", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	r.ServeHTTP(rec, req)
	answer := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &answer)
	return rec.Code, answer
}

func TestEditorLSPClassNameRefusesWhatIsNoLookup(t *testing.T) {
	_, r := lspClassNameServer(t)
	position := `"position":{"line":0,"character":0}`
	for name, body := range map[string]string{
		"no json":                          `{`,
		"no client":                        `{"path":"a.php","content":"<?php",` + position + `}`,
		"a client past the limit":          `{"client":"` + strings.Repeat("c", maxLSPClientIDLength+1) + `","path":"a.php",` + position + `}`,
		"no path":                          `{"client":"c",` + position + `}`,
		"a path out of the project":        `{"client":"c","path":"../x.php",` + position + `}`,
		"an absolute path no server holds": `{"client":"c","path":"/etc/x.php",` + position + `}`,
	} {
		code, answer := postClassName(t, r, body)
		if code != http.StatusBadRequest || answer["error"] == nil {
			t.Fatalf("%s: %d %v", name, code, answer)
		}
	}
}

func TestEditorLSPClassNameAnswersAStatusWithoutAServer(t *testing.T) {
	s, r := lspClassNameServer(t)
	body := `{"client":"c","path":"src/a.php","content":"<?php\nnew A();","position":{"line":1,"character":5}}`
	code, answer := postClassName(t, r, body)
	if code != http.StatusOK || answer["available"] != false || answer["status"] != "unavailable" {
		t.Fatalf("no service: %d %v", code, answer)
	}

	s.settings.Set(editorLSPServerKey("php"), "off")
	code, answer = postClassName(t, r, body)
	if code != http.StatusOK || answer["available"] != false || answer["status"] != "disabled" {
		t.Fatalf("php switched off: %d %v", code, answer)
	}
}

func TestLSPSpecsNameTheLanguagesWithClassNames(t *testing.T) {
	s, _ := lspClassNameServer(t)
	spec, names := s.lspSpecs()
	if spec != "php:PHP" || names != "php" {
		t.Fatalf("php alone: %q %q", spec, names)
	}

	s.settings.Set(editorLSPServerKey("go"), "gopls-docker")
	s.settings.Set(editorLSPServerKey("php"), "off")
	spec, names = s.lspSpecs()
	if spec != "go:Go" || names != "" {
		t.Fatalf("go alone names no classes: %q %q", spec, names)
	}
}
