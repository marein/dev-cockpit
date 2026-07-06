package copilot

import (
	"fmt"
	"strings"

	"github.com/local/dev-cockpit/internal/clirun"
)

type runtime struct{}

func (runtime) UsesProvidedSessionID() bool { return false }

func (runtime) Env() map[string]string { return nil }

func (runtime) StartCommand(sessionID, sessionName, workdir, agentID string, remoteControl, automaticApproval bool) string {
	return fmt.Sprintf("cd %s && exec copilot%s --name %s",
		clirun.ShellQuote(workdir), flags(agentID, remoteControl, automaticApproval), clirun.ShellQuote(sessionName))
}

func (runtime) ResumeCommand(sessionID, workdir string, remoteControl, automaticApproval bool) string {
	return fmt.Sprintf("cd %s && exec copilot%s --resume %s",
		clirun.ShellQuote(workdir), flags("", remoteControl, automaticApproval), clirun.ShellQuote(sessionID))
}

func flags(agentID string, remoteControl, automaticApproval bool) string {
	var flags strings.Builder
	// copilot has no key binding to scroll its transcript line-wise; that only
	// works via mouse-wheel events, which it reads only with mouse reporting on.
	// Default it on so the browser's synthesized wheel scrolls line-by-line.
	flags.WriteString(" --mouse")
	if remoteControl {
		flags.WriteString(" --remote")
	}
	if automaticApproval {
		flags.WriteString(" --yolo")
	}
	if agentID != "" {
		flags.WriteString(" --agent ")
		flags.WriteString(clirun.ShellQuote(agentID))
	}
	return flags.String()
}
