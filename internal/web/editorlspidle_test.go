package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/editorintelligence"
)

// lspIdleRound posts the LSP tab and follows the redirect with the session
// cookie, answering the page the flash lands on.
func lspIdleRound(t *testing.T, r http.Handler, form url.Values) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, editorLSPSettingsPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("save answered %d: %s", rec.Code, rec.Body.String())
	}
	get := httptest.NewRequest(http.MethodGet, editorLSPSettingsPath, nil)
	for _, c := range rec.Result().Cookies() {
		get.AddCookie(c)
	}
	page := httptest.NewRecorder()
	r.ServeHTTP(page, get)
	if page.Code != http.StatusOK {
		t.Fatalf("page answered %d", page.Code)
	}
	return page.Body.String()
}

func TestLSPIdleTimeoutSetting(t *testing.T) {
	r, s := modelSettingsServer(t)
	r.GET(editorLSPSettingsPath, s.handleSettingsEditorLSP)
	r.POST(editorLSPSettingsPath, s.handleSettingsEditorLSPSave)

	if got := s.lspIdleTimeout(); got != 5*time.Minute {
		t.Fatalf("a fresh install stops idle servers after five minutes, got %s", got)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, editorLSPSettingsPath, nil))
	body := rec.Body.String()
	field := strings.Index(body, `name="idle_seconds"`)
	if field < 0 || !strings.Contains(body, `value="300"`) || !strings.Contains(body, `min="60"`) {
		t.Fatalf("the tab carries the idle field on its default and its floor:\n%s", body)
	}
	if first := strings.Index(body, `name="server_`); first >= 0 && first < field {
		t.Fatal("the idle field stands above the per language selects")
	}
	if !strings.Contains(body, "Default 300, minimum 60, maximum 86400.") || !strings.Contains(body, "Default 3, minimum 1, maximum 32.") {
		t.Fatalf("the help texts name default and range of both settings:\n%s", body)
	}

	for _, bad := range []string{"30", "59", "abc", "", "86401"} {
		page := lspIdleRound(t, r, url.Values{"idle_seconds": {bad}, "server_go": {"off"}})
		if !strings.Contains(page, "Idle timeout must be a whole number of seconds from 60 to 86400.") {
			t.Fatalf("value %q must be refused with a sentence, page:\n%s", bad, page)
		}
		if _, ok := s.settings.Lookup(editorLSPIdleSecondsKey); ok {
			t.Fatalf("value %q must not be stored", bad)
		}
		if s.settings.Get(editorLSPServerKey("go")) != "" {
			t.Fatalf("a refused save stores nothing else either (value %q)", bad)
		}
	}

	page := lspIdleRound(t, r, url.Values{"idle_seconds": {"60"}})
	if !strings.Contains(page, "Settings saved.") || !strings.Contains(page, `value="60"`) {
		t.Fatalf("the floor itself is accepted:\n%s", page)
	}
	if got := s.lspIdleTimeout(); got != time.Minute {
		t.Fatalf("the saved value is what the service reads next, got %s", got)
	}
	lspIdleRound(t, r, url.Values{"idle_seconds": {" 900 "}})
	if got := s.lspIdleTimeout(); got != 15*time.Minute {
		t.Fatalf("a second save applies at once, got %s", got)
	}
	lspIdleRound(t, r, url.Values{"server_go": {"off"}})
	if got := s.lspIdleTimeout(); got != 15*time.Minute {
		t.Fatalf("a post without the field keeps the timeout, got %s", got)
	}

	s.settings.Set(editorLSPIdleSecondsKey, "10")
	if got := s.lspIdleTimeout(); got != time.Minute {
		t.Fatalf("a stored value under the floor reads as the floor, got %s", got)
	}
	s.settings.Set(editorLSPIdleSecondsKey, "999999")
	if got := s.lspIdleTimeout(); got != 24*time.Hour {
		t.Fatalf("a stored value above the ceiling reads as the ceiling, got %s", got)
	}
	s.settings.Set(editorLSPIdleSecondsKey, "0")
	if got, want := s.lspIdleTimeout(), editorintelligence.ClampIdleTimeout(0); got != want {
		t.Fatalf("the web layer and the service read a stored value by one rule, got %s and %s", got, want)
	}
}

func TestLSPMaxProjectsSetting(t *testing.T) {
	r, s := modelSettingsServer(t)
	r.GET(editorLSPSettingsPath, s.handleSettingsEditorLSP)
	r.POST(editorLSPSettingsPath, s.handleSettingsEditorLSPSave)

	if got := s.lspMaxProjects(); got != 3 {
		t.Fatalf("a fresh install runs three projects, got %d", got)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, editorLSPSettingsPath, nil))
	body := rec.Body.String()
	idle := strings.Index(body, `name="idle_seconds"`)
	field := strings.Index(body, `name="max_projects"`)
	if field < 0 || !strings.Contains(body, `name="max_projects" min="1" max="32" step="1" required value="3"`) {
		t.Fatalf("the tab carries the limit on its default and its range:\n%s", body)
	}
	if field < idle {
		t.Fatal("the limit stands below the idle timeout")
	}
	if first := strings.Index(body, `name="server_`); first >= 0 && first < field {
		t.Fatal("the limit stands above the per language selects")
	}

	for _, bad := range []string{"0", "-1", "abc", "", "33"} {
		page := lspIdleRound(t, r, url.Values{"idle_seconds": {"120"}, "max_projects": {bad}, "server_go": {"off"}})
		if !strings.Contains(page, "Max running projects must be a whole number from 1 to 32.") {
			t.Fatalf("value %q must be refused with a sentence, page:\n%s", bad, page)
		}
		if s.settings.Get(editorLSPMaxProjectsKey) != "" || s.settings.Get(editorLSPIdleSecondsKey) != "" || s.settings.Get(editorLSPServerKey("go")) != "" {
			t.Fatalf("a refused limit %q stores nothing of the save", bad)
		}
	}

	page := lspIdleRound(t, r, url.Values{"max_projects": {"1"}})
	if !strings.Contains(page, "Settings saved.") || !strings.Contains(page, `name="max_projects" min="1" max="32" step="1" required value="1"`) {
		t.Fatalf("the floor itself is accepted:\n%s", page)
	}
	if got := s.lspMaxProjects(); got != 1 {
		t.Fatalf("the saved value is what the service reads next, got %d", got)
	}
	lspIdleRound(t, r, url.Values{"max_projects": {" 5 "}})
	if got := s.lspMaxProjects(); got != 5 {
		t.Fatalf("a second save applies at once, got %d", got)
	}
	lspIdleRound(t, r, url.Values{"idle_seconds": {"120"}})
	if got := s.lspMaxProjects(); got != 5 {
		t.Fatalf("a post without the field keeps the limit, got %d", got)
	}

	s.settings.Set(editorLSPMaxProjectsKey, "0")
	if got := s.lspMaxProjects(); got != 1 || got != editorintelligence.ClampMaxProjects(0) {
		t.Fatalf("a stored value under the floor reads as the floor, the service's rule, got %d", got)
	}
	s.settings.Set(editorLSPMaxProjectsKey, "99")
	if got := s.lspMaxProjects(); got != 32 {
		t.Fatalf("a stored value above the ceiling reads as the ceiling, got %d", got)
	}
}

// LSP is the first tab of the editor settings, and the bare path leads there.
func TestEditorSettingsOpenOnTheLSPTab(t *testing.T) {
	r, s := modelSettingsServer(t)
	r.GET("/settings/editor", s.handleSettingsEditor)
	r.GET(editorLSPSettingsPath, s.handleSettingsEditorLSP)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings/editor", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != editorLSPSettingsPath {
		t.Fatalf("the bare path answered %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, editorLSPSettingsPath, nil))
	body := rec.Body.String()
	nav := body[strings.Index(body, "data-editor-sections"):]
	if first := strings.Index(nav, `href="/settings/editor/`); first < 0 || !strings.HasPrefix(nav[first:], `href="/settings/editor/lsp"`) {
		t.Fatalf("LSP is not the first tab:\n%s", nav)
	}
}
