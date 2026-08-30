package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/settings"
)

// searchServer builds a server over a throwaway projects root holding one
// project whose files carry the given contents.
func searchServer(t *testing.T, files map[string]string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	for rel, body := range files {
		full := filepath.Join(projectDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{
		projects: project.NewRepository(root, nil),
		settings: settings.New(filepath.Join(t.TempDir(), "settings.json")),
	}
	r := gin.New()
	r.GET("/projects/:name/editor/search", s.handleEditorSearch)
	return r
}

type searchResponse struct {
	Matches   []filesystem.SearchMatch `json:"matches"`
	Truncated bool                     `json:"truncated"`
	Error     string                   `json:"error"`
}

// searchGet runs one request and returns the status next to the decoded body,
// so a check can read both the refusal and the answer.
func searchGet(t *testing.T, r *gin.Engine, query url.Values) (int, searchResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/demo/editor/search?"+query.Encode(), nil)
	r.ServeHTTP(rec, req)
	var got searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode (%s): %v (%s)", query.Encode(), err, rec.Body.String())
	}
	return rec.Code, got
}

func searchPaths(res searchResponse) []string {
	out := make([]string, 0, len(res.Matches))
	for _, m := range res.Matches {
		out = append(out, m.Path)
	}
	return out
}

func searchTree() map[string]string {
	return map[string]string{
		"main.go":            "needle at the top\n",
		"readme.md":          "needle in the readme\n",
		"src/app.go":         "needle in the app\n",
		"src/app.js":         "needle in the script\n",
		"src/deep/other.go":  "needle further down\n",
		"other/elsewhere.go": "needle elsewhere\n",
	}
}

func equalPaths(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestEditorSearchWithoutTheNewParametersIsUnchanged is the compatibility check:
// a request that names neither the mask nor the folder searches the whole
// project, exactly as it did before the two existed.
func TestEditorSearchWithoutTheNewParametersIsUnchanged(t *testing.T) {
	r := searchServer(t, searchTree())

	code, res := searchGet(t, r, url.Values{"q": {"needle"}})
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, res.Error)
	}
	want := []string{"main.go", "other/elsewhere.go", "readme.md", "src/app.go", "src/app.js", "src/deep/other.go"}
	if got := searchPaths(res); !equalPaths(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
}

// TestEditorSearchTakesAFileMask carries the palette's file field through to the
// scan, the comma separated list and the exclusion included.
func TestEditorSearchTakesAFileMask(t *testing.T) {
	r := searchServer(t, searchTree())

	code, res := searchGet(t, r, url.Values{"q": {"needle"}, "file": {"*.go"}})
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, res.Error)
	}
	want := []string{"main.go", "other/elsewhere.go", "src/app.go", "src/deep/other.go"}
	if got := searchPaths(res); !equalPaths(got, want) {
		t.Errorf("mask *.go = %v, want %v", got, want)
	}

	_, res = searchGet(t, r, url.Values{"q": {"needle"}, "file": {" *.go , *.md "}})
	want = []string{"main.go", "other/elsewhere.go", "readme.md", "src/app.go", "src/deep/other.go"}
	if got := searchPaths(res); !equalPaths(got, want) {
		t.Errorf("mask *.go,*.md = %v, want %v", got, want)
	}

	_, res = searchGet(t, r, url.Values{"q": {"needle"}, "file": {"!*.go"}})
	want = []string{"readme.md", "src/app.js"}
	if got := searchPaths(res); !equalPaths(got, want) {
		t.Errorf("mask !*.go = %v, want %v", got, want)
	}
}

// TestEditorSearchTakesAFolderScope narrows the walk to one folder, and the
// paths that come back still name the file from the project root, which is what
// opens it.
func TestEditorSearchTakesAFolderScope(t *testing.T) {
	r := searchServer(t, searchTree())

	code, res := searchGet(t, r, url.Values{"q": {"needle"}, "path": {"src"}})
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, res.Error)
	}
	want := []string{"src/app.go", "src/app.js", "src/deep/other.go"}
	if got := searchPaths(res); !equalPaths(got, want) {
		t.Errorf("scope src = %v, want %v", got, want)
	}

	_, res = searchGet(t, r, url.Values{"q": {"needle"}, "path": {"src"}, "file": {"*.js"}})
	if got := searchPaths(res); !equalPaths(got, []string{"src/app.js"}) {
		t.Errorf("scope plus mask = %v, want only src/app.js", got)
	}

	// An empty folder is the whole project, the same as leaving it out.
	_, res = searchGet(t, r, url.Values{"q": {"needle"}, "path": {""}})
	if got := searchPaths(res); len(got) != 6 {
		t.Errorf("empty scope = %v, want the whole project", got)
	}
}

// TestEditorSearchRefusesAFolderOutsideTheProject is the traversal guard: the
// folder is client supplied, so a path that climbs out of the project, one that
// is not there and one that is a file are all refused with a message rather
// than searched.
func TestEditorSearchRefusesAFolderOutsideTheProject(t *testing.T) {
	r := searchServer(t, searchTree())

	for _, folder := range []string{"..", "../..", "../demo", "src/../../demo", "/etc", "nosuchfolder", "main.go"} {
		code, res := searchGet(t, r, url.Values{"q": {"needle"}, "path": {folder}})
		if code != http.StatusBadRequest {
			t.Errorf("folder %q = %d, want 400 (%v)", folder, code, searchPaths(res))
			continue
		}
		if res.Error == "" {
			t.Errorf("folder %q was refused without a message", folder)
		}
		if len(res.Matches) != 0 {
			t.Errorf("folder %q answered matches with its refusal: %v", folder, searchPaths(res))
		}
	}
}

// TestEditorSearchRefusesAFolderThatSymlinksOut keeps the second half of the
// guard, the one a lexical check cannot see.
func TestEditorSearchRefusesAFolderThatSymlinksOut(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("needle outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(projectDir, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	gin.SetMode(gin.TestMode)
	s := &Server{
		projects: project.NewRepository(root, nil),
		settings: settings.New(filepath.Join(t.TempDir(), "settings.json")),
	}
	r := gin.New()
	r.GET("/projects/:name/editor/search", s.handleEditorSearch)

	code, res := searchGet(t, r, url.Values{"q": {"needle"}, "path": {"escape"}})
	if code != http.StatusBadRequest {
		t.Fatalf("a symlinked folder = %d, want 400 (%v)", code, searchPaths(res))
	}
}

// TestEditorSearchMindsCaseOnRequest carries the palette's Aa switch to the
// scan, and leaves the answer folded without it.
func TestEditorSearchMindsCaseOnRequest(t *testing.T) {
	r := searchServer(t, map[string]string{"a.go": "GetName\ngetname\n"})

	if _, res := searchGet(t, r, url.Values{"q": {"getname"}}); len(res.Matches) != 2 {
		t.Errorf("without the switch both lines match, got %d", len(res.Matches))
	}
	code, res := searchGet(t, r, url.Values{"q": {"getname"}, "case": {"1"}})
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, res.Error)
	}
	if len(res.Matches) != 1 || res.Matches[0].Text != "getname" {
		t.Errorf("with the switch = %v, want only the line that was typed", searchPaths(res))
	}
}
