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
)

// oneCoder is an assistant with a single installed coder, enough to open a
// conversation. The runner stays nil, this test never runs a turn.
type oneCoder struct{}

func (oneCoder) Available() []assistant.CoderInfo {
	return []assistant.CoderInfo{{ID: "claude", Label: "Claude"}}
}

// The page's reads must leave the notification unread. The page pulls its
// conversation, its list column and its memory again on every assistant
// event, in background windows too, so a server side read in any of them
// would land before the push dispatcher re-checks unread, and assistant news
// would never toast, jingle or push. Reading is the client's decision, posted
// only for a surface that is visible in a focused window.
func TestThePageReadsLeaveTheNotificationUnread(t *testing.T) {
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

	// The three reads the page is made of, behind the session middleware the
	// CSRF token comes from. The page handler itself also builds the shell,
	// which needs the whole server; the reads are what could mark anything.
	r := gin.New()
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.GET("/assistant/:id", func(c *gin.Context) {
		conversation, err := s.conversations.Get(c.Param("id"))
		if err != nil {
			c.String(http.StatusNotFound, err.Error())
			return
		}
		data := s.assistantData(conversation, false)
		data.Ctx = s.assistantCtxData(c, data.Path, "assistant-ctx-new")
		_ = s.assistantMemoryData(c, "memory")
		if data.Ctx.Current == nil || data.Ctx.Current.ID != conversation.ID {
			c.String(http.StatusInternalServerError, "the column does not carry the live conversation")
			return
		}
		c.Status(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assistant/"+current.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("page reads failed: %d: %s", rec.Code, rec.Body.String())
	}
	if !s.notifier.UnreadTargets()[current.ID] {
		t.Fatalf("the page reads read the notification away")
	}
}
