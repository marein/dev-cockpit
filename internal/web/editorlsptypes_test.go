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
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/settings"
)

// lspTypesServer builds a server over a Go project and a project of notes,
// every language pinned to the Docker way and no intelligence service, which
// answers like a server that is not there.
func lspTypesServer(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	projectsRoot, stateDir := t.TempDir(), t.TempDir()
	for path, content := range map[string]string{
		"gosrc/go.mod":     "module example.com/x\n",
		"gosrc/main.go":    "package main\n",
		"notes/readme.txt": "Nothing a language server reads.\n",
	} {
		if err := os.MkdirAll(filepath.Join(projectsRoot, filepath.Dir(path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectsRoot, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{
		cfg:      config.Config{StateDir: stateDir},
		projects: project.NewRepository(projectsRoot, nil),
		docker:   docker.NewService(stateDir, func() string { return "" }),
		settings: settings.New(filepath.Join(stateDir, "settings.json")),
	}
	for id, server := range map[string]string{"go": "gopls", "php": "intelephense", "typescript": "tsgo", "python": "pyright"} {
		s.settings.Set(editorLSPServerKey(id), server+"-docker")
	}
	r := gin.New()
	r.GET("/projects/:name/editor/lsp/types", s.handleEditorLSPTypes)
	return s, r
}

func getTypes(t *testing.T, r *gin.Engine, project, query string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/"+project+"/editor/lsp/types?"+query, nil)
	req.Header.Set("Accept", "application/json")
	r.ServeHTTP(rec, req)
	answer := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &answer)
	return rec.Code, answer
}

func TestEditorLSPTypesRefusesWhatIsNoSearch(t *testing.T) {
	_, r := lspTypesServer(t)
	for name, query := range map[string]string{
		"no client":               "q=Widget",
		"a client past the limit": "client=" + strings.Repeat("c", maxLSPClientIDLength+1) + "&q=Widget",
		"a query past the limit":  "client=c&q=" + url.QueryEscape(strings.Repeat("W", maxTypeQueryLength+1)),
	} {
		if code, answer := getTypes(t, r, "gosrc", query); code != http.StatusBadRequest || answer["error"] == nil {
			t.Fatalf("%s: %d %v", name, code, answer)
		}
	}
	if code, _ := getTypes(t, r, "gone", "client=c&q=Widget"); code != http.StatusNotFound {
		t.Fatalf("an unknown project: %d", code)
	}
}

// A project whose languages no server lists types of says so, a Go project
// asks its server and words a missing one as a status, and an empty query
// asks nobody.
func TestEditorLSPTypesSaysWhichProjectsAServerCovers(t *testing.T) {
	s, r := lspTypesServer(t)
	code, answer := getTypes(t, r, "notes", "client=c&q=Widget")
	if code != http.StatusOK || answer["covered"] != false {
		t.Fatalf("notes: %d %v", code, answer)
	}

	code, answer = getTypes(t, r, "gosrc", "client=c&q=Widget")
	statuses, _ := answer["statuses"].([]any)
	if code != http.StatusOK || answer["covered"] != true || len(statuses) != 1 {
		t.Fatalf("go: %d %v", code, answer)
	}
	if status := statuses[0].(map[string]any); status["label"] != "Go" || status["status"] != "unavailable" {
		t.Fatalf("go status: %v", status)
	}

	code, answer = getTypes(t, r, "gosrc", "client=c&q=+")
	if types, _ := answer["types"].([]any); code != http.StatusOK || answer["covered"] != true || types == nil || answer["statuses"] != nil {
		t.Fatalf("empty query: %d %v", code, answer)
	}

	s.settings.Set(editorLSPServerKey("go"), "off")
	if _, answer = getTypes(t, r, "gosrc", "client=c&q=Widget"); answer["covered"] != false {
		t.Fatalf("go switched off: %v", answer)
	}
}
