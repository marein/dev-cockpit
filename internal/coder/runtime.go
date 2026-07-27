package coder

// SessionStart is what a new coder session needs to come up. Task is optional:
// when it is set the CLI is started with that first prompt in its argv, which
// is the only delivery that cannot be lost. Typing into a pane that has not
// read stdin yet loses the text without a trace.
type SessionStart struct {
	SessionID         string
	Name              string
	Workdir           string
	AgentID           string
	AutomaticApproval bool
	Task              string
}

// SessionRuntime builds coder-specific start and resume commands.
type SessionRuntime interface {
	UsesProvidedSessionID() bool
	StartCommand(start SessionStart) string
	ResumeCommand(sessionID, workdir string, automaticApproval bool) string
	Env() map[string]string
}
