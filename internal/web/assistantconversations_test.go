package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
)

// The two conversation routes exist for the assistant's own CLI: the index and
// one transcript as JSON, read from the same service the overlay renders from.
// They only report, nothing here writes or marks anything read.
func TestConversationRoutesServeTheAssistantReads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	store := assistant.NewStore(stateDir)
	at := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	longAnswer := strings.Repeat("a", assistant.TranscriptMessageRunes+100)
	tabs := "11111111-1111-4111-8111-111111111111"
	plans := "22222222-2222-4222-8222-222222222222"
	store.Save(assistant.Conversation{
		Summary: assistant.Summary{ID: tabs, Title: "Fix the tabs", CoderID: "claude", Status: assistant.StatusArchived},
		Messages: []assistant.Message{
			{ID: "m1", Role: assistant.RoleUser, Content: "the strip flickers", CreatedAt: at, State: assistant.StateComplete},
			{ID: "m2", Role: assistant.RoleAssistant, Content: longAnswer, CreatedAt: at.Add(time.Minute), State: assistant.StateComplete},
		},
	})
	store.Save(assistant.Conversation{
		Summary:  assistant.Summary{ID: plans, Title: "Weekend plans", CoderID: "claude", Status: assistant.StatusArchived},
		Messages: []assistant.Message{{ID: "m3", Role: assistant.RoleUser, Content: "nothing else", CreatedAt: at.Add(time.Hour), State: assistant.StateComplete}},
	})
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	s := &Server{conversations: conversations, assistant: workspace}
	r := gin.New()
	r.GET("/assistant/conversations", s.handleAssistantConversations)
	r.GET("/assistant/conversations/:id", s.handleAssistantConversationRead)

	get := func(path string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d: %s", path, rec.Code, rec.Body.String())
		}
		var answer map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return answer
	}

	list, _ := get("/assistant/conversations")["conversations"].([]any)
	if len(list) != 2 {
		t.Fatalf("want both conversations listed, got %d", len(list))
	}
	first, _ := list[0].(map[string]any)
	if first["id"] != plans {
		t.Fatalf("want the newest conversation first, got %v", first["id"])
	}
	second, _ := list[1].(map[string]any)
	if preview, _ := second["preview"].(string); !strings.HasPrefix(longAnswer, strings.TrimSuffix(preview, "…")) || preview == "" {
		t.Fatalf("want the stored preview in the list, got %q", preview)
	}

	filtered, _ := get("/assistant/conversations?contains=FLICKERS")["conversations"].([]any)
	if len(filtered) != 1 {
		t.Fatalf("want the message match to narrow the list, got %d entries", len(filtered))
	}
	if entry, _ := filtered[0].(map[string]any); entry["id"] != tabs {
		t.Fatalf("want the conversation carrying the word, got %v", entry["id"])
	}

	one := get("/assistant/conversations/" + tabs)
	messages, _ := one["messages"].([]any)
	if len(messages) != 2 || int(one["messageCount"].(float64)) != 2 || int(one["dropped"].(float64)) != 0 {
		t.Fatalf("want the whole short transcript, got %v", one)
	}
	cut, _ := messages[1].(map[string]any)
	if content, _ := cut["content"].(string); !strings.Contains(content, "runes shown, use --full") {
		t.Fatalf("a long message has to arrive cut with a note, got %q", content)
	}
	if role, _ := cut["role"].(string); role != "assistant" {
		t.Fatalf("want the role next to the text, got %q", role)
	}

	full := get("/assistant/conversations/" + tabs + "?entries=1&full=1")
	fullMessages, _ := full["messages"].([]any)
	if len(fullMessages) != 1 || int(full["dropped"].(float64)) != 1 {
		t.Fatalf("want one message with one dropped, got %v", full)
	}
	if entry, _ := fullMessages[0].(map[string]any); entry["content"] != longAnswer {
		t.Fatal("full has to lift the per message cut")
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assistant/conversations/99999999-9999-4999-8999-999999999999", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown conversation has to answer not found, got %d", rec.Code)
	}
}
