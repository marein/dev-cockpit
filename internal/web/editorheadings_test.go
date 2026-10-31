package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

func postHeadings(t *testing.T, form url.Values) (int, string) {
	t.Helper()
	s, r := lspClassNameServer(t)
	r.POST("/projects/:name/editor/headings", s.handleEditorHeadings)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/demo/editor/headings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	r.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// The test's server has no editor intelligence at all.
func TestEditorHeadingsAnswerTheStructureWithoutAServer(t *testing.T) {
	code, body := postHeadings(t, url.Values{"content": {"# Guide\n\n## Install\nrun it\n"}, "line": {"3"}, "character": {"2"}})
	want := `{"available":true,"symbols":[{"name":"Guide","kind":"h1","anchor":"guide","line":1,"character":2,"children":[{"name":"Install","kind":"h2","anchor":"install","line":3,"character":3,"cursor":true}]}]}`
	if code != http.StatusOK || body != want {
		t.Fatalf("%d %s", code, body)
	}
}

func TestEditorHeadingsRefuseWhatIsNoDocument(t *testing.T) {
	for name, form := range map[string]url.Values{
		"a line that is no number":  {"content": {"# A"}, "line": {"x"}},
		"a document past the limit": {"content": {strings.Repeat("x", filesystem.MaxEditableBytes+1)}},
	} {
		code, body := postHeadings(t, form)
		answer := map[string]any{}
		_ = json.Unmarshal([]byte(body), &answer)
		if code != http.StatusBadRequest || answer["error"] == nil {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
}
