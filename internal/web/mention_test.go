package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/settings"
)

// mentionServer builds a server over a throwaway projects root holding the
// given files, each path led by its project's directory.
func mentionServer(t *testing.T, files []string) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	for _, rel := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		projects:  project.NewRepository(root, nil),
		settings:  settings.New(filepath.Join(t.TempDir(), "settings.json")),
		quickOpen: filesystem.NewQuickOpenCache(),
	}
	r := gin.New()
	r.GET("/mention/files", s.handleMentionFiles)
	return r, resolved
}

type mentionAnswer struct {
	Folder    string        `json:"folder"`
	Query     string        `json:"query"`
	Ref       string        `json:"ref"`
	Files     []mentionFile `json:"files"`
	Total     int           `json:"total"`
	Under     int           `json:"under"`
	Truncated bool          `json:"truncated"`
	Error     string        `json:"error"`
}

func mentionGet(t *testing.T, r *gin.Engine, project, query string) (int, mentionAnswer) {
	t.Helper()
	rec := httptest.NewRecorder()
	target := "/mention/files?project=" + url.QueryEscape(project) + "&q=" + url.QueryEscape(query)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	var got mentionAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v (%s)", target, err, rec.Body.String())
	}
	return rec.Code, got
}

// The file step answers what the editor's quick open answers for the same
// query, ranked the same, and names every hit by its absolute path under the
// project, the folder itself with its trailing slash. What the walk leaves
// out, .git by default, stays out here too.
func TestMentionFilesAnswersTheQuickOpenRankingWithAbsolutePaths(t *testing.T) {
	r, root := mentionServer(t, []string{
		"demo/internal/web/router.go",
		"demo/internal/web/router_test.go",
		"demo/docs/routing.md",
		"demo/.git/router.go",
		"other/router.go",
	})
	code, got := mentionGet(t, r, "demo", "router")
	if code != http.StatusOK {
		t.Fatalf("GET = %d: %+v", code, got)
	}
	want := []mentionFile{
		{Path: "internal/web/router.go", Ref: filepath.Join(root, "demo", "internal", "web", "router.go")},
		{Path: "internal/web/router_test.go", Ref: filepath.Join(root, "demo", "internal", "web", "router_test.go")},
	}
	if fmt.Sprint(got.Files) != fmt.Sprint(want) {
		t.Fatalf("files %+v, want %+v", got.Files, want)
	}
	if got.Folder != "" || got.Ref != filepath.Join(root, "demo")+"/" || got.Total != 2 || got.Truncated || got.Under != 3 {
		t.Fatalf("answer %+v", got)
	}
}

// Folders stand among the files, ranked by the same rule and named by their
// absolute path with a trailing slash, quoted like a file when they hold a
// blank. A typed path that names a folder walks into it: an empty rest lists
// what stands in it, folders first, and a rest is matched below it.
func TestMentionFilesFindsFoldersAndWalksIntoThem(t *testing.T) {
	r, root := mentionServer(t, []string{
		"demo/internal/web/router.go",
		"demo/internal/web/static/app.js",
		"demo/internal/webhook/send.go",
		"demo/my docs/web notes.md",
		"demo/go.mod",
		"demo/.git/web/x",
	})
	dir := func(rel string) string { return filepath.Join(root, "demo", filepath.FromSlash(rel)) + "/" }
	file := func(rel string) string { return filepath.Join(root, "demo", filepath.FromSlash(rel)) }
	_, got := mentionGet(t, r, "demo", "web")
	want := []mentionFile{
		{Path: "internal/web", Ref: dir("internal/web"), Folder: true},
		{Path: "internal/webhook", Ref: dir("internal/webhook"), Folder: true},
		{Path: "my docs/web notes.md", Ref: strconv.Quote(file("my docs/web notes.md"))},
		{Path: "internal/web/static", Ref: dir("internal/web/static"), Folder: true},
		{Path: "internal/web/router.go", Ref: file("internal/web/router.go")},
		{Path: "internal/webhook/send.go", Ref: file("internal/webhook/send.go")},
		{Path: "internal/web/static/app.js", Ref: file("internal/web/static/app.js")},
	}
	if fmt.Sprint(got.Files) != fmt.Sprint(want) || got.Total != len(want) {
		t.Fatalf("web answered %+v, want %+v", got.Files, want)
	}
	_, got = mentionGet(t, r, "demo", "")
	want = []mentionFile{
		{Path: "internal", Ref: dir("internal"), Folder: true},
		{Path: "my docs", Ref: strconv.Quote(dir("my docs")), Folder: true},
		{Path: "go.mod", Ref: file("go.mod")},
	}
	if fmt.Sprint(got.Files) != fmt.Sprint(want) || got.Under != 5 {
		t.Fatalf("the root lists %+v, want %+v", got.Files, want)
	}
	code, got := mentionGet(t, r, "demo", "internal/web/")
	want = []mentionFile{
		{Path: "internal/web/static", Ref: dir("internal/web/static"), Folder: true},
		{Path: "internal/web/router.go", Ref: file("internal/web/router.go")},
	}
	if code != http.StatusOK || got.Folder != "internal/web" || got.Query != "" || got.Ref != dir("internal/web") || got.Under != 2 || fmt.Sprint(got.Files) != fmt.Sprint(want) {
		t.Fatalf("internal/web/ answered %d %+v", code, got)
	}
	_, got = mentionGet(t, r, "demo", "internal/web/app")
	if got.Folder != "internal/web" || got.Query != "app" || len(got.Files) != 1 || got.Files[0].Path != "internal/web/static/app.js" {
		t.Fatalf("app below internal/web answered %+v", got)
	}
	_, got = mentionGet(t, r, "demo", "my docs/")
	if got.Folder != "my docs" || got.Ref != strconv.Quote(dir("my docs")) || len(got.Files) != 1 {
		t.Fatalf("a folder with a blank answered %+v", got)
	}
	for _, typed := range []string{".git/", "../demo/", "/internal/"} {
		if code, got := mentionGet(t, r, "demo", typed); code != http.StatusOK || got.Folder != "" {
			t.Fatalf("%q walked into %d %+v", typed, code, got)
		}
	}
}

// A folder the index still holds and the disk no longer does answers that it
// is gone, not a listing of nothing.
func TestMentionFilesSaysWhenTheFolderIsGone(t *testing.T) {
	r, root := mentionServer(t, []string{"demo/src/main.go", "demo/README.md"})
	if _, got := mentionGet(t, r, "demo", "src/"); got.Folder != "src" {
		t.Fatalf("src/ answered %+v", got)
	}
	if err := os.RemoveAll(filepath.Join(root, "demo", "src")); err != nil {
		t.Fatal(err)
	}
	if code, got := mentionGet(t, r, "demo", "src/"); code != http.StatusNotFound || got.Error != "The folder is gone." {
		t.Fatalf("a gone folder answered %d %+v", code, got)
	}
}

// One answer carries at most the popup's share of the hits and says how many
// there were, so the count in the head can tell the rest is there.
func TestMentionFilesIsBounded(t *testing.T) {
	var files []string
	for i := 0; i < mentionFileLimit+30; i++ {
		files = append(files, "demo/src/file"+strconv.Itoa(i)+".go")
	}
	r, _ := mentionServer(t, files)
	_, got := mentionGet(t, r, "demo", "file")
	if len(got.Files) != mentionFileLimit || got.Total != mentionFileLimit+30 || !got.Truncated {
		t.Fatalf("got %d files of %d, truncated %v", len(got.Files), got.Total, got.Truncated)
	}
}

// Every input is checked before anything is read: a project that is no
// project of the cockpit, a name that walks out of the root or one longer
// than any directory name answers not found, and a query past the bound is
// refused, each with a message that quotes nothing of the request.
func TestMentionFilesRefusesWhatNamesNoProject(t *testing.T) {
	r, _ := mentionServer(t, []string{"demo/main.go", "outside.txt"})
	for _, name := range []string{"", "nope", "..", "../demo", "demo/..", "demo/main.go", strings.Repeat("a", maxProjectName+1)} {
		code, got := mentionGet(t, r, name, "")
		if code != http.StatusNotFound || got.Error != "Project not found." {
			t.Fatalf("project %q answered %d %+v", name, code, got)
		}
	}
	code, got := mentionGet(t, r, "demo", strings.Repeat("q", maxMentionQuery+1))
	if code != http.StatusBadRequest || got.Error != "The search is too long." {
		t.Fatalf("a long query answered %d %+v", code, got)
	}
	if code, _ := mentionGet(t, r, "demo", strings.Repeat("q", maxMentionQuery)); code != http.StatusOK {
		t.Fatalf("a query at the bound answered %d", code)
	}
}

// A path with a blank in it is quoted, so the reader of the message sees
// where it ends; a plain one stands as it is.
func TestMentionRefQuotesWhatHasNoClearEnd(t *testing.T) {
	r, root := mentionServer(t, []string{"my app/read me.md"})
	_, got := mentionGet(t, r, "my app", "read")
	want := strconv.Quote(filepath.Join(root, "my app", "read me.md"))
	if len(got.Files) != 1 || got.Files[0].Ref != want || got.Ref != strconv.Quote(filepath.Join(root, "my app")+"/") {
		t.Fatalf("got %+v, want the ref %s", got, want)
	}
	for in, want := range map[string]string{
		"/p/main.go":   "/p/main.go",
		"/p/a b.go":    `"/p/a b.go"`,
		`/p/say"hi.go`: `"/p/say\"hi.go"`,
		"/p/it's.go":   `"/p/it's.go"`,
	} {
		if got := mentionRef(in); got != want {
			t.Fatalf("mentionRef(%q) = %q, want %q", in, got, want)
		}
	}
}
