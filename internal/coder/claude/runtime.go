package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/local/dev-cockpit/internal/clirun"
)

type runtime struct {
	notifyInbox string
}

func (runtime) UsesProvidedSessionID() bool { return true }

func (runtime) Env() map[string]string { return map[string]string{"CLAUDE_CODE_NO_FLICKER": "1"} }

func (r runtime) StartCommand(sessionID, sessionName, workdir, agentID string, automaticApproval bool) string {
	return fmt.Sprintf("cd %s && exec claude%s --session-id %s --name %s",
		clirun.ShellQuote(workdir), r.flags(agentID, automaticApproval), clirun.ShellQuote(sessionID), clirun.ShellQuote(sessionName))
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
// when the user's global config carries a fixed theme. It also wires the
// Stop and Notification hooks: each hook streams its stdin JSON into the
// notify inbox; the write goes to a .tmp name first so the poller only ever
// reads complete .json files.
func (r runtime) sessionSettings() string {
	values := map[string]any{"theme": "auto"}
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
