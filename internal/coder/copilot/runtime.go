package copilot

import (
	"fmt"
	"strings"

	"github.com/local/dev-cockpit/internal/clirun"
	"github.com/local/dev-cockpit/internal/coder"
)

type runtime struct{}

func (runtime) UsesProvidedSessionID() bool { return false }

func (runtime) Env() map[string]string { return nil }

// StartCommand builds the interactive session. A task goes into copilot's
// --interactive flag, which starts interactive mode and runs that prompt, so
// the session comes up already working on it.
func (runtime) StartCommand(start coder.SessionStart) string {
	command := fmt.Sprintf("cd %s && exec copilot%s --name %s",
		clirun.ShellQuote(start.Workdir), flags(start.AgentID, start.AutomaticApproval),
		clirun.ShellQuote(start.Name))
	if task := strings.TrimSpace(start.Task); task != "" {
		command += " --interactive " + clirun.ShellQuote(task)
	}
	return command
}

func (runtime) ResumeCommand(sessionID, workdir string, automaticApproval bool) string {
	return fmt.Sprintf("cd %s && exec copilot%s --resume %s",
		clirun.ShellQuote(workdir), flags("", automaticApproval), clirun.ShellQuote(sessionID))
}

func flags(agentID string, automaticApproval bool) string {
	var flags strings.Builder
	// copilot has no key binding to scroll its transcript line-wise; that only
	// works via mouse-wheel events, which it reads only with mouse reporting on.
	// Default it on so the browser's synthesized wheel scrolls line-by-line.
	flags.WriteString(" --mouse")
	if automaticApproval {
		flags.WriteString(" --yolo")
	}
	if agentID != "" {
		flags.WriteString(" --agent ")
		flags.WriteString(clirun.ShellQuote(agentID))
	}
	return flags.String()
}
