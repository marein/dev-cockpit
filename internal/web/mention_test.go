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
	Project   string        `json:"project"`
	Root      string        `json:"root"`
	Files     []mentionFile `json:"files"`
	Total     int           `json:"total"`
	Indexed   int           `json:"indexed"`
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
// project, the folder itself included. What the walk leaves out, .git by
// default, stays out here too.
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
	if got.Project != "demo" || got.Root != filepath.Join(root, "demo") || got.Total != 2 || got.Truncated || got.Indexed != 3 {
		t.Fatalf("answer %+v", got)
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
	if len(got.Files) != 1 || got.Files[0].Ref != want || got.Root != strconv.Quote(filepath.Join(root, "my app")) {
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
