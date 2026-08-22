package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// oneCoder is an assistant with a single installed coder, enough to open a
// conversation. The runner stays nil, this test never runs a turn.
type oneCoder struct{}

func (oneCoder) Available() []assistant.CoderInfo {
	return []assistant.CoderInfo{{ID: "claude", Label: "Claude"}}
}

// The panel chat fetch must leave the notification unread. An open panel
// fetches this view on every assistant event, in background windows too, so a
// server side read here would land before the push dispatcher re-checks
// unread, and assistant news would never toast, jingle or push. Reading is
// the client's decision, posted only for a surface that is visible in a
// focused window.
func TestThePanelChatFetchLeavesTheNotificationUnread(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	current, err := conversations.Open("")
	if err != nil {
		t.Fatalf("open conversation: %v", err)
	}
	notifier := notify.NewService(filepath.Join(stateDir, "notifications.json"), nil)
	notifier.Add(current.ID)
	if !notifier.UnreadTargets()[current.ID] {
		t.Fatalf("the notification has to start unread")
	}
	s := &Server{
		conversations: conversations,
		assistant:     workspace,
		watcher:       assistant.NewWatcher(conversations, assistant.NewJobStore(stateDir), nil),
		notifier:      notifier,
	}

	// The handler renders the panel fragment and reads the CSRF token from the
	// session, so the engine carries the embedded templates and the session
	// middleware, nothing else.
	r := gin.New()
	r.SetHTMLTemplate(render.HTMLTemplate(func(p string) string { return p }, "test", "test"))
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.GET("/assistant/panel", s.handleAssistantPanel)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assistant/panel", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("panel fetch failed: %d: %s", rec.Code, rec.Body.String())
	}
	if !s.notifier.UnreadTargets()[current.ID] {
		t.Fatalf("the panel fetch read the notification away")
	}

	// The named conversation form of the same view is what a notification
	// link opens, it must stay a pure read as well.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assistant/panel?conversation="+current.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("named panel fetch failed: %d: %s", rec.Code, rec.Body.String())
	}
	if !s.notifier.UnreadTargets()[current.ID] {
		t.Fatalf("the named panel fetch read the notification away")
	}
}
