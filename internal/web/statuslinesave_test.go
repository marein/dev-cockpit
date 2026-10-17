package web

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder/claude/statusline"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// ownerSession signs in to s, whose routes are the router's own, behind the
// cookie session the flash rides on. send sends the session the last answer
// set, token is the session's CSRF token.
func ownerSession(t *testing.T, s *Server) (send func(*http.Request) *httptest.ResponseRecorder, token string) {
	t.Helper()
	s.cfg.AuthUsername = "admin"
	r := gin.New()
	r.SetHTMLTemplate(render.HTMLTemplate(s.assets.assetPath, "test", "test", s.plugins, nil))
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.GET("/test/sign-in", func(c *gin.Context) {
		ginsessions.Default(c).Set(sessionUserKey, "admin")
		c.String(http.StatusOK, s.csrfToken(c))
	})
	s.registerRoutes(r)

	var session string
	send = func(req *http.Request) *httptest.ResponseRecorder {
		if session != "" {
			req.Header.Set("Cookie", session)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if next, _, _ := strings.Cut(rec.Header().Get("Set-Cookie"), ";"); next != "" {
			session = next
		}
		return rec
	}
	token = send(httptest.NewRequest(http.MethodGet, "/test/sign-in", nil)).Body.String()
	if session == "" || token == "" {
		t.Fatal("the sign-in answered no session")
	}
	return send, token
}

// statusLineSession answers an owner's post of the status line form and read
// of its page, over ownerSession.
func statusLineSession(t *testing.T) (s *Server, path string, post func(url.Values) *httptest.ResponseRecorder, get func() string) {
	t.Helper()
	_, s = modelSettingsServer(t)
	send, token := ownerSession(t, s)
	path = s.coderBase(s.coders[0]) + "/" + statusLineSection
	post = func(form url.Values) *httptest.ResponseRecorder {
		form.Set(csrfFieldName, token)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return send(req)
	}
	get = func() string {
		rec := send(httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", path, rec.Code)
		}
		return rec.Body.String()
	}
	return s, path, post, get
}

// A refused save answers like any other: the redirect back with the refusal in
// the flash and the stored line as it was. The flash rides the session cookie,
// and a bound quoted uncut once outgrew it, so the save ended in a 500.
func TestStatusLineSaveRefusesPastItsBoundsWithAShortFlash(t *testing.T) {
	s, path, post, get := statusLineSession(t)
	valid := func() url.Values {
		return url.Values{
			"mode":              {"always"},
			"entry_kind":        {"value"},
			"entry_value":       {"context"},
			"entry_label":       {"c"},
			"entry_label_color": {"dim"},
			"entry_color":       {""},
			"entry_text":        {""},
			"entry_thresholds":  {"1"},
			"threshold_at":      {"0"},
			"threshold_color":   {"green"},
		}
	}
	if rec := post(valid()); rec.Code != http.StatusSeeOther {
		t.Fatalf("the valid save answered %d", rec.Code)
	}
	get()
	stored := s.settings.Get(statusline.SettingKey)
	written, err := os.ReadFile(statusline.ConfigPath(s.cfg.StateDir))
	if !strings.Contains(stored, `"context"`) || err != nil {
		t.Fatalf("the valid save stored %q and wrote %v", stored, err)
	}

	longBound := valid()
	longBound.Set("threshold_at", strings.Repeat("x", 10000))
	manyEntries := valid()
	manyEntries["entry_kind"] = slices.Repeat([]string{"break"}, statusline.MaxEntries+1)
	shown := strings.Repeat("ä", statusline.MaxShownTextRunes+1)
	longSeparator := valid()
	longSeparator.Set("entry_kind", string(statusline.KindSeparator))
	longSeparator.Set("entry_text", shown)
	longFreeText := valid()
	longFreeText.Set("entry_value", statusline.FreeTextValue)
	longFreeText.Set("entry_text", shown)
	shownSays := fmt.Sprintf("At most %d characters per text.", statusline.MaxShownTextRunes)
	flash := regexp.MustCompile(`(?s)data-statusline-flash>\s*<div class="alert alert-danger`)
	for _, c := range []struct {
		name string
		form url.Values
		says string
	}{
		{"a bound of 10000 characters", longBound, fmt.Sprintf("At most %d characters per bound.", statusline.MaxBoundLength)},
		{"one entry past the most", manyEntries, fmt.Sprintf("At most %d entries.", statusline.MaxEntries)},
		{"a separator past a part of the line", longSeparator, shownSays},
		{"a free text past a part of the line", longFreeText, shownSays},
	} {
		rec := post(c.form)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != path+"#settings-statusline" {
			t.Fatalf("%s: answered %d %q, want the redirect back to the section", c.name, rec.Code, rec.Header().Get("Location"))
		}
		page := get()
		if !flash.MatchString(page) || notice(page) != c.says {
			t.Fatalf("%s: the flash says %q, want the refusal %q", c.name, notice(page), c.says)
		}
		if got := s.settings.Get(statusline.SettingKey); got != stored {
			t.Fatalf("%s: the stored line became %.200q", c.name, got)
		}
		if now, err := os.ReadFile(statusline.ConfigPath(s.cfg.StateDir)); err != nil || !bytes.Equal(now, written) {
			t.Fatalf("%s: the written line changed (%v)", c.name, err)
		}
	}
}
