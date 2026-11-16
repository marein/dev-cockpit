package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/notify"
)

const slackTool = "mcp__claude_ai_Slack__slack_search_channels"

// askTool posts one tool call the way the tool approval server does, as the
// fixture's assistant over the local socket, and hands the answer over once
// the request returns.
func (f *composeFixture) askTool(ctx context.Context, t *testing.T, body map[string]any) <-chan *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(context.WithValue(ctx, localCallKey, true), http.MethodPost, toolApprovalsPath, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(localapi.AssistantHeader, f.owner)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		done <- rec
	}()
	return done
}

func toolAnswer(t *testing.T, done <-chan *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("the tool approval answered %d: %s", rec.Code, rec.Body.String())
		}
		return decodeJSON(t, rec)
	case <-time.After(10 * time.Second):
		t.Fatal("the tool approval never answered")
		return nil
	}
}

func slackCall(id string) map[string]any {
	return map[string]any{"tool": slackTool, "call": id, "input": "{\n  \"query\": \"general\"\n}"}
}

// A turn's tool call stands as an approval naming the assistant, the tool and
// its input, with the box that allows the tool for good and its short wait.
// Approving with the box ticked lets it run and stores the tool, and from then
// on that tool runs at once, for every assistant, without a question. Nothing
// of it lands in the thread.
func TestAToolCallWaitsForTheUserAndCanBeAllowedForGood(t *testing.T) {
	f := newComposeFixture(t)
	done := f.askTool(context.Background(), t, slackCall("toolu_1"))
	q := f.waitQuestion(t)
	want := []askpass.Detail{{Label: "Tool", Value: slackTool, As: askpass.AsMono}, {Label: "Input", Value: "{\n  \"query\": \"general\"\n}", As: askpass.AsCode}}
	if q.Kind != askpass.KindApproval || q.Action != "Use the tool slack_search_channels" || q.Title != "" || q.Icon != "tool" || q.Tone != "" || !slices.Equal(q.Details, want) ||
		q.Remember != "Always allow this tool" || q.WaitMinutes != 5 || q.Assistant != assistant.Name || q.AssistantID != coder.ShortID(f.owner) {
		t.Fatalf("the question reads %+v", q)
	}
	id, _ := askpass.ApprovalID(q.Key)
	f.waitNews(t, notify.ApprovalTarget(id))
	f.decide(t, q, true, true)
	if got := toolAnswer(t, done); got["allow"] != true {
		t.Fatalf("the approved call answered %v", got)
	}
	if tools := AllowedTools(f.s.settings); !slices.Equal(tools, []string{slackTool}) {
		t.Fatalf("the tools allowed for good read %q", tools)
	}
	if got := toolAnswer(t, f.askTool(context.Background(), t, slackCall("toolu_2"))); got["allow"] != true || len(f.broker.Questions()) != 0 {
		t.Fatalf("the allowed tool answered %v with %d questions", got, len(f.broker.Questions()))
	}
	c, _ := f.s.assistants.Get(f.owner)
	for _, m := range c.Messages {
		if m.IsNote() {
			t.Fatalf("a tool approval wrote a note: %+v", m)
		}
	}
}

// A denial lets nothing run and tells the turn so, and remembers nothing even
// with the box ticked. An input the server had to cut says so.
func TestADeniedToolCallTellsTheTurn(t *testing.T) {
	f := newComposeFixture(t)
	call := slackCall("toolu_1")
	call["cut"] = true
	done := f.askTool(context.Background(), t, call)
	q := f.waitQuestion(t)
	if len(q.Details) != 2 || !q.Details[1].Cut {
		t.Fatalf("the question of a cut input reads %+v", q)
	}
	f.decide(t, q, false, true)
	got := toolAnswer(t, done)
	if got["allow"] != false || got["message"] != toolDeclined || len(AllowedTools(f.s.settings)) != 0 {
		t.Fatalf("the denied call answered %v, tools %q", got, AllowedTools(f.s.settings))
	}
}

// The turn ending breaks the request off and takes the question along, and so
// does deleting the assistant. The assistant cannot answer its own question
// over the local socket.
func TestAToolQuestionGoesWithItsTurnAndItsAssistant(t *testing.T) {
	f := newComposeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := f.askTool(ctx, t, slackCall("toolu_1"))
	q := f.waitQuestion(t)
	body, _ := json.Marshal(map[string]any{"key": q.Key, "id": q.ID, "approve": true})
	req := httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(string(body)))
	req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
	req.Header.Set(localapi.AssistantHeader, f.owner)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the assistant answered its own question: %d", rec.Code)
	}
	cancel()
	<-done
	f.waitNoQuestion(t)

	done = f.askTool(context.Background(), t, slackCall("toolu_2"))
	f.waitQuestion(t)
	f.deleteOwner(t)
	if got := toolAnswer(t, done); got["allow"] != false || got["message"] != toolWithdrawn {
		t.Fatalf("the call of a deleted assistant answered %v", got)
	}
	f.waitNoQuestion(t)
}

// Only an assistant asks, and only for a call it names by a tool name and an
// id a coder writes; anything else is refused before a question stands.
func TestAToolCallIsRefusedWithoutItsShape(t *testing.T) {
	f := newComposeFixture(t)
	browser := httptest.NewRequest(http.MethodPost, toolApprovalsPath, strings.NewReader(`{"tool":"Read","call":"toolu_1"}`))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, browser)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a browser asked: %d", rec.Code)
	}
	for _, body := range []map[string]any{
		{"tool": "", "call": "toolu_1"},
		{"tool": "Read\nme", "call": "toolu_1"},
		{"tool": strings.Repeat("a", 201), "call": "toolu_1"},
		{"tool": "Read", "call": ""},
		{"tool": "Read", "call": "toolu 1"},
	} {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, toolApprovalsPath, strings.NewReader(string(raw)))
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		req.Header.Set(localapi.AssistantHeader, f.owner)
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%v answered %d", body, rec.Code)
		}
	}
	if len(f.broker.Questions()) != 0 {
		t.Fatal("a refused call put a question up")
	}
}

// The tools allowed for good stay sorted, once each and only under a tool's
// name; their Remove, which only the browser may press, forgets one, and the
// last one gone takes the key along.
func TestAllowedToolsAreKeptAndForgotten(t *testing.T) {
	f := newComposeFixture(t)
	allowTool(f.s.settings, slackTool)
	allowTool(f.s.settings, "WebFetch")
	allowTool(f.s.settings, "WebFetch")
	allowTool(f.s.settings, "bad name")
	if tools := AllowedTools(f.s.settings); !slices.Equal(tools, []string{"WebFetch", slackTool}) {
		t.Fatalf("the tools read %q", tools)
	}
	if rec := f.localPost(t, assistantApprovalsSettingsPath, f.owner, url.Values{"forget-tool": {"WebFetch"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("the socket removed a tool: %d", rec.Code)
	}
	f.postApprovals(t, url.Values{"forget-tool": {"WebFetch"}})
	if tools := AllowedTools(f.s.settings); !slices.Equal(tools, []string{slackTool}) {
		t.Fatalf("the tools read %q after a remove", tools)
	}
	f.postApprovals(t, url.Values{"forget-tool": {slackTool}})
	if _, ok := f.s.settings.Lookup(allowedToolsKey); ok {
		t.Fatal("the last tool removed left the key")
	}
}

func TestTheShortNameOfATool(t *testing.T) {
	for tool, want := range map[string]string{
		slackTool:  "slack_search_channels",
		"Bash":     "Bash",
		"mcp__x__": "mcp__x__",
		"mcp__a":   "mcp__a",
	} {
		if got := toolShortName(tool); got != want {
			t.Fatalf("%s reads %q, want %q", tool, got, want)
		}
	}
}
