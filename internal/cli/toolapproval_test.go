package cli

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/notify"
)

// mcpSession runs the server over pipes, the way a coder starts it.
type mcpSession struct {
	t      *testing.T
	in     *io.PipeWriter
	out    *bufio.Scanner
	closed chan error
}

func startMCP(t *testing.T, ask func(toolCall) toolVerdict) *mcpSession {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &mcpSession{t: t, in: inW, out: bufio.NewScanner(outR), closed: make(chan error, 1)}
	go func() {
		s.closed <- serveToolApprovals(inR, outW, ask)
		_ = outW.Close()
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return s
}

func (s *mcpSession) send(line string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		s.t.Fatal(err)
	}
}

func (s *mcpSession) read() map[string]any {
	s.t.Helper()
	if !s.out.Scan() {
		s.t.Fatalf("the server wrote nothing more: %v", s.out.Err())
	}
	var msg map[string]any
	if err := json.Unmarshal(s.out.Bytes(), &msg); err != nil {
		s.t.Fatalf("the server wrote %q: %v", s.out.Text(), err)
	}
	return msg
}

// The handshake answers the version the coder speaks, a notification is
// answered by nothing, the one tool is listed, and a method the server does
// not speak is refused as JSON-RPC refuses it.
func TestTheToolApprovalServerSpeaksTheHandshake(t *testing.T) {
	s := startMCP(t, func(toolCall) toolVerdict { return toolVerdict{Behavior: "allow"} })
	s.send(`{"jsonrpc":"2.0","id":"probe","method":"server/discover","params":{}}`)
	if msg := s.read(); msg["id"] != "probe" || msg["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("want an unknown method refused, got %v", msg)
	}
	s.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{}}}`)
	msg := s.read()
	result := msg["result"].(map[string]any)
	if msg["id"] != float64(0) || result["protocolVersion"] != "2025-11-25" || result["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("want the handshake answered, got %v", msg)
	}
	s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	s.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := s.read()["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "approve" {
		t.Fatalf("want the one tool, got %v", tools)
	}
	s.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"other","arguments":{}}}`)
	if msg := s.read(); msg["id"] != float64(2) || msg["error"] == nil {
		t.Fatalf("want another tool refused, got %v", msg)
	}
	_ = s.in.Close()
	select {
	case err := <-s.closed:
		if err != nil {
			t.Fatalf("the server ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing stdin did not end the server")
	}
}

// Each call is asked on its own: a call answered quickly is not held behind
// one that waits, and each answer is the verdict as the coder reads it.
func TestTheToolApprovalServerAsksEachCallOnItsOwn(t *testing.T) {
	release := make(chan struct{})
	s := startMCP(t, func(call toolCall) toolVerdict {
		if call.Name == "Slow" {
			<-release
			return toolVerdict{Behavior: "deny", Message: "no"}
		}
		return toolVerdict{Behavior: "allow", UpdatedInput: call.Input}
	})
	s.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"approve","arguments":{"tool_name":"Slow","input":{},"tool_use_id":"toolu_1"}}}`)
	s.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"approve","arguments":{"tool_name":"Fast","input":{"q":"bot"},"tool_use_id":"toolu_2"}}}`)
	verdict := func(msg map[string]any) map[string]any {
		text := msg["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
		var v map[string]any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			t.Fatalf("the verdict reads %q", text)
		}
		return v
	}
	first := s.read()
	if v := verdict(first); first["id"] != float64(2) || v["behavior"] != "allow" || v["updatedInput"].(map[string]any)["q"] != "bot" {
		t.Fatalf("want the fast call answered first with its input, got %v", first)
	}
	close(release)
	second := s.read()
	if v := verdict(second); second["id"] != float64(1) || v["behavior"] != "deny" || v["message"] != "no" {
		t.Fatalf("want the slow call denied, got %v", second)
	}
}

// A call goes to the running cockpit as the assistant whose turn it is, with
// each key of its input shown on its own as it will run, and the input goes
// back to the coder as it came. A denial carries the cockpit's sentence, a
// refusal says why, and a cockpit that cannot be asked denies the call. A
// value past what the dialog shows goes cut on a character and says so, the
// coder gets it back whole.
func TestAToolCallIsAskedOfTheCockpit(t *testing.T) {
	var got struct {
		Tool  string                `json:"tool"`
		Call  string                `json:"call"`
		Input []assistant.ToolField `json:"input"`
	}
	var as string
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		as = r.Header.Get(localapi.AssistantHeader)
		got.Input = nil
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		switch got.Tool {
		case "Denied":
			_ = json.NewEncoder(w).Encode(map[string]any{"allow": false, "message": "The user denied it."})
		case "Refused":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "The tool name is not one a coder calls."})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"allow": r.URL.Path == "/assistants/tool-approvals"})
		}
	})
	opts := inspectOptions{stateDir: dir, assistantID: "bot"}
	input := json.RawMessage(`{"command":"make && ls > out 2>&1 <done>","description":"<@user>"}`)
	v := askToolApproval(opts, toolCall{Name: "Bash", Input: input, ID: "toolu_1"})
	if v.Behavior != "allow" || string(v.UpdatedInput) != string(input) {
		t.Fatalf("want the call allowed with its input, got %+v", v)
	}
	want := []assistant.ToolField{{Key: "command", Value: "make && ls > out 2>&1 <done>"}, {Key: "description", Value: "<@user>"}}
	if as != "bot" || got.Tool != "Bash" || got.Call != "toolu_1" || !slices.Equal(got.Input, want) {
		t.Fatalf("the cockpit was asked %+v as %q", got, as)
	}
	big := json.RawMessage(`{"file_path":"/tmp/a","content":"` + strings.Repeat("ü", 1<<19) + `"}`)
	if v := askToolApproval(opts, toolCall{Name: "Write", Input: big, ID: "toolu_2"}); v.Behavior != "allow" || string(v.UpdatedInput) != string(big) {
		t.Fatalf("want a long input allowed whole, got %s", v.Behavior)
	}
	if len(got.Input) != 2 || got.Input[0] != (assistant.ToolField{Key: "file_path", Value: "/tmp/a"}) || !got.Input[1].Cut ||
		len(got.Input[1].Value) > approval.MaxDetailBytes || !utf8.ValidString(got.Input[1].Value) {
		t.Fatalf("want the content cut on a character within %d bytes, got %d bytes, cut %v", approval.MaxDetailBytes, len(got.Input[1].Value), got.Input[1].Cut)
	}
	v = askToolApproval(opts, toolCall{Name: "Denied"})
	if v.Behavior != "deny" || v.Message != "The user denied it." || got.Call == "" || got.Input == nil || len(got.Input) != 0 {
		t.Fatalf("want the denial with its sentence, got %+v after %+v", v, got)
	}
	v = askToolApproval(opts, toolCall{Name: "Refused"})
	if v.Behavior != "deny" || v.Message != toolRefused+"The tool name is not one a coder calls." {
		t.Fatalf("want a refusal to say why, got %+v", v)
	}
	t.Setenv("DEV_COCKPIT_DIAL_BUDGET", "0s")
	v = askToolApproval(inspectOptions{stateDir: t.TempDir(), assistantID: "bot"}, toolCall{Name: "Read"})
	if v.Behavior != "deny" || v.Message != toolNoCockpit {
		t.Fatalf("want a cockpit that is not there to deny, got %+v", v)
	}
}

// A cockpit that goes away while the user is asked, a restart among them,
// denies the call with one sentence the model can act on, never the error of
// the broken request.
func TestACockpitGoneDuringTheQuestionDeniesTheCall(t *testing.T) {
	asked := make(chan struct{}, 1)
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		asked <- struct{}{}
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	})
	v := askToolApproval(inspectOptions{stateDir: dir, assistantID: "bot"}, toolCall{Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`), ID: "toolu_1"})
	<-asked
	if v.Behavior != "deny" || v.Message != toolNoAnswer || strings.Contains(v.Message, "Post ") || strings.Contains(v.Message, "EOF") {
		t.Fatalf("want the lost question denied with one sentence, got %+v", v)
	}
}

// The news a tool question rings with, and the push made of it, name who asks
// and the tool, never the input: markup in it reaches no channel at all.
func TestAToolQuestionRingsWithoutItsInput(t *testing.T) {
	broker := askpass.New(t.TempDir())
	hostile := `<script>alert(1)</script><img src=x onerror=alert(2)>`
	bridge := broker.BeginApproval(askpass.ApprovalKey("a1"), askpass.Question{Assistant: "Ops", AssistantID: "1a2b3c4d", Action: "Use the tool Bash",
		Details: []askpass.Detail{{Label: "Tool", Value: "Bash", As: askpass.AsMono}, {Label: "command", Value: hostile, As: askpass.AsCode}}})
	go bridge.AskApproval()
	t.Cleanup(bridge.End)
	for deadline := time.Now().Add(5 * time.Second); len(broker.Questions()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the question never stood")
		}
	}
	info := notifyResolver(nil, nil, nil, nil, nil, nil, broker)(notify.ApprovalTarget("a1"))
	if info.Title != "Assistant asks approval." || info.Detail != "Ops · 1a2b3c4d: Use the tool Bash" {
		t.Fatalf("the news reads %q %q", info.Title, info.Detail)
	}
}

// The hook answers one call the way a PreToolUse hook does: nothing for a
// call that may run, a deny with the sentence the model reads otherwise, and
// a deny for whatever it could not read.
func TestTheToolApprovalHookAnswersOneCall(t *testing.T) {
	var asked []toolCall
	verdict := toolVerdict{Behavior: "allow"}
	ask := func(call toolCall) toolVerdict { asked = append(asked, call); return verdict }
	input := `{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"mcp__probe__lookup","tool_input":{"word":"apple"},"tool_use_id":"exec-10d0e158"}`

	var out strings.Builder
	if err := answerToolHook(strings.NewReader(input), &out, ask); err != nil || out.Len() != 0 {
		t.Fatalf("an allowed call prints nothing: %q, %v", out.String(), err)
	}
	if len(asked) != 1 || asked[0].Name != "mcp__probe__lookup" || string(asked[0].Input) != `{"word":"apple"}` || asked[0].ID != "exec-10d0e158" {
		t.Fatalf("asked = %+v", asked)
	}

	verdict = toolVerdict{Behavior: "deny", Message: "The user said <no>."}
	if err := answerToolHook(strings.NewReader(input), &out, ask); err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Output struct {
			Event    string `json:"hookEventName"`
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out.String()), &answer); err != nil {
		t.Fatalf("%v in %q", err, out.String())
	}
	if answer.Output.Event != "PreToolUse" || answer.Output.Decision != "deny" || answer.Output.Reason != "The user said <no>." {
		t.Fatalf("answer = %+v", answer)
	}

	for _, unreadable := range []string{"", "not json", `{"tool_input":{}}`} {
		out.Reset()
		asked = nil
		if err := answerToolHook(strings.NewReader(unreadable), &out, ask); err != nil || !strings.Contains(out.String(), `"deny"`) || len(asked) != 0 {
			t.Fatalf("%q must be denied unasked: %q, %v", unreadable, out.String(), err)
		}
	}
}
