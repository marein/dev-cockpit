package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/statefile"
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
	return map[string]any{"tool": slackTool, "call": id, "input": []assistant.ToolField{{Key: "query", Value: "general"}}}
}

// A turn's tool call stands as an approval naming the assistant, the tool and
// its input, with the box that allows the tool for good and its short wait.
// Approving with the box ticked lets it run and stores the tool, and from then
// on that tool runs at once, for every Claude assistant, without a question.
// Nothing of it lands in the thread.
func TestAToolCallWaitsForTheUserAndCanBeAllowedForGood(t *testing.T) {
	f := newComposeFixture(t)
	done := f.askTool(context.Background(), t, slackCall("toolu_1"))
	q := f.waitQuestion(t)
	want := []askpass.Detail{{Label: "Tool", Value: slackTool, As: askpass.AsMono}, {Label: "query", Value: "general", As: askpass.AsCode}}
	if q.Kind != askpass.KindApproval || q.Action != "Use slack_search_channels" || q.Title != "" || q.Icon != "tool" || q.Tone != "" || !slices.Equal(q.Details, want) ||
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
// with the box ticked. A value the server had to cut says so.
func TestADeniedToolCallTellsTheTurn(t *testing.T) {
	f := newComposeFixture(t)
	call := slackCall("toolu_1")
	call["input"] = []assistant.ToolField{{Key: "query", Value: "general", Cut: true}}
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
		{"tool": "Read", "call": "toolu_1", "input": "{}"},
		{"tool": "Read", "call": "toolu_1", "input": []assistant.ToolField{{Key: "a\nb"}}},
		{"tool": "Read", "call": "toolu_1", "input": []assistant.ToolField{{Key: "a", Value: strings.Repeat("x", approval.MaxDetailBytes+1)}}},
		{"tool": "Read", "call": "toolu_1", "input": []assistant.ToolField{{Key: "a", Value: strings.Repeat("x", assistant.ToolApprovalBody)}}},
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

// An input shows as it will run: each key in a box of its own under its key,
// a multi line value with its line breaks, nothing escaped for a page on the
// way, in the question and in the approval as it stands on disk. The question's
// line names the tool alone, so the news and the push carry none of the input.
func TestAToolInputStandsAsItWillRun(t *testing.T) {
	f := newComposeFixture(t)
	input := json.RawMessage(`{"command":"make && ls > out 2>&1 <done> <@user>","description":"<script>alert(1)</script>","content":"<img src=x onerror=alert(2)>\nline two & three\n"}`)
	done := f.askTool(context.Background(), t, map[string]any{"tool": "Bash", "call": "toolu_1", "input": assistant.ToolFields(input)})
	q := f.waitQuestion(t)
	want := []askpass.Detail{
		{Label: "Tool", Value: "Bash", As: askpass.AsMono},
		{Label: "command", Value: "make && ls > out 2>&1 <done> <@user>", As: askpass.AsCode},
		{Label: "description", Value: "<script>alert(1)</script>", As: askpass.AsCode},
		{Label: "content", Value: "<img src=x onerror=alert(2)>\nline two & three\n", As: askpass.AsCode},
	}
	if q.Action != "Use Bash" || !slices.Equal(q.Details, want) {
		t.Fatalf("the question reads %q %+v", q.Action, q.Details)
	}
	stored := map[string]approval.Approval{}
	statefile.Load(filepath.Join(f.stateDir, "approvals.json"), &stored)
	if len(stored) != 1 {
		t.Fatalf("%d approvals stand on disk", len(stored))
	}
	for _, a := range stored {
		if a.What != "Use Bash" || !slices.Equal(a.Details, want) {
			t.Fatalf("the approval on disk reads %q %+v", a.What, a.Details)
		}
	}
	f.decide(t, q, false, false)
	toolAnswer(t, done)
}

// An input of any size and density stands as a question, never as a refusal
// that would deny it unseen: each value past the dialog's bound shows its
// start and says it was cut.
func TestALargeToolInputStandsAsAQuestion(t *testing.T) {
	f := newComposeFixture(t)
	var dense strings.Builder
	dense.WriteString("[")
	for i := 0; dense.Len() < 98_000; i++ {
		fmt.Fprintf(&dense, `{"id":%d,"q":"\"<&>\"","t":["a","b"]},`, i)
	}
	minified := strings.TrimSuffix(dense.String(), ",") + "]"
	write := func(content string) json.RawMessage {
		raw, _ := json.Marshal(map[string]string{"file_path": "/tmp/data.json", "content": content})
		return raw
	}
	for name, input := range map[string]json.RawMessage{
		"98 KB of minified JSON written":  write(minified),
		"98 KB of minified JSON as input": json.RawMessage(`{"rows":` + minified + `}`),
		"1 MB of minified JSON written":   write(strings.Repeat(minified, 11)),
		"1 MB of control characters":      write(strings.Repeat("\x01\x1f", 1<<19)),
		"1 MB of markup":                  write(strings.Repeat("<&>", 350_000)),
	} {
		fields := assistant.ToolFields(input)
		if body, _ := json.Marshal(map[string]any{"tool": "Write", "call": "toolu_1", "input": fields}); len(body) > assistant.ToolApprovalBody {
			t.Fatalf("%s takes %d bytes, the server reads %d", name, len(body), assistant.ToolApprovalBody)
		}
		done := f.askTool(context.Background(), t, map[string]any{"tool": "Write", "call": "toolu_1", "input": fields})
		q := f.waitQuestion(t)
		last := q.Details[len(q.Details)-1]
		if !slices.ContainsFunc(q.Details, func(d askpass.Detail) bool { return d.Cut }) || len(last.Value) > approval.MaxDetailBytes {
			t.Fatalf("%s stands as %d details, the last of %d bytes, cut %v", name, len(q.Details), len(last.Value), last.Cut)
		}
		f.decide(t, q, false, false)
		toolAnswer(t, done)
		f.waitNoQuestion(t)
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
	req := httptest.NewRequest(http.MethodPost, assistantApprovalsSettingsPath, strings.NewReader(url.Values{"forget-tool": {slackTool}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if got := decodeJSON(t, rec); rec.Code != http.StatusOK || got["message"] != toolRemoved {
		t.Fatalf("the list's remove answered %d %v", rec.Code, got)
	}
	if _, ok := f.s.settings.Lookup(allowedToolsKey); ok {
		t.Fatal("the last tool removed left the key")
	}
}

func TestTheServerAndNameOfATool(t *testing.T) {
	for tool, want := range map[string][2]string{
		slackTool:               {"claude_ai_Slack", "slack_search_channels"},
		"mcp__fake-slack__x__y": {"fake-slack__x", "y"},
		"Bash":                  {"", "Bash"},
		"mcp__x__":              {"", "mcp__x__"},
		"mcp____x":              {"", "mcp____x"},
		"mcp__a":                {"", "mcp__a"},
	} {
		if server, name := toolParts(tool); server != want[0] || name != want[1] {
			t.Fatalf("%s reads %q and %q, want %q", tool, server, name, want)
		}
	}
}
