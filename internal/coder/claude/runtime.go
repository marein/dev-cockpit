package claude

import (
	"fmt"
	"strings"

	"github.com/local/dev-cockpit/internal/clirun"
)

type runtime struct{}

func (runtime) UsesProvidedSessionID() bool { return true }

func (runtime) Env() map[string]string { return map[string]string{"CLAUDE_CODE_NO_FLICKER": "1"} }

func (runtime) StartCommand(sessionID, sessionName, workdir, agentID string, remoteControl, automaticApproval bool) string {
	return fmt.Sprintf("cd %s && exec claude%s --session-id %s --name %s",
		clirun.ShellQuote(workdir), flags(agentID, remoteControl, automaticApproval), clirun.ShellQuote(sessionID), clirun.ShellQuote(sessionName))
}

func (runtime) ResumeCommand(sessionID, workdir string, remoteControl, automaticApproval bool) string {
	return fmt.Sprintf("cd %s && exec claude%s --resume %s",
		clirun.ShellQuote(workdir), flags("", remoteControl, automaticApproval), clirun.ShellQuote(sessionID))
}

func flags(agentID string, remoteControl, automaticApproval bool) string {
	var flags strings.Builder
	if remoteControl {
		flags.WriteString(" --remote-control")
	}
	if automaticApproval {
		flags.WriteString(" --permission-mode auto")
	}
	if agentID != "" {
		flags.WriteString(" --agent ")
		flags.WriteString(clirun.ShellQuote(agentID))
	}
	return flags.String()
}
