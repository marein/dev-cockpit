package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// A turn runs without anybody at its terminal, so a tool call its coder would
// stop on with a permission dialog, a connector tool the organization wants
// approved first among them, comes here instead: the coder's permission
// prompt goes to the tool approval server behind the turn's wrapper, which
// posts it to toolApprovalsPath and waits for the answer. The question is an
// approval like any other, on every page and on the phone, except that the
// request waits for it: the answer goes back into the running turn and no note
// lands in the thread. The turn ending kills the server, the request breaks off
// and the question goes with it.

// toolApprovalsPath is where the tool approval server asks.
const toolApprovalsPath = "/assistants/tool-approvals"

// toolApprovalKind is the approval kind of a tool call. It is no entry of
// approvalKinds: there is no switch to ask nothing, a tool the organization
// wants approved is never let through for every assistant at once. What the
// user allows for good is one tool at a time, see allowedToolsKey.
const toolApprovalKind = "tool-use"

// allowedToolsKey holds the tools the user allowed for good, for every
// assistant, as a sorted JSON list in the settings store. maxAllowedTools bounds
// it; a tool past the bound is asked about again each time.
const (
	allowedToolsKey = "assistant-allowed-tools"
	maxAllowedTools = 200
)

// toolNamePattern is what a tool's name may be: the coder's own tools and
// those of an MCP server, mcp__<server>__<tool>, in the letters coders write
// them in. A name it refuses is no tool any coder calls.
var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,200}$`)

// toolCallPattern is what the id of one tool call may be.
var toolCallPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// The words for the user and for the turn. The denials are read by the model,
// each says what happened and what to do instead of trying again.
const (
	toolRememberLabel = "Always allow this tool"
	toolRemoved       = "Tool removed."
	toolDeclined      = "The user denied this tool call in the cockpit. Do not call it again for the same purpose; go on without it or tell the user what you could not do."
	toolExpired       = "Nobody answered the approval of this tool call within %d minutes, so it was denied. Do not call it again in this turn; tell the user it needs their approval."
	toolWithdrawn     = "The approval of this tool call was withdrawn before the user answered."
	toolNotAsked      = "The cockpit could not ask the user about this tool call, so it was denied: %s"
)

type toolApprovalRequest struct {
	Tool  string                `json:"tool"`
	Call  string                `json:"call"`
	Input []assistant.ToolField `json:"input"`
}

// handleToolApproval answers whether a turn's tool call may run: at once for a
// tool the user allowed for good, otherwise once the user decided. Every
// answer is a 200 with allow and, for a denial, the message the turn reads; a
// request it cannot read is refused.
func (s *Server) handleToolApproval(c *gin.Context) {
	owner, err := s.assistantCaller(c)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	var req toolApprovalRequest
	if err := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, assistant.ToolApprovalBody)).Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	if !toolNamePattern.MatchString(req.Tool) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The tool name is not one a coder calls."})
		return
	}
	if !toolCallPattern.MatchString(req.Call) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The tool call id is not one a coder writes."})
		return
	}
	if err := assistant.CheckToolFields(req.Input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if slices.Contains(AllowedTools(s.settings), req.Tool) {
		c.JSON(http.StatusOK, gin.H{"allow": true})
		return
	}
	details := []askpass.Detail{{Label: "Tool", Value: req.Tool, As: askpass.AsMono}}
	for _, field := range req.Input {
		label := field.Key
		if label == "" {
			label = "Input"
		}
		details = append(details, askpass.Detail{Label: label, Value: field.Value, As: askpass.AsCode, Cut: field.Cut})
	}
	tool := req.Tool
	_, name := toolParts(tool)
	verdict, err := s.approvals.Await(c.Request.Context(), approval.Request{
		Owner:         owner,
		Kind:          toolApprovalKind,
		Key:           owner + "/" + req.Call,
		What:          "Use " + name,
		Icon:          "tool",
		Details:       details,
		RememberLabel: toolRememberLabel,
		Remember:      func() { allowTool(s.settings, tool) },
	}, assistant.ToolApprovalWait)
	switch {
	case err != nil:
		c.JSON(http.StatusOK, gin.H{"allow": false, "message": fmt.Sprintf(toolNotAsked, err.Error())})
	case verdict == approval.Approved:
		c.JSON(http.StatusOK, gin.H{"allow": true})
	case verdict == approval.Declined:
		c.JSON(http.StatusOK, gin.H{"allow": false, "message": toolDeclined})
	case verdict == approval.Expired:
		c.JSON(http.StatusOK, gin.H{"allow": false, "message": fmt.Sprintf(toolExpired, int(assistant.ToolApprovalWait/time.Minute))})
	default:
		c.JSON(http.StatusOK, gin.H{"allow": false, "message": toolWithdrawn})
	}
}

// toolParts splits an MCP tool, mcp__<server>__<tool>, into the server it
// comes from and its own name, the part a person recognises. Any other tool is
// its own name and comes from no server.
func toolParts(tool string) (server, name string) {
	if rest, ok := strings.CutPrefix(tool, "mcp__"); ok {
		if i := strings.LastIndex(rest, "__"); i > 0 && i+2 < len(rest) {
			return rest[:i], rest[i+2:]
		}
	}
	return "", tool
}

// allowedToolRows are the tools allowed for good as the Approvals tab lists them.
func allowedToolRows(store *settings.Store) []render.AllowedToolRow {
	tools := AllowedTools(store)
	rows := make([]render.AllowedToolRow, 0, len(tools))
	for _, tool := range tools {
		server, name := toolParts(tool)
		rows = append(rows, render.AllowedToolRow{Tool: tool, Name: name, Server: server})
	}
	return rows
}

// AllowedTools are the tools the user allowed for good, sorted. A value that
// does not read as a list is no tool.
func AllowedTools(store *settings.Store) []string {
	if store == nil {
		return nil
	}
	return decodeTools(store.Get(allowedToolsKey))
}

func decodeTools(raw string) []string {
	var tools []string
	if raw == "" || json.Unmarshal([]byte(raw), &tools) != nil {
		return nil
	}
	return slices.DeleteFunc(tools, func(tool string) bool { return !toolNamePattern.MatchString(tool) })
}

// allowTool adds a tool to the ones allowed for good.
func allowTool(store *settings.Store, tool string) {
	if store == nil || !toolNamePattern.MatchString(tool) {
		return
	}
	store.Update(allowedToolsKey, func(raw string, ok bool) (string, bool) {
		tools := decodeTools(raw)
		if slices.Contains(tools, tool) {
			return raw, ok
		}
		if len(tools) >= maxAllowedTools {
			log.Printf("assistant: %d tools always allowed already, %s still asks", len(tools), tool)
			return raw, ok
		}
		tools = append(tools, tool)
		slices.Sort(tools)
		encoded, _ := json.Marshal(tools)
		return string(encoded), true
	})
}

// forgetTool takes a tool out of the ones allowed for good; the last one gone
// takes the key with it.
func forgetTool(store *settings.Store, tool string) {
	if store == nil {
		return
	}
	store.Update(allowedToolsKey, func(raw string, ok bool) (string, bool) {
		tools := decodeTools(raw)
		if !slices.Contains(tools, tool) {
			return raw, ok
		}
		tools = slices.DeleteFunc(tools, func(t string) bool { return t == tool })
		if len(tools) == 0 {
			return "", false
		}
		encoded, _ := json.Marshal(tools)
		return string(encoded), true
	})
}
