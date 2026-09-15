package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
)

// The two instance routes exist for the assistants' own CLI: the index of all
// of them and one transcript as JSON, which is how they see each other. They
// only report, nothing here writes or marks anything read.
func TestInstanceRoutesServeTheAssistantReads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	store := assistant.NewStore(stateDir)
	at := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	longAnswer := strings.Repeat("a", assistant.TranscriptMessageRunes+100)
	tabs := "11111111-1111-4111-8111-111111111111"
	plans := "22222222-2222-4222-8222-222222222222"
	store.Save(assistant.Instance{
		Summary: assistant.Summary{ID: tabs, Title: "Fix the tabs", CoderID: "claude"},
		Messages: []assistant.Message{
			{ID: "m1", Role: assistant.RoleUser, Content: "the strip flickers", CreatedAt: at, State: assistant.StateComplete},
			{ID: "m2", Role: assistant.RoleAssistant, Content: longAnswer, CreatedAt: at.Add(time.Minute), State: assistant.StateComplete},
		},
	})
	store.Save(assistant.Instance{
		Summary:  assistant.Summary{ID: plans, Title: "Weekend plans", CoderID: "claude"},
		Messages: []assistant.Message{{ID: "m3", Role: assistant.RoleUser, Content: "nothing else", CreatedAt: at.Add(time.Hour), State: assistant.StateComplete}},
	})
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	s := &Server{assistants: conversations, workspace: workspace, watcher: assistant.NewWatcher(conversations, assistant.NewJobs(store), nil, nil)}
	r := gin.New()
	r.GET("/assistants/instances", s.handleAssistantInstances)
	r.GET("/assistants/instances/:id", s.handleAssistantInstanceRead)

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

	list, _ := get("/assistants/instances")["assistants"].([]any)
	if len(list) != 2 {
		t.Fatalf("want both assistants listed, got %d", len(list))
	}
	first, _ := list[0].(map[string]any)
	if first["id"] != plans {
		t.Fatalf("want the newest assistant first, got %v", first["id"])
	}
	second, _ := list[1].(map[string]any)
	if preview, _ := second["preview"].(string); !strings.HasPrefix(longAnswer, strings.TrimSuffix(preview, "…")) || preview == "" {
		t.Fatalf("want the stored preview in the list, got %q", preview)
	}

	filtered, _ := get("/assistants/instances?contains=FLICKERS")["assistants"].([]any)
	if len(filtered) != 1 {
		t.Fatalf("want the message match to narrow the list, got %d entries", len(filtered))
	}
	if entry, _ := filtered[0].(map[string]any); entry["id"] != tabs {
		t.Fatalf("want the assistant carrying the word, got %v", entry["id"])
	}

	one := get("/assistants/instances/" + tabs)
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

	full := get("/assistants/instances/" + tabs + "?entries=1&full=1")
	fullMessages, _ := full["messages"].([]any)
	if len(fullMessages) != 1 || int(full["dropped"].(float64)) != 1 {
		t.Fatalf("want one message with one dropped, got %v", full)
	}
	if entry, _ := fullMessages[0].(map[string]any); entry["content"] != longAnswer {
		t.Fatal("full has to lift the per message cut")
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assistants/instances/99999999-9999-4999-8999-999999999999", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown assistant has to answer not found, got %d", rec.Code)
	}
}

// One assistant's media address serves the files of that assistant's own
// workspace, and nobody else's: a path is read inside the workspace of the
// assistant the address names, so the same relative path under another
// assistant's address lands in that other workspace, or nowhere.
func TestMediaAddressServesItsOwnAssistantsWorkspace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	store := assistant.NewStore(stateDir)
	mine := "11111111-1111-4111-8111-111111111111"
	theirs := "22222222-2222-4222-8222-222222222222"
	for _, id := range []string{mine, theirs} {
		store.Save(assistant.Instance{Summary: assistant.Summary{ID: id, Title: id, CoderID: "claude"}})
	}
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	write := func(id, rel, body string) {
		t.Helper()
		full := filepath.Join(workspace.Dir(id), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(mine, assistant.FilesDirName+"/mine.txt", "mine")
	write(mine, "drafts/note.txt", "anywhere in the workspace")
	write(theirs, assistant.FilesDirName+"/theirs.txt", "theirs")
	write(theirs, "user-upload/pic.txt", "theirs")

	s := &Server{assistants: conversations, workspace: workspace}
	r := gin.New()
	r.GET("/assistants/:id/media/*path", s.handleAssistantMedia)
	get := func(id, rel string) int {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assistants/"+id+"/media/"+rel, nil))
		return rec.Code
	}

	for _, rel := range []string{
		assistant.FilesDirName + "/mine.txt",
		"drafts/note.txt",
	} {
		if code := get(mine, rel); code != http.StatusOK {
			t.Fatalf("%s is not served to its own assistant: %d", rel, code)
		}
	}
	for _, rel := range []string{
		assistant.FilesDirName + "/theirs.txt",
		"user-upload/pic.txt",
	} {
		if code := get(mine, rel); code != http.StatusNotFound {
			t.Fatalf("%s was served under another assistant's address: %d", rel, code)
		}
		if code := get(theirs, rel); code != http.StatusOK {
			t.Fatalf("%s is not served to the assistant it belongs to: %d", rel, code)
		}
	}
}
