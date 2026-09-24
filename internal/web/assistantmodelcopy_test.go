package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/settings"
)

// storeCoders is the one coder of these tests reading its model defaults out
// of a settings store the way runServe wires CoderInfo.Defaults, so a default
// stored on the Models tab reaches a create here the way it does on a server.
type storeCoders struct{ store *settings.Store }

func (c storeCoders) Available() []assistant.CoderInfo {
	return []assistant.CoderInfo{{ID: "claude", Label: "Claude", Defaults: func() assistant.ModelDefaults {
		return assistant.ModelDefaultsFor(c.store, "claude")
	}}}
}

// The three defaults of the Models tab are creation defaults for assistants
// alone: an assistant made while they stand carries them as its chat model,
// its check model and its trigger model, and with a default cleared it is
// made without that one. A trigger posted from the page's form on the
// assistant default carries no model of its own whether or not the Trigger
// default stands, one posted with a model keeps its own. The JSON
// `trigger-new` reads names as modelDefault what the reaction would run on
// without the flag: the owner's Triggers pick while one stands, else the
// chat, which here is the copied chat default standing as the pick.
func TestTheTabsCreationDefaultsReachANewAssistantAndNeverATrigger(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	store := settings.New(filepath.Join(stateDir, "settings.json"))
	conversations, _, err := assistant.New(stateDir, storeCoders{store: store}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	s := &Server{
		assistants: conversations,
		settings:   store,
		watcher:    assistant.NewWatcher(conversations, assistant.NewJobs(assistant.NewStore(stateDir)), nil, nil),
	}
	for purpose, name := range map[assistant.ModelPurpose]string{
		assistant.ModelPurposeChat: "haiku", assistant.ModelPurposeCheck: "sonnet", assistant.ModelPurposeTrigger: "opus",
	} {
		store.Set(assistant.ModelDefaultKey("claude", purpose), name)
	}

	made, err := conversations.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if made.Model != "haiku" || made.CheckModel != "sonnet" || made.TriggerModel != "opus" {
		t.Fatalf("want the Chat, Checks and Trigger defaults copied onto the new assistant, got %+v", made.Summary)
	}

	post := func(owner string, form url.Values) map[string]any {
		t.Helper()
		form.Set("form", "new")
		form.Set("assistant", owner)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodPost, "/assistants/triggers", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		c.Request = req
		s.handleAssistantTriggersAction(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("trigger post: %d: %s", rec.Code, rec.Body.String())
		}
		var answer map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
			t.Fatalf("trigger answer: %v: %s", err, rec.Body.String())
		}
		return answer
	}
	stored := func(answer map[string]any) assistant.Trigger {
		t.Helper()
		id, _ := answer["id"].(string)
		trigger, ok := conversations.Events().Get(id)
		if !ok {
			t.Fatalf("the posted trigger %q is not stored", id)
		}
		return trigger
	}

	empty := post(made.ID, url.Values{"event": {"job-done"}, "task": {"summarize"}, "model": {""}})
	if got := stored(empty); got.Model != "" || empty["model"] != "" || empty["modelDefault"] != "opus" {
		t.Fatalf("want a trigger posted on the assistant default to carry none, the copied trigger pick named as what runs, got %q and %v", got.Model, empty)
	}
	own := post(made.ID, url.Values{"event": {"job-done"}, "task": {"summarize"}, "model": {"fable"}})
	if got := stored(own); got.Model != "fable" || own["model"] != "fable" || own["modelDefault"] != "opus" {
		t.Fatalf("want a trigger's own pick kept above the assistant default, got %q and %v", got.Model, own)
	}

	store.Delete(assistant.ModelDefaultKey("claude", assistant.ModelPurposeCheck))
	store.Delete(assistant.ModelDefaultKey("claude", assistant.ModelPurposeTrigger))
	bare, err := conversations.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if bare.Model != "haiku" || bare.CheckModel != "" || bare.TriggerModel != "" {
		t.Fatalf("want an assistant made with the chat default alone to carry that one, got %+v", bare.Summary)
	}
	plain := post(bare.ID, url.Values{"event": {"job-done"}, "task": {"summarize"}})
	if got := stored(plain); got.Model != "" || plain["model"] != "" || plain["modelDefault"] != "haiku" {
		t.Fatalf("want a trigger made under no trigger pick to carry none, the copied chat named as what runs, got %q and %v", got.Model, plain)
	}
	store.Delete(assistant.ModelDefaultKey("claude", assistant.ModelPurposeChat))
	none, err := conversations.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if none.Model != "" || none.CheckModel != "" || none.TriggerModel != "" {
		t.Fatalf("want an assistant made without any default to carry none, got %+v", none.Summary)
	}
	if _, err := conversations.SetModels(bare.ID, assistant.ModelChoice{Trigger: "sonnet", TriggerSet: true}); err != nil {
		t.Fatalf("set the trigger model: %v", err)
	}
	picked := post(bare.ID, url.Values{"event": {"job-done"}, "task": {"summarize"}})
	if got := stored(picked); got.Model != "" || picked["model"] != "" || picked["modelDefault"] != "sonnet" {
		t.Fatalf("want a trigger made under the ring's trigger pick to carry none, that pick named as what runs, got %q and %v", got.Model, picked)
	}
}
