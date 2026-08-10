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
	"github.com/local/dev-cockpit/internal/project"
)

// editorFileServer builds a server over a throwaway projects root holding one
// project, with the two routes a buffer lives on: the read that answers the
// version and the save that carries it back.
func editorFileServer(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{projects: project.NewRepository(root, nil)}
	r := gin.New()
	r.GET("/projects/:name/editor/file", s.handleEditorReadFile)
	r.POST("/projects/:name/editor/file", s.handleEditorSaveFile)
	r.POST("/projects/:name/editor/create", s.handleEditorCreateFile)
	return r, projectDir
}

type editorFileAnswer struct {
	Content  string `json:"content"`
	Version  string `json:"version"`
	Conflict string `json:"conflict"`
	Error    string `json:"error"`
}

func readEditorFile(t *testing.T, r *gin.Engine, path string) (int, editorFileAnswer) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/demo/editor/file?path="+url.QueryEscape(path), nil)
	req.Header.Set("Accept", "application/json")
	r.ServeHTTP(rec, req)
	var body editorFileAnswer
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func postEditorForm(t *testing.T, r *gin.Engine, route string, form url.Values) (int, editorFileAnswer) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/demo/editor/"+route, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	r.ServeHTTP(rec, req)
	var body editorFileAnswer
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// saveEditorFile posts one save. An empty version is the create path, which is
// what a form without the field looks like too.
func saveEditorFile(t *testing.T, r *gin.Engine, path, content, version string) (int, editorFileAnswer) {
	t.Helper()
	form := url.Values{"path": {path}, "content": {content}}
	if version != "" {
		form.Set("version", version)
	}
	return postEditorForm(t, r, "file", form)
}

func onDisk(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEditorSaveWritesOnTheVersionTheReadAnswered(t *testing.T) {
	r, dir := editorFileServer(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, read := readEditorFile(t, r, "a.txt")
	if code != http.StatusOK || read.Version == "" {
		t.Fatalf("the read answered %d without a version: %+v", code, read)
	}
	code, saved := saveEditorFile(t, r, "a.txt", "two\n", read.Version)
	if code != http.StatusOK {
		t.Fatalf("a save on the read's version answered %d: %s", code, saved.Error)
	}
	if got := onDisk(t, dir, "a.txt"); got != "two\n" {
		t.Fatalf("the file holds %q", got)
	}
	if saved.Version == "" || saved.Version == read.Version {
		t.Fatalf("the save answered version %q", saved.Version)
	}
	// The second save carries the first one's answer, so saving twice in a row
	// costs no reload and asks nothing.
	code, again := saveEditorFile(t, r, "a.txt", "three\n", saved.Version)
	if code != http.StatusOK {
		t.Fatalf("the second save answered %d: %s", code, again.Error)
	}
	if got := onDisk(t, dir, "a.txt"); got != "three\n" {
		t.Fatalf("the second save left %q", got)
	}
}

func TestEditorSaveWithAnOldVersionRefusesAndLeavesTheFile(t *testing.T) {
	r, dir := editorFileServer(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, read := readEditorFile(t, r, "a.txt")
	// A coder writes the same file while the buffer stands open.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a coder was here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, refused := saveEditorFile(t, r, "a.txt", "my old buffer\n", read.Version)
	if code != http.StatusConflict {
		t.Fatalf("the stale save answered %d: %+v", code, refused)
	}
	if refused.Conflict != "changed" {
		t.Fatalf("the refusal is marked %q", refused.Conflict)
	}
	if refused.Error == "" {
		t.Fatal("the refusal carries no message")
	}
	if got := onDisk(t, dir, "a.txt"); got != "a coder was here\n" {
		t.Fatalf("the refused save touched the file: %q", got)
	}
	// And the way out of that dialog: read the file back, and the version it
	// answers writes.
	_, fresh := readEditorFile(t, r, "a.txt")
	if code, saved := saveEditorFile(t, r, "a.txt", "on top of the coder\n", fresh.Version); code != http.StatusOK {
		t.Fatalf("the save after the reload answered %d: %s", code, saved.Error)
	}
	if got := onDisk(t, dir, "a.txt"); got != "on top of the coder\n" {
		t.Fatalf("the save after the reload left %q", got)
	}
}

func TestEditorSaveOnADeletedFileRefusesAndCreatesNothing(t *testing.T) {
	r, dir := editorFileServer(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, read := readEditorFile(t, r, "a.txt")
	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	code, refused := saveEditorFile(t, r, "a.txt", "my buffer\n", read.Version)
	if code != http.StatusConflict {
		t.Fatalf("the save on a deleted file answered %d: %+v", code, refused)
	}
	// Its own marker: the dialog behind it offers to write the file again, and
	// offering a reload of something that is gone offers nothing.
	if refused.Conflict != "deleted" {
		t.Fatalf("the refusal is marked %q", refused.Conflict)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("the refused save put the deleted file back")
	}
	// Create again is a save without a version, and it answers one.
	code, created := saveEditorFile(t, r, "a.txt", "my buffer\n", "")
	if code != http.StatusOK {
		t.Fatalf("creating it again answered %d: %s", code, created.Error)
	}
	if got := onDisk(t, dir, "a.txt"); got != "my buffer\n" {
		t.Fatalf("the recreated file holds %q", got)
	}
	if created.Version == "" {
		t.Fatal("the recreated file came back without a version")
	}
	if code, next := saveEditorFile(t, r, "a.txt", "and again\n", created.Version); code != http.StatusOK {
		t.Fatalf("the save after the recreate answered %d: %s", code, next.Error)
	}
}

func TestEditorSaveOfAFreshlyCreatedFileWrites(t *testing.T) {
	r, dir := editorFileServer(t)
	if code, created := postEditorForm(t, r, "create", url.Values{"path": {"new.txt"}}); code != http.StatusOK {
		t.Fatalf("the create answered %d: %s", code, created.Error)
	}
	// The editor opens what it created, which is an empty file with a version
	// like any other, and the first save carries it.
	code, read := readEditorFile(t, r, "new.txt")
	if code != http.StatusOK || read.Content != "" || read.Version == "" {
		t.Fatalf("the fresh file read as %d %+v", code, read)
	}
	code, saved := saveEditorFile(t, r, "new.txt", "first line\n", read.Version)
	if code != http.StatusOK {
		t.Fatalf("the first save answered %d: %s", code, saved.Error)
	}
	if got := onDisk(t, dir, "new.txt"); got != "first line\n" {
		t.Fatalf("the first save left %q", got)
	}
	// And a save for a path nothing ever read carries no version at all, which
	// is the create path: it writes.
	code, blind := saveEditorFile(t, r, "sub/blind.txt", "written\n", "")
	if code != http.StatusBadRequest {
		t.Fatalf("a save into a missing folder answered %d: %+v", code, blind)
	}
	code, blind = saveEditorFile(t, r, "blind.txt", "written\n", "")
	if code != http.StatusOK {
		t.Fatalf("a save without a version answered %d: %s", code, blind.Error)
	}
	if got := onDisk(t, dir, "blind.txt"); got != "written\n" {
		t.Fatalf("the versionless save left %q", got)
	}
}
