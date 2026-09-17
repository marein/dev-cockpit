package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/project"
)

// What is typed for a new branch reaches git as a name it takes, the same
// rule the worktree create applies, and the answer names the branch as it was
// made so the client can say so. A name that leaves nothing is refused before
// the working copy is even taken.
func TestEditorGitBranchTakesWhatWasTypedAsANameGitAccepts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	dir := worktreeSourceRepo(t, root, "demo")
	s := &Server{
		projects:  project.NewRepository(root, nil),
		gitWrites: newGitWrites(),
		bus:       eventbus.New(),
	}
	r := gin.New()
	r.POST("/projects/:name/editor/git/branch", s.handleEditorGitBranch)
	post := func(body string) (int, map[string]string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/projects/demo/editor/git/branch", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		var answer map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &answer)
		return rec.Code, answer
	}

	code, answer := post(`{"branch":"fix login bug"}`)
	if code != http.StatusOK || answer["branch"] != "fix-login-bug" {
		t.Fatalf("a typed name answered %d %v", code, answer)
	}
	if got := strings.TrimSpace(gitOutput(t, dir, "symbolic-ref", "--short", "HEAD")); got != "fix-login-bug" {
		t.Fatalf("the working copy stands on %q", got)
	}

	code, answer = post(`{"branch":"???"}`)
	if code != http.StatusBadRequest || answer["error"] == "" {
		t.Fatalf("a name with nothing git takes answered %d %v", code, answer)
	}
	gitDir, err := filepath.EvalSymlinks(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.gitWrites.try(gitDir) {
		t.Fatal("the refusal kept the working copy")
	}
}
