package coder

// SessionRuntime builds coder-specific start and resume commands.
type SessionRuntime interface {
	UsesProvidedSessionID() bool
	StartCommand(sessionID, sessionName, workdir, agentID string, automaticApproval bool) string
	ResumeCommand(sessionID, workdir string, automaticApproval bool) string
	Env() map[string]string
}
