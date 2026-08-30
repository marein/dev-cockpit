package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/settings"
)

// filtersProject is the shape the checks below count: two folder levels, four
// extensions with different frequencies, two names that carry none, and a
// vendor folder to exclude.
var filtersProject = []string{
	"main.go",
	"README.md",
	".gitignore",
	"Makefile",
	"cmd/dev-cockpit/main.go",
	"docs/notes.md",
	"internal/filesystem/search.go",
	"internal/filesystem/tree.go",
	"internal/web/server.go",
	"internal/web/router.go",
	"internal/web/static/app.js",
	"internal/web/static/style.css",
	"vendor/lib/vendor.go",
	"vendor/lib/notes.md",
}

// filtersServer builds a server over a throwaway projects root holding one
// project of real files, wired the way the router wires the editor reads.
func filtersServer(t *testing.T, files []string) (*gin.Engine, *Server) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	for _, rel := range files {
		full := filepath.Join(projectDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{
		projects: project.NewRepository(root, nil),
		// The handler reads the folder exclusions from here, so the store is
		// part of what makes this a real server and not a stub.
		settings:  settings.New(filepath.Join(t.TempDir(), "settings.json")),
		quickOpen: filesystem.NewQuickOpenCache(),
	}
	r := gin.New()
	r.GET("/projects/:name/editor/filters", s.handleEditorFilters)
	return r, s
}

type filtersResponse struct {
	Folders    []filesystem.FolderCount    `json:"folders"`
	Extensions []filesystem.ExtensionCount `json:"extensions"`
	Files      int                         `json:"files"`
	Error      string                      `json:"error"`
}

func filtersGet(t *testing.T, r *gin.Engine, name string) (int, filtersResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/"+name+"/editor/filters", nil)
	r.ServeHTTP(rec, req)
	var got filtersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, got
}

// folderLines renders the answer's folders as "path=files" so a wrong count and
// a wrong order both read off one comparison.
func folderLines(res filtersResponse) []string {
	out := make([]string, 0, len(res.Folders))
	for _, folder := range res.Folders {
		out = append(out, fmt.Sprintf("%s=%d", folder.Path, folder.Files))
	}
	return out
}

func extensionLines(res filtersResponse) []string {
	out := make([]string, 0, len(res.Extensions))
	for _, ext := range res.Extensions {
		out = append(out, fmt.Sprintf("%s=%d", ext.Pattern, ext.Files))
	}
	return out
}

// TestEditorFiltersAnswersTheFoldersAndExtensions is the route's contract: the
// folders with everything below them counted, shallow first, and the patterns
// the project really has, the most common first.
func TestEditorFiltersAnswersTheFoldersAndExtensions(t *testing.T) {
	r, _ := filtersServer(t, filtersProject)

	code, res := filtersGet(t, r, "demo")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, res.Error)
	}
	if res.Files != len(filtersProject) {
		t.Errorf("files = %d, want %d", res.Files, len(filtersProject))
	}

	// A folder counts every file under it at any depth, because that is what
	// scoping to it would search. The order is shallow first, then alphabetical.
	wantFolders := []string{
		"cmd=1", "docs=1", "internal=6", "vendor=2",
		"cmd/dev-cockpit=1", "internal/filesystem=2", "internal/web=4", "vendor/lib=2",
		"internal/web/static=2",
	}
	if got := folderLines(res); !equalPaths(got, wantFolders) {
		t.Errorf("folders = %v\nwant %v", got, wantFolders)
	}

	// Most common first, and equal counts alphabetically. A name without an
	// extension carries no pattern, ".gitignore" and "Makefile" among them, so
	// they are in the file count and in no row here.
	wantExtensions := []string{"*.go=7", "*.md=3", "*.css=1", "*.js=1"}
	if got := extensionLines(res); !equalPaths(got, wantExtensions) {
		t.Errorf("extensions = %v\nwant %v", got, wantExtensions)
	}
}

// TestEditorFiltersHonoursTheExclusionSetting is the other half of the editor's
// search settings: what is excluded there is not offered here, neither as a
// folder nor through the files it holds, and the counts of the patterns it
// shared with the rest of the project go down with it.
func TestEditorFiltersHonoursTheExclusionSetting(t *testing.T) {
	r, s := filtersServer(t, filtersProject)

	if _, res := filtersGet(t, r, "demo"); !equalPaths(extensionLines(res), []string{"*.go=7", "*.md=3", "*.css=1", "*.js=1"}) {
		t.Fatalf("by default the vendor files count too, got %v", extensionLines(res))
	}

	// What the search settings form's Save writes.
	s.storeExclusions("vendor")
	code, res := filtersGet(t, r, "demo")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, res.Error)
	}
	if res.Files != len(filtersProject)-2 {
		t.Errorf("files = %d, want %d without the two vendored files", res.Files, len(filtersProject)-2)
	}
	for _, folder := range res.Folders {
		if folder.Path == "vendor" || folder.Path == "vendor/lib" {
			t.Errorf("the excluded folder is still offered: %v", folderLines(res))
			break
		}
	}
	wantExtensions := []string{"*.go=6", "*.md=2", "*.css=1", "*.js=1"}
	if got := extensionLines(res); !equalPaths(got, wantExtensions) {
		t.Errorf("extensions = %v\nwant %v (the vendored files must not count)", got, wantExtensions)
	}

	// An emptied list is a real choice: offer everything again.
	s.storeExclusions("")
	if _, res := filtersGet(t, r, "demo"); res.Files != len(filtersProject) {
		t.Errorf("after emptying the list files = %d, want %d", res.Files, len(filtersProject))
	}
}

// TestEditorFiltersUnknownProject keeps the route on the same answer the other
// editor routes give, so the palette's fetch fails the way the rest does.
func TestEditorFiltersUnknownProject(t *testing.T) {
	r, _ := filtersServer(t, filtersProject)

	code, res := filtersGet(t, r, "nosuchproject")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
	if res.Error != "Project not found." {
		t.Errorf("error = %q, want the editor's own message", res.Error)
	}
	if len(res.Folders) != 0 || len(res.Extensions) != 0 {
		t.Errorf("a refusal must carry no choices: %v %v", folderLines(res), extensionLines(res))
	}
}
