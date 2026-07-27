package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/local/dev-cockpit/internal/clirun"
	"github.com/local/dev-cockpit/internal/coder"
)

type runtime struct {
	notifyInbox string
}

func (runtime) UsesProvidedSessionID() bool { return true }

func (runtime) Env() map[string]string { return map[string]string{"CLAUDE_CODE_NO_FLICKER": "1"} }

// StartCommand builds the interactive session. A task is passed as claude's
// positional prompt (`claude [options] [prompt]`), so the session comes up
// already working on it.
func (r runtime) StartCommand(start coder.SessionStart) string {
	command := fmt.Sprintf("cd %s && exec claude%s --session-id %s --name %s",
		clirun.ShellQuote(start.Workdir), r.flags(start.AgentID, start.AutomaticApproval),
		clirun.ShellQuote(start.SessionID), clirun.ShellQuote(start.Name))
	if task := strings.TrimSpace(start.Task); task != "" {
		command += " " + clirun.ShellQuote(task)
	}
	return command
}

func (r runtime) ResumeCommand(sessionID, workdir string, automaticApproval bool) string {
	return fmt.Sprintf("cd %s && exec claude%s --resume %s",
		clirun.ShellQuote(workdir), r.flags("", automaticApproval), clirun.ShellQuote(sessionID))
}

func (r runtime) flags(agentID string, automaticApproval bool) string {
	var flags strings.Builder
	if automaticApproval {
		flags.WriteString(" --permission-mode auto")
	}
	if agentID != "" {
		flags.WriteString(" --agent ")
		flags.WriteString(clirun.ShellQuote(agentID))
	}
	if settings := r.sessionSettings(); settings != "" {
		flags.WriteString(" --settings ")
		flags.WriteString(clirun.ShellQuote(settings))
	}
	return flags.String()
}

// sessionSettings builds the --settings JSON for every session dev-cockpit
// starts, without touching the user's own settings files. It pins the theme
// to auto so claude follows the terminal background signal (the tmux pane
// style answers its OSC 11 query, mode 2031 reports switch it live) even
// when the user's global config carries a fixed theme. It disables the agent
// view, because the cockpit forwards keys via send-keys and tmux never
// swallows Ctrl+B as prefix, an accidental Ctrl+B or a left arrow into the
// agent view would turn the session into a background agent the cockpit can
// no longer resume. It also wires the Stop and Notification hooks: each hook
// streams its stdin JSON into the notify inbox; the write goes to a .tmp
// name first so the poller only ever reads complete .json files.
func (r runtime) sessionSettings() string {
	values := map[string]any{"theme": "auto", "disableAgentView": true}
	if r.notifyInbox != "" {
		dir := clirun.ShellQuote(r.notifyInbox)
		command := "d=" + dir + ` && mkdir -p "$d" && f="$d"/$(date +%s%N)-$$ && cat > "$f.tmp" && mv "$f.tmp" "$f.json"`
		hook := []map[string]any{{
			"hooks": []map[string]any{{"type": "command", "command": command}},
		}}
		values["hooks"] = map[string]any{"Stop": hook, "Notification": hook}
	}
	settings, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(settings)
}
