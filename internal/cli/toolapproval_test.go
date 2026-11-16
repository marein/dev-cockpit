package cli

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/localapi"
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
// its input shown indented, and the input goes back to the coder as it came.
// A denial carries the cockpit's sentence, and a cockpit that cannot be asked
// denies the call. An input past what travels goes cut on a character and says
// so, the coder gets it back whole.
func TestAToolCallIsAskedOfTheCockpit(t *testing.T) {
	var got map[string]any
	var as string
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		as = r.Header.Get(localapi.AssistantHeader)
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		if got["tool"] == "Denied" {
			_ = json.NewEncoder(w).Encode(map[string]any{"allow": false, "message": "The user denied it."})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"allow": r.URL.Path == "/assistants/tool-approvals"})
	})
	opts := inspectOptions{stateDir: dir, assistantID: "bot"}
	input := json.RawMessage(`{"search":"bot"}`)
	v := askToolApproval(opts, toolCall{Name: "mcp__x__list", Input: input, ID: "toolu_1"})
	if v.Behavior != "allow" || string(v.UpdatedInput) != string(input) {
		t.Fatalf("want the call allowed with its input, got %+v", v)
	}
	if as != "bot" || got["tool"] != "mcp__x__list" || got["call"] != "toolu_1" || got["cut"] != false || got["input"] != "{\n  \"search\": \"bot\"\n}" {
		t.Fatalf("the cockpit was asked %v as %q", got, as)
	}
	big := json.RawMessage(`{"content":"` + strings.Repeat("ü", toolInputBytes) + `"}`)
	if v := askToolApproval(opts, toolCall{Name: "Write", Input: big, ID: "toolu_2"}); v.Behavior != "allow" || string(v.UpdatedInput) != string(big) {
		t.Fatalf("want a long input allowed whole, got %s", v.Behavior)
	}
	if shown, _ := got["input"].(string); got["cut"] != true || len(shown) > toolInputBytes || !utf8.ValidString(shown) {
		t.Fatalf("want the input cut on a character within %d bytes, got %d bytes, cut %v", toolInputBytes, len(shown), got["cut"])
	}
	v = askToolApproval(opts, toolCall{Name: "Denied"})
	if v.Behavior != "deny" || v.Message != "The user denied it." || got["call"] == "" || got["input"] != "{}" {
		t.Fatalf("want the denial with its sentence, got %+v after %v", v, got)
	}
	t.Setenv("DEV_COCKPIT_DIAL_BUDGET", "0s")
	v = askToolApproval(inspectOptions{stateDir: t.TempDir(), assistantID: "bot"}, toolCall{Name: "Read"})
	if v.Behavior != "deny" || !strings.HasPrefix(v.Message, "The cockpit could not ask the user about this tool call") {
		t.Fatalf("want a cockpit that is not there to deny, got %+v", v)
	}
}
