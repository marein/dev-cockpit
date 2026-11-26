package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/statefile"
	"github.com/spf13/cobra"
)

// newToolApprovalServeCommand is the MCP server a turn's coder asks its
// permission prompts through, started by the coder over stdio behind the
// turn's wrapper, see assistant.ToolApprovalServer. It is nobody's command to
// type, so it stays out of the help the assistant reads.
func newToolApprovalServeCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:    assistant.ToolApprovalCommand,
		Short:  "Ask the user about a turn's tool calls over MCP",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ask := func(call toolCall) toolVerdict { return askToolApproval(*opts, call) }
			return serveToolApprovals(cmd.InOrStdin(), cmd.OutOrStdout(), ask)
		},
	}
}

// newToolApprovalAskCommand is the hook a turn's coder runs in front of a
// tool call, started by the coder behind the turn's wrapper, see
// assistant.ToolApprovalHook. Like the server it is nobody's command to type.
func newToolApprovalAskCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:    assistant.ToolApprovalHookCommand,
		Short:  "Ask the user about one tool call of a turn, as its hook",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ask := func(call toolCall) toolVerdict { return askToolApproval(*opts, call) }
			return answerToolHook(cmd.InOrStdin(), cmd.OutOrStdout(), ask)
		},
	}
}

// toolHookCall is one tool call as a coder hands it to a PreToolUse hook.
type toolHookCall struct {
	Name  string          `json:"tool_name"`
	Input json.RawMessage `json:"tool_input"`
	ID    string          `json:"tool_use_id"`
}

const toolHookUnread = "The cockpit could not read this tool call, so it was denied. Do not call it again in this turn; tell the user it needs their approval."

// answerToolHook asks about the one call on in and answers the way a
// PreToolUse hook does: nothing lets the call run, a deny with the sentence
// the model reads keeps it from running. Whatever keeps the call from being
// read or asked denies it.
func answerToolHook(in io.Reader, out io.Writer, ask func(toolCall) toolVerdict) error {
	deny := func(message string) error {
		return json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": message,
		}})
	}
	data, err := io.ReadAll(io.LimitReader(in, maxRPCLine+1))
	var call toolHookCall
	if err != nil || len(data) > maxRPCLine || json.Unmarshal(data, &call) != nil || call.Name == "" {
		return deny(toolHookUnread)
	}
	verdict := ask(toolCall{Name: call.Name, Input: call.Input, ID: call.ID})
	if verdict.Behavior == "allow" {
		return nil
	}
	return deny(verdict.Message)
}

// toolCall is one permission prompt as the coder hands it to the tool.
type toolCall struct {
	Name  string          `json:"tool_name"`
	Input json.RawMessage `json:"input"`
	ID    string          `json:"tool_use_id"`
}

// toolVerdict is what the tool answers the coder: allow with the input as it
// came, or deny with the sentence the model reads.
type toolVerdict struct {
	Behavior     string          `json:"behavior"`
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	Message      string          `json:"message,omitempty"`
}

// toolApprovalSlack is how much longer than the question's wait the request
// may take: the server ends every question itself and answers why, this only
// keeps a request from hanging on a server that went away without closing its
// socket.
const toolApprovalSlack = time.Minute

// The denials the model reads when the cockpit could not be asked or gave no
// answer. A restart breaks the request off with a transport error, which
// would only confuse the model.
const (
	toolNoCockpit = "No running cockpit could ask the user about this tool call, so it was denied. Do not call it again in this turn; tell the user it needs their approval."
	toolNoAnswer  = "The cockpit restarted or stopped answering before the user decided, so this tool call was denied. Do not call it again in this turn; tell the user it needs their approval."
	toolRefused   = "The cockpit could not ask the user about this tool call, so it was denied: "
)

// askToolApproval asks the running cockpit and waits for the user. Whatever
// keeps the question from being asked or answered denies the call, never lets
// it through.
func askToolApproval(opts inspectOptions, call toolCall) toolVerdict {
	input := call.Input
	if len(input) == 0 || string(input) == "null" {
		input = json.RawMessage("{}")
	}
	if call.ID == "" {
		call.ID = statefile.NewID()
	}
	deny := func(message string) toolVerdict { return toolVerdict{Behavior: "deny", Message: message} }
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return deny(toolNoCockpit)
	}
	answer, err := client.PostJSON("/assistants/tool-approvals", map[string]any{
		"tool":  call.Name,
		"call":  call.ID,
		"input": assistant.ToolFields(input),
	}, assistant.ToolApprovalWait+toolApprovalSlack)
	var lost *url.Error
	switch {
	case errors.As(err, &lost):
		return deny(toolNoAnswer)
	case err != nil:
		return deny(toolRefused + err.Error())
	}
	if jsonBool(answer["allow"]) {
		return toolVerdict{Behavior: "allow", UpdatedInput: input}
	}
	message, _ := answer["message"].(string)
	if strings.TrimSpace(message) == "" {
		message = "The tool call was denied."
	}
	return deny(message)
}

// rpcMessage is one JSON-RPC message of MCP over stdio, one per line.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// maxRPCLine bounds one message the coder sends: a call carries the tool's
// whole input, a file a write tool is about to write among them.
const maxRPCLine = 32 << 20

// serveToolApprovals speaks the part of MCP a permission prompt needs: the
// handshake, the one tool and its calls. Each call is asked on its own, so
// two prompts of one turn stand side by side, and the answers go out one
// line at a time. The coder closing stdin ends the server, and with it every
// question still waiting.
func serveToolApprovals(in io.Reader, out io.Writer, ask func(toolCall) toolVerdict) error {
	var mu sync.Mutex
	send := func(msg rpcMessage) {
		msg.JSONRPC = "2.0"
		line, _ := json.Marshal(msg)
		mu.Lock()
		defer mu.Unlock()
		_, _ = out.Write(append(line, '\n'))
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), maxRPCLine)
	for scanner.Scan() {
		var msg rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			send(rpcMessage{ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "Parse error"}})
			continue
		}
		if len(msg.ID) == 0 {
			continue
		}
		switch msg.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			if params.ProtocolVersion == "" {
				params.ProtocolVersion = "2025-06-18"
			}
			send(rpcMessage{ID: msg.ID, Result: map[string]any{
				"protocolVersion": params.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": assistant.ToolApprovalServerName, "version": "1"},
			}})
		case "ping":
			send(rpcMessage{ID: msg.ID, Result: map[string]any{}})
		case "tools/list":
			send(rpcMessage{ID: msg.ID, Result: map[string]any{"tools": []any{toolApprovalTool}}})
		case "tools/call":
			var params struct {
				Name      string   `json:"name"`
				Arguments toolCall `json:"arguments"`
			}
			if err := json.Unmarshal(msg.Params, &params); err != nil || params.Name != assistant.ToolApprovalToolName {
				send(rpcMessage{ID: msg.ID, Error: &rpcError{Code: -32602, Message: "Unknown tool or arguments"}})
				continue
			}
			go func(id json.RawMessage, call toolCall) {
				verdict, _ := json.Marshal(ask(call))
				send(rpcMessage{ID: id, Result: map[string]any{"content": []any{map[string]any{"type": "text", "text": string(verdict)}}}})
			}(msg.ID, params.Arguments)
		default:
			send(rpcMessage{ID: msg.ID, Error: &rpcError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", msg.Method)}})
		}
	}
	return scanner.Err()
}

// toolApprovalTool is the one tool, in the shape a permission prompt calls it.
var toolApprovalTool = map[string]any{
	"name":        assistant.ToolApprovalToolName,
	"description": "Asks the user in the cockpit whether a tool call may run.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool_name":   map[string]any{"type": "string"},
			"input":       map[string]any{"type": "object"},
			"tool_use_id": map[string]any{"type": "string"},
		},
		"required": []string{"tool_name", "input"},
	},
}
