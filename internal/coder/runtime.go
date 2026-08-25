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

// SessionNaming is the optional answer to whether the CLI carries the
// cockpit's chosen name into the session record it creates. A runtime that
// does not implement it is taken to name its sessions, which is what every
// coder before opencode did: copilot has --name, claude takes the whole id.
// A runtime that answers false has a CLI with no name flag and no id flag,
// so the promote step can never find the fresh session by its name and
// matches it on the working directory alone.
type SessionNaming interface {
	NamesSessions() bool
}
