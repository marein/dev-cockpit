package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/recent"
)

// A start over the socket is the page's create with a name and a first
// message the new assistant answers as if the user had sent it, live in every
// open list. A message the send would refuse makes nothing, and the browser's
// create stays as it was. A prompt over the socket is the page's send.
func TestAnAssistantStartsAnotherWithANameAndAFirstMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir, stateDir := t.TempDir(), t.TempDir()
	assistants, _, err := assistant.New(stateDir, sessionTurnCoder{dir: dir}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		assistants:      assistants,
		assistantRecent: recent.NewCapped(filepath.Join(stateDir, "recent-assistants.json"), recentEntries),
		notifier:        notify.NewService(filepath.Join(stateDir, "notifications.json"), nil),
		bus:             eventbus.New(),
	}
	assistants.SetHooks(s.PublishConversations, nil)
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "gate"), nil, 0o644)
		deadline := time.Now().Add(10 * time.Second)
		idle := func(id string) bool {
			thread, _ := assistants.Get(id)
			for _, m := range thread.Messages {
				if m.State == assistant.StateQueued {
					return false
				}
			}
			return assistants.RunningSince(id).IsZero()
		}
		for _, entry := range assistants.List() {
			for !idle(entry.ID) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
		}
	})
	asker, err := assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	post := func(local bool, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistants/new", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if local {
			req.Header.Set("Accept", "application/json")
			req.Header.Set(localapi.AssistantHeader, asker.ID)
			req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = req
		s.handleAssistantNew(c)
		c.Writer.WriteHeaderNow()
		return rec
	}

	rec := post(true, url.Values{"form": {"new"}, "name": {"x"}, "message": {strings.Repeat("x", assistant.MaxPromptBytes+1)}})
	if rec.Code != http.StatusBadRequest || len(assistants.List()) != 1 {
		t.Fatalf("a message too long answered %d and left %d assistants", rec.Code, len(assistants.List()))
	}

	events, stop := s.bus.Subscribe()
	defer stop()
	rec = post(true, url.Values{"form": {"new"}, "name": {"Release notes"}, "message": {"Write the notes for 2.1."}})
	var answer map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("the start answered %d %s", rec.Code, rec.Body.String())
	}
	id, _ := answer["id"].(string)
	if answer["name"] != "Release notes" || answer["answering"] != true || id == "" {
		t.Fatalf("the start answered %v", answer)
	}
	select {
	case ev := <-events:
		if ev.Type != "assistant" {
			t.Fatalf("the start published %q, the lists pull on assistant", ev.Type)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no open list heard about the new assistant")
	}
	created, err := assistants.Get(id)
	if err != nil || created.Title != "Release notes" || len(created.Messages) == 0 || created.Messages[0].Role != assistant.RoleUser || created.Messages[0].Content != "Write the notes for 2.1." {
		t.Fatalf("the new assistant reads %+v, %v", created.Summary, err)
	}

	rec = post(false, url.Values{"form": {"new"}, "coder": {"claude"}, "name": {"ignored"}, "message": {"ignored"}})
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/assistants/") {
		t.Fatalf("the browser's create answered %d %s", rec.Code, rec.Header().Get("Location"))
	}
	mine, _ := assistants.Get(strings.TrimPrefix(rec.Header().Get("Location"), "/assistants/"))
	if mine.Title != assistant.DefaultTitle || len(mine.Messages) != 0 {
		t.Fatalf("the browser's create took the fields of a start: %+v", mine.Summary)
	}

	// A prompt over the socket is the page's send: the free assistant answers
	// at once, the one still answering its first message queues it, the asker
	// itself included, and a broken one is refused with the page's sentence.
	prompt := func(id, message string) (int, map[string]any) {
		form := url.Values{"form": {"message"}, "message": {message}}
		req := httptest.NewRequest(http.MethodPost, "/assistants/"+id, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.Header.Set(localapi.AssistantHeader, asker.ID)
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = req
		c.Params = gin.Params{{Key: "id", Value: id}}
		s.handleAssistantAction(c)
		c.Writer.WriteHeaderNow()
		var answer map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &answer)
		return rec.Code, answer
	}
	if code, answer := prompt(asker.ID, "Look at the release."); code != http.StatusOK || answer["queued"] != false || answer["runId"] == "" {
		t.Fatalf("a prompt into a free assistant answered %d %v", code, answer)
	}
	if code, answer := prompt(id, "And the changelog."); code != http.StatusOK || answer["queued"] != true || answer["title"] != "Release notes" {
		t.Fatalf("a prompt into a busy assistant answered %d %v", code, answer)
	}
	if code, answer := prompt(asker.ID, "Then tell me."); code != http.StatusOK || answer["queued"] != true {
		t.Fatalf("a prompt to itself answered %d %v", code, answer)
	}
	for _, bad := range []struct{ id, message string }{{asker.ID, " "}, {asker.ID, strings.Repeat("x", assistant.MaxPromptBytes+1)}, {"no-such-assistant", "hello"}} {
		if code, answer := prompt(bad.id, bad.message); code != http.StatusBadRequest || answer["error"] == "" {
			t.Fatalf("a broken prompt answered %d %v", code, answer)
		}
	}
	thread, _ := assistants.Get(id)
	if last := thread.Messages[len(thread.Messages)-1]; last.Role != assistant.RoleUser || last.State != assistant.StateQueued || last.Content != "And the changelog." {
		t.Fatalf("the busy assistant holds %+v last", last)
	}
}
