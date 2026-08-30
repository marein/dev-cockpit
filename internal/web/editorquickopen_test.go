package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/settings"
)

// quickOpenServer builds a server over a throwaway projects root holding one
// project, wired the way the real router wires the editor writes.
func quickOpenServer(t *testing.T, files []string) (*gin.Engine, *Server, string) {
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
	editor := r.Group("/projects/:name/editor", s.invalidateQuickOpenAfterWrite)
	editor.GET("/files", s.handleEditorFiles)
	// A stand-in for any write route: it only has to answer 2xx so the
	// invalidating middleware fires.
	editor.POST("/create", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r, s, projectDir
}

type quickOpenResponse struct {
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated"`
	Total     int      `json:"total"`
	Indexed   int      `json:"indexed"`
}

func quickOpenGet(t *testing.T, r *gin.Engine, query string) quickOpenResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/demo/editor/files?q="+query, nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET files (q=%q) = %d: %s", query, rec.Code, rec.Body.String())
	}
	var got quickOpenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return got
}

// TestQuickOpenReachesPastTheOldCap is the regression test for the bug this
// endpoint was rewritten to fix. The old handler shipped the first
// 5000 paths in lexical order and let the browser filter them, so a
// file sorting after that cap could not be opened from the palette at all.
func TestQuickOpenReachesPastTheOldCap(t *testing.T) {
	// Enough files before the target, lexically, to bury it well past the old
	// cap: "aaa..." sorts before "src...".
	const oldCap = 5000 // what the endpoint used to stop at
	files := make([]string, 0, oldCap+2)
	for i := 0; i < oldCap+1; i++ {
		files = append(files, "aaa/pad"+strconv.Itoa(i)+".php")
	}
	target := "src/ConnectFour/Domain/Game/GameId.php"
	files = append(files, target)

	r, _, _ := quickOpenServer(t, files)

	got := quickOpenGet(t, r, "GameId")
	if len(got.Files) != 1 || got.Files[0] != target {
		t.Fatalf("files = %v, want exactly %q", got.Files, target)
	}
	if got.Indexed != len(files) {
		t.Errorf("indexed = %d, want %d (the whole tree must be indexed)", got.Indexed, len(files))
	}
}

// TestQuickOpenCapsTheResponse checks that a query matching most of the tree
// still answers with only what the palette renders, and reports the rest as a
// count rather than as paths.
func TestQuickOpenCapsTheResponse(t *testing.T) {
	files := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		files = append(files, "src/file"+strconv.Itoa(i)+".php")
	}
	r, _, _ := quickOpenServer(t, files)

	got := quickOpenGet(t, r, "php")
	if len(got.Files) != filesystem.QuickOpenLimit {
		t.Errorf("files = %d, want %d", len(got.Files), filesystem.QuickOpenLimit)
	}
	if got.Total != 500 {
		t.Errorf("total = %d, want 500", got.Total)
	}
	if !got.Truncated {
		t.Error("truncated should be set when there are more matches than shown")
	}
}

func TestQuickOpenEmptyAndMissingQueries(t *testing.T) {
	r, _, _ := quickOpenServer(t, []string{"a.php", "b.php", "c.php"})

	// Opening the palette sends no query and must still list something.
	if got := quickOpenGet(t, r, ""); len(got.Files) != 3 {
		t.Errorf("empty query files = %v, want 3", got.Files)
	}
	// A query nothing matches is an empty list, not an error.
	got := quickOpenGet(t, r, "nosuchthing")
	if len(got.Files) != 0 {
		t.Errorf("no-hit files = %v, want none", got.Files)
	}
	if got.Truncated {
		t.Error("no-hit answer must not claim truncation")
	}
}

// TestQuickOpenWriteInvalidatesTheIndex covers the case that would otherwise be
// the most visible staleness: create a file, then look for it straight away.
func TestQuickOpenWriteInvalidatesTheIndex(t *testing.T) {
	r, _, projectDir := quickOpenServer(t, []string{"src/existing.php"})

	// Warm the index.
	if got := quickOpenGet(t, r, "existing"); len(got.Files) != 1 {
		t.Fatalf("warmup files = %v", got.Files)
	}

	// Write behind the index, the way a create handler would.
	if err := os.WriteFile(filepath.Join(projectDir, "src", "fresh.php"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// This GET is also the check that reads do not invalidate: if it dropped
	// the index, the rebuild would pick the new file up and this would fail.
	if got := quickOpenGet(t, r, "fresh"); len(got.Files) != 0 {
		t.Fatalf("index should still be warm and unaware, got %v", got.Files)
	}

	// Any successful write below /editor must drop the index on its way out.
	rec := httptest.NewRecorder()
	rec2 := httptest.NewRequest(http.MethodPost, "/projects/demo/editor/create", nil)
	r.ServeHTTP(rec, rec2)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d", rec.Code)
	}

	if got := quickOpenGet(t, r, "fresh"); len(got.Files) != 1 || got.Files[0] != "src/fresh.php" {
		t.Errorf("after a write the new file must be findable, got %v", got.Files)
	}
}

// TestQuickOpenHonoursTheExclusionSetting is the endpoint's half of the setting:
// what the search settings tab stores has to reach the palette, and changing it
// has to take effect without restarting anything.
func TestQuickOpenHonoursTheExclusionSetting(t *testing.T) {
	r, s, _ := quickOpenServer(t, []string{"src/app.php", "vendor/lib/app.php"})

	if got := quickOpenGet(t, r, "app"); len(got.Files) != 2 {
		t.Fatalf("by default both files are found, got %v", got.Files)
	}

	// What the form's Save writes.
	s.storeExclusions("vendor")
	got := quickOpenGet(t, r, "app")
	if len(got.Files) != 1 || got.Files[0] != "src/app.php" {
		t.Errorf("after excluding vendor got %v, want only src/app.php", got.Files)
	}

	// An emptied list is a real choice: search everything again.
	s.storeExclusions("")
	if got := quickOpenGet(t, r, "app"); len(got.Files) != 2 {
		t.Errorf("after emptying the list got %v, want both files", got.Files)
	}
}

// TestExclusionsDefaultUntilSaved keeps the two states apart that Get alone
// cannot: never configured falls back to the old skip list, saved-as-empty means
// exclude nothing.
func TestExclusionsDefaultUntilSaved(t *testing.T) {
	_, s, _ := quickOpenServer(t, []string{"src/app.php"})

	if got := s.exclusions().List(); len(got) != len(filesystem.DefaultExclusions) {
		t.Errorf("untouched install = %v, want the defaults %v", got, filesystem.DefaultExclusions)
	}
	s.storeExclusions("")
	if got := s.exclusions().Len(); got != 0 {
		t.Errorf("after saving an empty list Len() = %d, want 0", got)
	}
}

// quickOpenGetScoped is quickOpenGet with the folder the palette now sends.
func quickOpenGetScoped(t *testing.T, r *gin.Engine, query, scope string) quickOpenResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/demo/editor/files?q="+query+"&path="+url.QueryEscape(scope), nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET files (q=%q, path=%q) = %d: %s", query, scope, rec.Code, rec.Body.String())
	}
	var got quickOpenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return got
}

// TestQuickOpenTakesAFolderScope is the endpoint's half of the palette's folder
// field: the answer holds only what lives under the folder, and the paths stay
// relative to the project root. The scope filters the index rather than
// resolving a path on disk, so a folder that climbs out of the project or is
// not there simply matches nothing.
func TestQuickOpenTakesAFolderScope(t *testing.T) {
	r, _, _ := quickOpenServer(t, []string{"app.php", "src/app.php", "src/deep/app.php", "other/app.php"})

	got := quickOpenGetScoped(t, r, "app", "src")
	want := []string{"src/app.php", "src/deep/app.php"}
	if len(got.Files) != len(want) {
		t.Fatalf("files = %v, want %v", got.Files, want)
	}
	for i, path := range want {
		if got.Files[i] != path {
			t.Fatalf("files = %v, want %v", got.Files, want)
		}
	}

	// Slashes around the folder are trimmed, so what the tree hands over and
	// what somebody types both work.
	if got := quickOpenGetScoped(t, r, "app", "/src/"); len(got.Files) != 2 {
		t.Errorf("files = %v, want the two under src", got.Files)
	}

	// An empty query in a scope lists that folder rather than the project.
	if got := quickOpenGetScoped(t, r, "", "other"); len(got.Files) != 1 || got.Files[0] != "other/app.php" {
		t.Errorf("empty query in a scope = %v, want only other/app.php", got.Files)
	}

	for _, scope := range []string{"..", "../..", "src/../../demo", "nosuchfolder"} {
		if got := quickOpenGetScoped(t, r, "app", scope); len(got.Files) != 0 {
			t.Errorf("scope %q = %v, want nothing", scope, got.Files)
		}
	}

	// Without the folder the whole project is listed, as before.
	if got := quickOpenGet(t, r, "app"); len(got.Files) != 4 {
		t.Errorf("unscoped = %v, want all four", got.Files)
	}
}
