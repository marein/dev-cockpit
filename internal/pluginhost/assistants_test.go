package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/plugin"
)

const (
	knownAssistant = "a1b2c3d4-e5f6-4a1b-8c2d-0123456789ab"
	quietAssistant = "b1b2c3d4-e5f6-4a1b-8c2d-0123456789ab"
)

var assistantTime = time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)

// fakeAssistants records what a plugin asked for and answers one assistant
// and two coders. refuse makes Create make nothing, partial makes it answer
// the assistant beside the error, noCoders makes Coders answer none.
type fakeAssistants struct {
	mu       sync.Mutex
	created  []plugin.AssistantSpec
	queries  []plugin.AssistantQuery
	reads    []plugin.AssistantMessageQuery
	coders   []plugin.AssistantCoderQuery
	refuse   error
	partial  error
	noCoders bool
}

// with runs do under the fake's lock, for a test that reads or sets what a
// handler on another goroutine reads or sets.
func (f *fakeAssistants) with(do func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	do()
}

func (f *fakeAssistants) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created) + len(f.queries) + len(f.reads) + len(f.coders)
}

func (f *fakeAssistants) Create(ctx context.Context, in plugin.AssistantSpec) (plugin.Assistant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, in)
	if f.refuse != nil {
		return plugin.Assistant{}, f.refuse
	}
	return plugin.Assistant{ID: knownAssistant, Name: in.Name, Named: true, CoderID: "claude", CreatedAt: assistantTime, MessageCount: 2, Running: true, URL: "/assistants/" + knownAssistant}, f.partial
}

func (f *fakeAssistants) List(ctx context.Context, in plugin.AssistantQuery) (plugin.AssistantList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, in)
	if in.Contains == "nothing" {
		return plugin.AssistantList{}, nil
	}
	return plugin.AssistantList{Assistants: []plugin.Assistant{{ID: knownAssistant, Name: "Release", CoderID: "claude", Model: "opus", CreatedAt: assistantTime, LastMessageAt: assistantTime, MessageCount: 2, Preview: "AIRPORT", URL: "/assistants/" + knownAssistant}}}, nil
}

func (f *fakeAssistants) LastMessage(ctx context.Context, in plugin.AssistantMessageQuery) (plugin.AssistantMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, in)
	switch in.AssistantID {
	case knownAssistant:
		return plugin.AssistantMessage{ID: "m2", Role: plugin.AssistantMessageRoleAssistant, Text: "AIRPORT", Time: assistantTime, State: plugin.AssistantMessageStateComplete, Model: "opus"}, nil
	case quietAssistant:
		return plugin.AssistantMessage{}, nil
	}
	return plugin.AssistantMessage{}, plugin.ErrAssistantNotFound
}

func (f *fakeAssistants) Coders(ctx context.Context, in plugin.AssistantCoderQuery) (plugin.AssistantCoderList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.coders = append(f.coders, in)
	if f.noCoders {
		return plugin.AssistantCoderList{}, nil
	}
	return plugin.AssistantCoderList{Coders: []plugin.AssistantCoder{{ID: "claude", Name: "Claude", Default: true}, {ID: "copilot", Name: "Copilot"}}}, nil
}

// A compiled plugin reaches the assistants through its Serve once they
// stand: during ConfigureServe at serve start and in a goroutine it starts
// they answer ErrAssistantsNotReady, from a handler later every call passes
// through as it was made, answers and errors included.
func TestACompiledPluginReachesTheAssistantsOnceTheyStand(t *testing.T) {
	ctx := context.Background()
	var kept plugin.Serve
	var early, earlyCoders error
	started := make(chan error, 1)
	assistants := &Assistants{}
	pairs := []plugin.Named[plugin.ServePlugin]{{ID: "asker", Plugin: serveFunc(func(s plugin.Serve) error {
		kept = s
		_, early = s.Assistants().List(ctx, plugin.AssistantQuery{})
		_, earlyCoders = s.Assistants().Coders(ctx, plugin.AssistantCoderQuery{})
		go func() {
			_, err := s.Assistants().List(ctx, plugin.AssistantQuery{})
			started <- err
		}()
		return nil
	})}}
	if _, err := ConfigureServe(pairs, "", t.TempDir(), nil, nil, assistants); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(early, plugin.ErrAssistantsNotReady) || !errors.Is(earlyCoders, plugin.ErrAssistantsNotReady) {
		t.Fatalf("a call during ConfigureServe answered %v and %v", early, earlyCoders)
	}
	if err := <-started; !errors.Is(err, plugin.ErrAssistantsNotReady) {
		t.Fatalf("a goroutine of ConfigureServe calling at once got %v", err)
	}
	fake := &fakeAssistants{}
	assistants.Bind(fake)

	in := plugin.AssistantSpec{Name: "Release", Message: "Write the notes", CoderID: "claude"}
	made, err := kept.Assistants().Create(ctx, in)
	if err != nil || made.ID != knownAssistant || !made.Running || made.URL != "/assistants/"+knownAssistant {
		t.Fatalf("Create() = %+v, %v", made, err)
	}
	if len(fake.created) != 1 || !reflect.DeepEqual(fake.created[0], in) {
		t.Fatalf("Create() handed on %+v", fake.created)
	}
	list, err := kept.Assistants().List(ctx, plugin.AssistantQuery{Contains: "rel"})
	if err != nil || len(list.Assistants) != 1 || list.Assistants[0].Preview != "AIRPORT" || fake.queries[0].Contains != "rel" {
		t.Fatalf("List() = %+v, %v after %+v", list, err, fake.queries)
	}
	last, err := kept.Assistants().LastMessage(ctx, plugin.AssistantMessageQuery{AssistantID: knownAssistant})
	if err != nil || last.Role != plugin.AssistantMessageRoleAssistant || last.Text != "AIRPORT" || !last.Time.Equal(assistantTime) {
		t.Fatalf("LastMessage() = %+v, %v", last, err)
	}
	if _, err := kept.Assistants().LastMessage(ctx, plugin.AssistantMessageQuery{AssistantID: "gone"}); !errors.Is(err, plugin.ErrAssistantNotFound) {
		t.Fatalf("a missing assistant answered %v", err)
	}
	coders, err := kept.Assistants().Coders(ctx, plugin.AssistantCoderQuery{})
	if err != nil || len(coders.Coders) != 2 || coders.Coders[0].ID != "claude" || coders.Coders[0].Name != "Claude" || !coders.Coders[0].Default || coders.Coders[1].Default || len(fake.coders) != 1 {
		t.Fatalf("Coders() = %+v, %v", coders, err)
	}
	fake.partial = errors.New("The coder is not logged in.")
	made, err = kept.Assistants().Create(ctx, in)
	if made.ID != knownAssistant || err == nil {
		t.Fatalf("a first message that failed answered %+v, %v", made, err)
	}
}

// An optional plugin that is switched off reaches no assistant, even through
// a Serve it kept from when it was on.
func TestAnOptionalPluginReachesNoAssistantsWhileOff(t *testing.T) {
	ctx := context.Background()
	lamp := &lampPlugin{}
	oh := newOptionalHost(t, t.TempDir(), lamp)
	fake := &fakeAssistants{}
	oh.assistants.Bind(fake)
	oh.start()
	if err := oh.SetEnabled("lamp", true); err != nil {
		t.Fatal(err)
	}
	kept := lamp.last()
	if list, err := kept.Assistants().List(ctx, plugin.AssistantQuery{}); err != nil || len(list.Assistants) != 1 {
		t.Fatalf("switched on, List() = %+v, %v", list, err)
	}
	if coders, err := kept.Assistants().Coders(ctx, plugin.AssistantCoderQuery{}); err != nil || len(coders.Coders) != 2 {
		t.Fatalf("switched on, Coders() = %+v, %v", coders, err)
	}
	if err := oh.SetEnabled("lamp", false); err != nil {
		t.Fatal(err)
	}
	before := fake.calls()
	_, createErr := kept.Assistants().Create(ctx, plugin.AssistantSpec{Message: "hi"})
	_, listErr := kept.Assistants().List(ctx, plugin.AssistantQuery{})
	_, readErr := kept.Assistants().LastMessage(ctx, plugin.AssistantMessageQuery{AssistantID: knownAssistant})
	_, codersErr := kept.Assistants().Coders(ctx, plugin.AssistantCoderQuery{})
	for _, err := range []error{createErr, listErr, readErr, codersErr} {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("switched off, a call answered %v", err)
		}
	}
	if fake.calls() != before {
		t.Fatal("a call of a switched off plugin reached the assistants")
	}
}

// An external plugin reaches the assistants on its callback socket with its
// token: the inputs arrive as sent, the answers carry the Go fields' JSON
// names, a refusal is a clean status with the cockpit's whole sentence, and a
// field or parameter this cockpit does not know is refused.
func TestTheCallbackServesTheAssistants(t *testing.T) {
	h := newTestHost(t)
	writePlugin(t, h.Dir(), "demo", "ok", nil)
	h.start()
	waitState(t, h, "demo", StateRunning)
	s := NewSet(nil, h.Host).Lookup("demo")
	call := func(method, path, body string) (int, map[string]any) {
		q := url.Values{"method": {method}, "path": {path}, "body": {body}}
		rec := proxied(t, s, httptest.NewRequest(http.MethodGet, "/plugins/demo/call?"+q.Encode(), nil))
		var answer map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &answer)
		return rec.Code, answer
	}
	for _, path := range []string{"/v1/assistants", "/v1/assistants/coders"} {
		if code, answer := call("GET", path, ""); code != http.StatusServiceUnavailable || answer["error"] != plugin.ErrAssistantsNotReady.Error() {
			t.Fatalf("an unbound %s answered %d %v", path, code, answer)
		}
	}
	fake := &fakeAssistants{}
	h.assistants.Bind(fake)

	code, answer := call("POST", "/v1/assistants", `{"name":"Release","message":"Write the notes","coder_id":"claude"}`)
	made, _ := answer["assistant"].(map[string]any)
	if code != http.StatusCreated || len(answer) != 1 || made["id"] != knownAssistant || made["name"] != "Release" || made["coder_id"] != "claude" || made["running"] != true || made["url"] != "/assistants/"+knownAssistant || made["created_at"] != "2026-11-17T09:30:00Z" {
		t.Fatalf("create answered %d %v", code, answer)
	}
	var created []plugin.AssistantSpec
	fake.with(func() { created = fake.created })
	if want := (plugin.AssistantSpec{Name: "Release", Message: "Write the notes", CoderID: "claude"}); len(created) != 1 || !reflect.DeepEqual(created[0], want) {
		t.Fatalf("create handed on %+v", created)
	}
	if _, ok := made["last_message_at"]; ok {
		t.Fatalf("a zero time was sent: %v", made)
	}
	if code, answer := call("POST", "/v1/assistants", `{"name":"x","prompt":"typo"}`); code != http.StatusBadRequest || fake.calls() != 1 {
		t.Fatalf("an unknown field answered %d %v", code, answer)
	}
	long := "That message is too long. Keep it under 32 KB, the cockpit's own sentence stands whole."
	fake.with(func() { fake.partial = errors.New(long) })
	if code, answer := call("POST", "/v1/assistants", `{"message":"hi"}`); code != http.StatusCreated || answer["assistant"].(map[string]any)["id"] != knownAssistant || answer["error"] != long {
		t.Fatalf("a first message that failed answered %d %v", code, answer)
	}
	fake.with(func() { fake.refuse = errors.New(long) })
	if code, answer := call("POST", "/v1/assistants", `{"coder_id":"nobody"}`); code != http.StatusBadRequest || answer["error"] != long || answer["assistant"] != nil {
		t.Fatalf("a refused create answered %d %v", code, answer)
	}
	fake.with(func() { fake.refuse = errors.New(strings.Repeat("é", maxErrorBytes)) })
	if code, answer := call("POST", "/v1/assistants", `{}`); code != http.StatusBadRequest || len(answer["error"].(string)) > maxErrorBytes+len("…") || !strings.HasSuffix(answer["error"].(string), "é…") {
		t.Fatalf("an endless refusal answered %d with %d bytes", code, len(answer["error"].(string)))
	}

	code, listed := call("GET", "/v1/assistants?contains=rel", "")
	rows, _ := listed["assistants"].([]any)
	var queries []plugin.AssistantQuery
	fake.with(func() { queries = fake.queries })
	if code != http.StatusOK || len(listed) != 1 || len(rows) != 1 || queries[0].Contains != "rel" {
		t.Fatalf("list answered %d %v after %+v", code, listed, queries)
	}
	if row := rows[0].(map[string]any); row["preview"] != "AIRPORT" || row["model"] != "opus" || row["message_count"] != float64(2) || row["last_message_at"] != "2026-11-17T09:30:00Z" {
		t.Fatalf("a listed assistant reads %v", row)
	}
	if code, listed := call("GET", "/v1/assistants?contains=nothing", ""); code != http.StatusOK || listed["assistants"] == nil || len(listed["assistants"].([]any)) != 0 {
		t.Fatalf("an empty list answered %d %v", code, listed)
	}
	before := fake.calls()
	for _, query := range []string{"?coder_id=claude", "?contains=a&bogus=1", "?contains=a&contains=b", "?_=1"} {
		if code, answer := call("GET", "/v1/assistants"+query, ""); code != http.StatusBadRequest || answer["error"] == nil {
			t.Fatalf("the list with %s answered %d %v", query, code, answer)
		}
	}

	code, last := call("GET", "/v1/assistants/"+knownAssistant+"/last-message", "")
	var reads []plugin.AssistantMessageQuery
	fake.with(func() { reads = fake.reads })
	if code != http.StatusOK || last["role"] != "assistant" || last["text"] != "AIRPORT" || last["state"] != "complete" || last["time"] != "2026-11-17T09:30:00Z" || reads[0].AssistantID != knownAssistant {
		t.Fatalf("last message answered %d %v", code, last)
	}
	if code, empty := call("GET", "/v1/assistants/"+quietAssistant+"/last-message", ""); code != http.StatusOK || empty["id"] != "" || empty["time"] != nil || empty["model"] != nil {
		t.Fatalf("an assistant without messages answered %d %v, want an empty id and the empty optional fields left out", code, empty)
	}
	if code, answer := call("GET", "/v1/assistants/gone/last-message", ""); code != http.StatusNotFound || answer["error"] != plugin.ErrAssistantNotFound.Error() {
		t.Fatalf("a missing assistant answered %d %v", code, answer)
	}
	for _, query := range []string{"?role=user", "?assistant_id=" + quietAssistant} {
		if code, answer := call("GET", "/v1/assistants/"+knownAssistant+"/last-message"+query, ""); code != http.StatusBadRequest || answer["error"] == nil {
			t.Fatalf("the last message with %s answered %d %v", query, code, answer)
		}
	}
	if after := fake.calls(); after != before+3 {
		t.Fatalf("a refused query reached the assistants: %d calls, want %d", after, before+3)
	}

	code, coders := call("GET", "/v1/assistants/coders", "")
	want := map[string]any{"coders": []any{
		map[string]any{"id": "claude", "name": "Claude", "default": true},
		map[string]any{"id": "copilot", "name": "Copilot", "default": false},
	}}
	if code != http.StatusOK || !reflect.DeepEqual(coders, want) {
		t.Fatalf("the coders answered %d %v, want %v", code, coders, want)
	}
	fake.with(func() { fake.noCoders = true })
	if code, coders := call("GET", "/v1/assistants/coders", ""); code != http.StatusOK || coders["coders"] == nil || len(coders["coders"].([]any)) != 0 {
		t.Fatalf("no coders answered %d %v", code, coders)
	}
	before = fake.calls()
	for _, query := range []string{"?contains=claude", "?default=true", "?_=1"} {
		if code, answer := call("GET", "/v1/assistants/coders"+query, ""); code != http.StatusBadRequest || answer["error"] == nil {
			t.Fatalf("the coders with %s answered %d %v", query, code, answer)
		}
	}
	if after := fake.calls(); after != before {
		t.Fatalf("a refused coder query reached the assistants: %d calls, want %d", after, before)
	}
}
