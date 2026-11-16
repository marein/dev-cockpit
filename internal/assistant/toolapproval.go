package assistant

import "time"

// A tool call a coder would stop on with a permission dialog cannot ask in a
// turn: nobody sits at it. A coder whose CLI lets a tool of an MCP server
// answer such a question in a turn starts ToolApprovalServer for it, the
// cockpit's own command behind the turn's wrapper, which puts the question
// before the user as an approval and answers the decision. The wrapper carries
// the cockpit's directories and the assistant's id, so the question is asked
// as the assistant whose turn it is.
const (
	// ToolApprovalServerName and ToolApprovalToolName are the MCP server and
	// its one tool, as a coder's configuration names them.
	ToolApprovalServerName = "dev-cockpit"
	ToolApprovalToolName   = "approve"
	// ToolApprovalCommand is the command of the wrapper that serves it.
	ToolApprovalCommand = "tool-approval-serve"
)

// ToolApprovalWait is how long a turn's tool call waits for the user before it
// is denied. The turn holds its run, and a check its slot and deadline, the
// whole time, so it is far shorter than the wait of an approval that runs
// later on its own.
const ToolApprovalWait = 5 * time.Minute

// ToolApprovalServer is the argv of the MCP server a turn in workdir asks its
// tool approvals through.
func ToolApprovalServer(workdir string) []string {
	return []string{wrapperIn(workdir), ToolApprovalCommand}
}
