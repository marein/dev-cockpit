package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/project"
)

// The working copy is its own parameter. A typed name never becomes it, a
// value other than the one the panel sends is refused, and so is asking for a
// revision and the working copy at once.
func TestEditorGitCompareTakesTheWorkingCopyOnlyByItsOwnParameter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	dir := worktreeSourceRepo(t, root, "demo")
	if err := os.WriteFile(filepath.Join(dir, "loose.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{projects: project.NewRepository(root, nil)}
	r := gin.New()
	r.GET("/projects/:name/editor/git/compare", s.handleEditorGitCompare)
	get := func(query string) (int, []byte) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/demo/editor/git/compare?"+query, nil))
		return rec.Code, rec.Body.Bytes()
	}

	code, body := get("from=master&worktree=1")
	var got git.Comparison
	if err := json.Unmarshal(body, &got); err != nil || code != http.StatusOK {
		t.Fatalf("the working copy answered %d %s", code, body)
	}
	if !got.Worktree || len(got.Files) != 1 || got.Files[0].Path != "loose.txt" || got.Files[0].Status != "?" || got.Files[0].Added != 2 {
		t.Fatalf("the untracked file is the one change: %+v", got)
	}

	for _, query := range []string{"from=master&to=Working+copy", "from=master&worktree=yes", "from=master&worktree=1&to=feature"} {
		code, body = get(query)
		var refused map[string]any
		_ = json.Unmarshal(body, &refused)
		if code != http.StatusBadRequest || refused["side"] != "to" || refused["error"] == nil || refused["error"] == "" {
			t.Fatalf("%s answered %d %s", query, code, body)
		}
	}

	code, body = get("from=master&to=feature")
	var commits git.Comparison
	if err := json.Unmarshal(body, &commits); err != nil || code != http.StatusOK || commits.Worktree || len(commits.Files) != 0 {
		t.Fatalf("two revisions still compare as commits: %d %s", code, body)
	}
}
