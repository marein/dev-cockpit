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
	"github.com/marein/dev-cockpit/internal/project"
)

// split=1 reads the file where HEAD left the revision, not at its tip, and
// names that commit; a revision that is an ancestor is its own split and
// names none. Anything but 1 is refused like a wrong name.
func TestEditorGitFileReadsAtTheSplitOnlyWhenAsked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	dir := worktreeSourceRepo(t, root, "demo")
	split := strings.TrimSpace(gitOutput(t, dir, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a moved on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, dir, "commit", "-qam", "master moves on")
	worktreeGit(t, dir, "switch", "-q", "feature")
	s := &Server{projects: project.NewRepository(root, nil)}
	r := gin.New()
	r.GET("/projects/:name/editor/git/file", s.handleEditorGitFile)
	get := func(query string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/demo/editor/git/file?path=a.txt&"+query, nil))
		answer := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &answer)
		return rec.Code, answer
	}

	code, answer := get("rev=master")
	if code != http.StatusOK || answer["content"] != "a moved on\n" || answer["split"] != nil {
		t.Fatalf("without split the tip answers: %d %v", code, answer)
	}
	code, answer = get("rev=master&split=1")
	if code != http.StatusOK || answer["content"] != "a\n" || answer["split"] != split {
		t.Fatalf("with split the merge base answers and is named: %d %v", code, answer)
	}
	code, answer = get("rev=feature&split=1")
	if code != http.StatusOK || answer["content"] != "a\n" || answer["split"] != nil {
		t.Fatalf("HEAD itself is its own split: %d %v", code, answer)
	}
	for _, query := range []string{"rev=master&split=yes", "rev=nowhere&split=1", "split=1"} {
		if code, answer = get(query); code != http.StatusBadRequest || answer["error"] == nil {
			t.Fatalf("%s answered %d %v", query, code, answer)
		}
	}
}
