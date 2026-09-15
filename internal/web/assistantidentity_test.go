package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/localapi"
)

// Reaching the socket says the caller may act. Who is acting is a different
// question, and with several assistants sharing that socket it has a different
// answer: every command an assistant runs carries its id on --as and the local
// API client sends it on a header. Everything that has to be charged to
// somebody refuses on an empty answer instead of picking one, because picking
// one means steering a coder in somebody else's name.
func TestASocketCallerIsOnlyAnAssistantWhenItSaysSo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	store := assistant.NewStore(stateDir)
	live := "11111111-1111-4111-8111-111111111111"
	store.Save(assistant.Instance{Summary: assistant.Summary{ID: live, Title: "Release work", CoderID: "claude"}})
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	s := &Server{
		assistants: conversations,
		workspace:  workspace,
		watcher:    assistant.NewWatcher(conversations, assistant.NewJobs(store), nil, nil),
	}

	r := gin.New()
	r.POST("/who", func(c *gin.Context) { c.String(http.StatusOK, s.callingAssistant(c)) })
	// steerOwner is what a steer asks before it writes a job, so it is where
	// the refusal has to happen.
	r.POST("/steer", func(c *gin.Context) {
		owner, err := s.steerOwner(c)
		if err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.String(http.StatusOK, owner)
	})

	post := func(path string, local bool, id string, form url.Values) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if id != "" {
			req.Header.Set(localapi.AssistantHeader, id)
		}
		if local {
			req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	// A browser is nobody in particular, however it asks.
	if _, who := post("/who", false, live, nil); who != "" {
		t.Fatalf("a request that never came over the socket answered as %q", who)
	}
	// A turn names itself, and only a name that still exists counts.
	if _, who := post("/who", true, live, nil); who != live {
		t.Fatalf("a turn was not recognized, got %q", who)
	}
	if _, who := post("/who", true, "33333333-3333-4333-8333-333333333333", nil); who != "" {
		t.Fatalf("a stale id acted as an assistant: %q", who)
	}
	if _, who := post("/who", true, "", nil); who != "" {
		t.Fatalf("a caller without an id acted as an assistant: %q", who)
	}

	// The refusal names the flag, because the one caller that can act on it is
	// a turn that dropped it from the command its instructions spell out.
	code, said := post("/steer", true, "", nil)
	if code != http.StatusBadRequest || !strings.Contains(said, "--as") {
		t.Fatalf("a socket steer without an identity was not refused by name: %d %q", code, said)
	}
	if code, owner := post("/steer", true, live, nil); code != http.StatusOK || owner != live {
		t.Fatalf("a turn could not steer for itself: %d %q", code, owner)
	}
	// The user steering from a page picks, and the pick is checked.
	if code, owner := post("/steer", false, "", url.Values{"assistant": {live}}); code != http.StatusOK || owner != live {
		t.Fatalf("the page could not steer for the assistant it named: %d %q", code, owner)
	}
	if code, _ := post("/steer", false, "", url.Values{"assistant": {"44444444-4444-4444-8444-444444444444"}}); code != http.StatusBadRequest {
		t.Fatalf("the page steered for an assistant that does not exist: %d", code)
	}
}
