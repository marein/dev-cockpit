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
	if settings := r.hookSettings(); settings != "" {
		flags.WriteString(" --settings ")
		flags.WriteString(clirun.ShellQuote(settings))
	}
	return flags.String()
}

// hookSettings builds the --settings JSON that wires the Stop and
// Notification hooks into every session dev-cockpit starts, without touching
// the user's own settings files. Each hook streams its stdin JSON into the
// notify inbox; the write goes to a .tmp name first so the poller only ever
// reads complete .json files.
func (r runtime) hookSettings() string {
	if r.notifyInbox == "" {
		return ""
	}
	dir := clirun.ShellQuote(r.notifyInbox)
	command := "d=" + dir + ` && mkdir -p "$d" && f="$d"/$(date +%s%N)-$$ && cat > "$f.tmp" && mv "$f.tmp" "$f.json"`
	hook := []map[string]any{{
		"hooks": []map[string]any{{"type": "command", "command": command}},
	}}
	settings, err := json.Marshal(map[string]any{
		"hooks": map[string]any{"Stop": hook, "Notification": hook},
	})
	if err != nil {
		return ""
	}
	return string(settings)
}
