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
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/settings"
)

// replaceServer builds a server over a throwaway projects root holding one
// project of real files, wired the way the router wires the editor writes.
func replaceServer(t *testing.T, files map[string]string) (*gin.Engine, string) {
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
		projects:  project.NewRepository(root, nil),
		settings:  settings.New(filepath.Join(t.TempDir(), "settings.json")),
		quickOpen: filesystem.NewQuickOpenCache(),
	}
	r := gin.New()
	r.POST("/projects/:name/editor/replace", s.handleEditorReplace)
	return r, projectDir
}

type replaceResponse struct {
	Matches   []filesystem.ReplaceMatch `json:"matches"`
	Truncated bool                      `json:"truncated"`
	Total     int                       `json:"total"`
	Files     int                       `json:"files"`
	Replaced  int                       `json:"replaced"`
	Changed   []string                  `json:"changed"`
	Blocked   []string                  `json:"blocked"`
	Error     string                    `json:"error"`
}

func replacePost(t *testing.T, r *gin.Engine, name string, form url.Values) (int, replaceResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/"+name+"/editor/replace", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(rec, req)
	var got replaceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, got
}

func fileBody(t *testing.T, dir, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestEditorReplacePreviewCountsTheWholeScope is the promise the button makes:
// the preview is capped, the count behind it is not, and nothing is written.
func TestEditorReplacePreviewCountsTheWholeScope(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 5; i++ {
		files["src/f"+string(rune('a'+i))+".go"] = strings.Repeat("needle here\n", 60)
	}
	r, dir := replaceServer(t, files)

	code, res := replacePost(t, r, "demo", url.Values{"q": {"needle"}, "to": {"pin"}})
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, res.Error)
	}
	if res.Total != 300 || res.Files != 5 {
		t.Errorf("counted %d in %d files, want 300 in 5", res.Total, res.Files)
	}
	if len(res.Matches) != filesystem.MaxSearchMatches || !res.Truncated {
		t.Errorf("preview carries %d rows, truncated = %v", len(res.Matches), res.Truncated)
	}
	row := res.Matches[0]
	if row.Text != "needle here" || row.After != "pin here" {
		t.Errorf("row shows %q -> %q", row.Text, row.After)
	}
	if fileBody(t, dir, "src/fa.go") != strings.Repeat("needle here\n", 60) {
		t.Error("the preview wrote to a file")
	}
}

// TestEditorReplaceAppliesAndReportsTheRealNumbers replaces past the preview
// cap, which is the whole point of the route.
func TestEditorReplaceAppliesAndReportsTheRealNumbers(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 5; i++ {
		files["src/f"+string(rune('a'+i))+".go"] = strings.Repeat("needle here\n", 60)
	}
	r, dir := replaceServer(t, files)

	code, res := replacePost(t, r, "demo", url.Values{"q": {"needle"}, "to": {"pin"}, "apply": {"1"}})
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, res.Error)
	}
	if res.Replaced != 300 || len(res.Changed) != 5 {
		t.Errorf("replaced %d in %d files, want 300 in 5", res.Replaced, len(res.Changed))
	}
	if got := fileBody(t, dir, "src/fe.go"); strings.Contains(got, "needle") {
		t.Error("a file past the preview cap kept its matches")
	}
}

// TestEditorReplaceDollarSemantics is the mistake this kind of function is most
// often found with, checked through the route both ways.
func TestEditorReplaceDollarSemantics(t *testing.T) {
	r, dir := replaceServer(t, map[string]string{"a.go": "func GetName() {}\n"})

	if code, res := replacePost(t, r, "demo", url.Values{
		"q": {"GetName"}, "to": {"cost $1"}, "apply": {"1"},
	}); code != http.StatusOK || res.Replaced != 1 {
		t.Fatalf("literal replace = %d %+v", code, res)
	}
	if got := fileBody(t, dir, "a.go"); got != "func cost $1() {}\n" {
		t.Errorf("without a regex the dollar must stand: %q", got)
	}

	r2, dir2 := replaceServer(t, map[string]string{"a.go": "func GetName() {}\n"})
	if code, res := replacePost(t, r2, "demo", url.Values{
		"q": {`Get(\w+)`}, "to": {"Read$1"}, "re": {"1"}, "apply": {"1"},
	}); code != http.StatusOK || res.Replaced != 1 {
		t.Fatalf("regex replace = %d %+v", code, res)
	}
	if got := fileBody(t, dir2, "a.go"); got != "func ReadName() {}\n" {
		t.Errorf("with a regex $1 is a back reference: %q", got)
	}
}

// TestEditorReplaceHonoursFolderAndMask keeps the route inside the scope the
// search would have shown.
func TestEditorReplaceHonoursFolderAndMask(t *testing.T) {
	r, dir := replaceServer(t, map[string]string{
		"top.go":     "needle\n",
		"src/in.go":  "needle\n",
		"src/in.md":  "needle\n",
		"other/x.go": "needle\n",
	})

	code, res := replacePost(t, r, "demo", url.Values{
		"q": {"needle"}, "to": {"pin"}, "apply": {"1"},
		"path": {"src"}, "file": {"*.go"},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, res.Error)
	}
	if strings.Join(res.Changed, ",") != "src/in.go" {
		t.Errorf("changed = %v, want only src/in.go", res.Changed)
	}
	for _, rel := range []string{"top.go", "src/in.md", "other/x.go"} {
		if fileBody(t, dir, rel) != "needle\n" {
			t.Errorf("%s was changed although it is outside the scope", rel)
		}
	}
}

// TestEditorReplaceRefusesAnUnsavedBuffer is the guard that keeps a browser
// buffer and the disk together: the whole job stops, with the names.
func TestEditorReplaceRefusesAnUnsavedBuffer(t *testing.T) {
	r, dir := replaceServer(t, map[string]string{"a.txt": "needle\n", "b.txt": "needle\n"})

	code, res := replacePost(t, r, "demo", url.Values{
		"q": {"needle"}, "to": {"pin"}, "apply": {"1"}, "dirty": {"b.txt"},
	})
	if code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%+v)", code, res)
	}
	if strings.Join(res.Blocked, ",") != "b.txt" {
		t.Errorf("blocked = %v", res.Blocked)
	}
	if !strings.Contains(res.Error, "b.txt") {
		t.Errorf("the refusal does not name the file: %q", res.Error)
	}
	for _, rel := range []string{"a.txt", "b.txt"} {
		if fileBody(t, dir, rel) != "needle\n" {
			t.Errorf("%s was written although the job was refused", rel)
		}
	}
}

// TestEditorReplaceOneRow is the single match a row replaces on its own.
func TestEditorReplaceOneRow(t *testing.T) {
	r, dir := replaceServer(t, map[string]string{"a.txt": "needle one\nneedle two\n", "b.txt": "needle\n"})

	code, res := replacePost(t, r, "demo", url.Values{
		"q": {"needle"}, "to": {"pin"}, "apply": {"1"}, "only": {"a.txt"}, "line": {"2"},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, res.Error)
	}
	if res.Replaced != 1 || strings.Join(res.Changed, ",") != "a.txt" {
		t.Errorf("report = %+v", res)
	}
	if got := fileBody(t, dir, "a.txt"); got != "needle one\npin two\n" {
		t.Errorf("file = %q", got)
	}
	if fileBody(t, dir, "b.txt") != "needle\n" {
		t.Error("another file was touched by a single row replace")
	}
}

// TestEditorReplaceRefusesWhatIsNotAskable covers the guards on the way in.
func TestEditorReplaceRefusesWhatIsNotAskable(t *testing.T) {
	r, dir := replaceServer(t, map[string]string{"a.txt": "needle\n"})

	cases := []struct {
		name string
		form url.Values
		code int
	}{
		{"a query too short", url.Values{"q": {"n"}, "to": {"pin"}, "apply": {"1"}}, http.StatusBadRequest},
		{"a folder outside the project", url.Values{"q": {"needle"}, "to": {"pin"}, "apply": {"1"}, "path": {"../.."}}, http.StatusBadRequest},
		{"a folder that is not there", url.Values{"q": {"needle"}, "to": {"pin"}, "apply": {"1"}, "path": {"nosuch"}}, http.StatusBadRequest},
		{"a pattern that does not compile", url.Values{"q": {"(needle"}, "to": {"pin"}, "re": {"1"}, "apply": {"1"}}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		if code, res := replacePost(t, r, "demo", tc.form); code != tc.code {
			t.Errorf("%s = %d, want %d (%+v)", tc.name, code, tc.code, res)
		}
	}
	if fileBody(t, dir, "a.txt") != "needle\n" {
		t.Error("a refused request wrote to the project")
	}
	if code, _ := replacePost(t, r, "nosuchproject", url.Values{"q": {"needle"}, "to": {"pin"}}); code != http.StatusNotFound {
		t.Errorf("unknown project = %d, want 404", code)
	}
}

// TestEditorReplaceMindsCaseOnRequest is the same switch on the writing route.
func TestEditorReplaceMindsCaseOnRequest(t *testing.T) {
	r, dir := replaceServer(t, map[string]string{"a.go": "GetName\ngetname\n"})

	if _, res := replacePost(t, r, "demo", url.Values{"q": {"getname"}, "to": {"found"}}); res.Total != 2 {
		t.Errorf("without the switch the preview counts %d, want 2", res.Total)
	}
	code, res := replacePost(t, r, "demo", url.Values{
		"q": {"getname"}, "to": {"found"}, "case": {"1"}, "apply": {"1"},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, res.Error)
	}
	if res.Replaced != 1 {
		t.Errorf("replaced = %d, want only the one that was typed", res.Replaced)
	}
	if got := fileBody(t, dir, "a.go"); got != "GetName\nfound\n" {
		t.Errorf("file = %q", got)
	}
}
