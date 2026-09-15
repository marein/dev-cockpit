package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The area moved from /assistant to /assistants, and the old subtree keeps
// answering: a stored push message and a notification entry point at
// /assistant/<id>, and a page loaded before the move still posts to the old
// path. Everything there answers 308, which keeps the method, so a form replays
// against the new address instead of losing its body.
func TestTheOldAssistantPathsLeadToTheNewOnes(t *testing.T) {
	r, _ := pluginRouter(t)
	session := signIn(t, r)

	moved := []struct{ method, from, to string }{
		{http.MethodGet, "/assistant", "/assistants"},
		{http.MethodGet, "/assistant/c1", "/assistants/c1"},
		{http.MethodGet, "/assistant/c1/stream", "/assistants/c1/stream"},
		{http.MethodGet, "/assistant/c1/draft", "/assistants/c1/draft"},
		{http.MethodGet, "/assistant/c1/media/assistant-files/c1/shot.png", "/assistants/c1/media/assistant-files/c1/shot.png"},
		{http.MethodGet, "/assistant/c1/messages/m1", "/assistants/c1/messages/m1"},
		{http.MethodGet, "/assistant/memory", "/assistants/memory"},
		{http.MethodGet, "/assistant/instances", "/assistants/instances"},
		{http.MethodGet, "/assistant/instances/c1", "/assistants/instances/c1"},
		// The overlay these two served is the page now, so they lead to the
		// area itself and not to a plural twin that never existed.
		{http.MethodGet, "/assistant/panel", "/assistants"},
		{http.MethodGet, "/assistant/history", "/assistants"},
		// The phone's sheet pulls its column by area name, and that is plural
		// too now.
		{http.MethodGet, "/ctx/assistant", "/ctx/assistants"},
	}
	for _, one := range moved {
		req := httptest.NewRequest(one.method, one.from, nil)
		req.Header.Set("Cookie", session)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusPermanentRedirect {
			t.Fatalf("%s %s answered %d, want 308", one.method, one.from, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != one.to {
			t.Fatalf("%s %s led to %q, want %q", one.method, one.from, got, one.to)
		}
	}

	// The query travels along, or the media route and the whole transcript view
	// would land on the page without what they were asked for.
	req := httptest.NewRequest(http.MethodGet, "/assistant/c1?all=1", nil)
	req.Header.Set("Cookie", session)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if got := rec.Header().Get("Location"); got != "/assistants/c1?all=1" {
		t.Fatalf("the query was dropped: %q", got)
	}
}

// The unsafe methods lead to the same place. They are not requested here, a
// POST without the session's CSRF token never reaches its handler, so the route
// table is what says where they go.
func TestTheOldAssistantPathsForwardEveryMethod(t *testing.T) {
	r, _ := pluginRouter(t)
	forwarded := map[string]bool{
		"POST /assistant":             false,
		"POST /assistant/:id":         false,
		"POST /assistant/:id/*rest":   false,
		"DELETE /assistant/:id/*rest": false,
	}
	for _, route := range r.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := forwarded[key]; !ok {
			continue
		}
		if !strings.Contains(route.Handler, "movedAssistantPath") {
			t.Fatalf("%s is handled by %s, want the forward", key, route.Handler)
		}
		forwarded[key] = true
	}
	for key, found := range forwarded {
		if !found {
			t.Fatalf("%s is not registered", key)
		}
	}
}

// What a released CLI calls over the socket answers in place instead of
// redirecting: the local API client never follows a redirect, it wants the
// JSON, so a 308 there would not move it, it would break it. They are the only
// real routes left in the old subtree, and the forward must not swallow them.
func TestTheOldCLIPathsStillAnswerThemselves(t *testing.T) {
	r, _ := pluginRouter(t)
	inPlace := map[string]bool{
		"GET /assistant/jobs":              false,
		"POST /assistant/jobs":             false,
		"GET /assistant/conversations":     false,
		"GET /assistant/conversations/:id": false,
	}
	for _, route := range r.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := inPlace[key]; !ok {
			continue
		}
		if strings.Contains(route.Handler, "movedAssistantPath") {
			t.Fatalf("%s forwards, but the local API client does not follow a redirect", key)
		}
		inPlace[key] = true
	}
	for key, found := range inPlace {
		if !found {
			t.Fatalf("%s is not registered", key)
		}
	}
}

// The routes the area is served on now, so a rename that misses one is caught
// here and not in the browser.
func TestTheAssistantAreaIsServedInThePlural(t *testing.T) {
	r, _ := pluginRouter(t)
	want := map[string]bool{
		"GET /assistants":                         false,
		"GET /assistants/:id":                     false,
		"POST /assistants/:id":                    false,
		"POST /assistants/order":                  false,
		"GET /assistants/:id/stream":              false,
		"GET /assistants/:id/draft":               false,
		"GET /assistants/:id/media/*path":         false,
		"GET /assistants/:id/messages/:messageId": false,
		"POST /assistants/:id/user-upload":        false,
		"POST /assistants/:id/stt":                false,
		"GET /assistants/memory":                  false,
		"POST /assistants/memory":                 false,
		"GET /assistants/jobs":                    false,
		"POST /assistants/jobs":                   false,
		"GET /assistants/instances":               false,
		"GET /assistants/instances/:id":           false,
	}
	for _, route := range r.Routes() {
		if _, ok := want[route.Method+" "+route.Path]; ok {
			want[route.Method+" "+route.Path] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Fatalf("route %s is not registered", key)
		}
	}
}
