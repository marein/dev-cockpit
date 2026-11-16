package assistant

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
	// ToolApprovalCommand is the command of the wrapper that serves it, and
	// ToolApprovalUnattended its flag for a turn nobody watches.
	ToolApprovalCommand    = "tool-approval-serve"
	ToolApprovalUnattended = "--unattended"
)

// ToolApprovalServer is the argv of the MCP server a turn in workdir asks its
// tool approvals through.
func ToolApprovalServer(workdir string, unattended bool) []string {
	argv := []string{wrapperIn(workdir), ToolApprovalCommand}
	if unattended {
		argv = append(argv, ToolApprovalUnattended)
	}
	return argv
}
