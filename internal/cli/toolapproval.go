package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
		Short:  "Serve the MCP tool a turn asks the user its tool approvals through",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ask := func(call toolCall) toolVerdict { return askToolApproval(*opts, call) }
			return serveToolApprovals(cmd.InOrStdin(), cmd.OutOrStdout(), ask)
		},
	}
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

// toolInputBytes is how much of a call's input travels to the question,
// above what the dialog shows of a value, so the cockpit decides where the
// dialog cuts it.
const toolInputBytes = 96 << 10

// toolApprovalSlack is how much longer than the question's wait the request
// may take: the server ends every question itself and answers why, this only
// keeps a request from hanging on a server that went away without closing its
// socket.
const toolApprovalSlack = time.Minute

// askToolApproval asks the running cockpit and waits for the user. Whatever
// keeps the question from being asked denies the call, never lets it through.
func askToolApproval(opts inspectOptions, call toolCall) toolVerdict {
	input := call.Input
	if len(input) == 0 || string(input) == "null" {
		input = json.RawMessage("{}")
	}
	shown, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		shown = input
	}
	cut := len(shown) > toolInputBytes
	if cut {
		end := toolInputBytes
		for end > 0 && !utf8.RuneStart(shown[end]) {
			end--
		}
		shown = shown[:end]
	}
	if call.ID == "" {
		call.ID = statefile.NewID()
	}
	deny := func(err error) toolVerdict {
		return toolVerdict{Behavior: "deny", Message: "The cockpit could not ask the user about this tool call, so it was denied: " + err.Error()}
	}
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return deny(err)
	}
	answer, err := client.PostJSON("/assistants/tool-approvals", map[string]any{
		"tool":  call.Name,
		"call":  call.ID,
		"input": string(shown),
		"cut":   cut,
	}, assistant.ToolApprovalWait+toolApprovalSlack)
	if err != nil {
		return deny(err)
	}
	if jsonBool(answer["allow"]) {
		return toolVerdict{Behavior: "allow", UpdatedInput: input}
	}
	message, _ := answer["message"].(string)
	if strings.TrimSpace(message) == "" {
		message = "The tool call was denied."
	}
	return toolVerdict{Behavior: "deny", Message: message}
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
