package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/local/dev-cockpit/internal/clirun"
	"github.com/local/dev-cockpit/internal/coder"
)

// statusLineRefreshSeconds is how often claude draws the status line again on
// its own. A minute is what a clock on the line needs to be right and what the
// generated script costs nothing at: the whole default line is a few
// milliseconds and asks no network of its own.
const statusLineRefreshSeconds = 60

type runtime struct {
	notifyInbox string
	// statusLine is the generated status line script. Whether that file is
	// there is the whole state of the feature: no script, no statusLine in the
	// settings blob, so a claude with a status line of its own keeps it.
	statusLine string
}

func (runtime) UsesProvidedSessionID() bool { return true }

func (runtime) Env() map[string]string { return map[string]string{"CLAUDE_CODE_NO_FLICKER": "1"} }

// StartCommand builds the interactive session. A task is passed as claude's
// positional prompt (`claude [options] [prompt]`), so the session comes up
// already working on it. It goes behind endOfOptions, because the shell quoting
// protects the shell and not claude's own flag parser: a task that starts with a
// dash would otherwise be parsed as an option and never reach the session.
func (r runtime) StartCommand(start coder.SessionStart) string {
	command := fmt.Sprintf("cd %s && exec claude%s --session-id %s --name %s",
		clirun.ShellQuote(start.Workdir), r.flags(start.AgentID, start.AutomaticApproval),
		clirun.ShellQuote(start.SessionID), clirun.ShellQuote(start.Name))
	if task := strings.TrimSpace(start.Task); task != "" {
		command += " " + endOfOptions + " " + clirun.ShellQuote(task)
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
//
// The status line joins it the same way, as a command claude runs, and only
// when the generated script is on disk: an install that never put one
// together keeps whatever status line the user's own settings ask for. It
// carries a refresh interval, because without one claude draws the line on its
// own state changes alone: the clock, the age of the last commit and the time
// left on a limit would then stand still between two answers, which is exactly
// the number somebody put them on the line for.
func (r runtime) sessionSettings() string {
	values := map[string]any{"theme": "auto", "disableAgentView": true}
	if r.statusLine != "" {
		if info, err := os.Stat(r.statusLine); err == nil && info.Mode().IsRegular() {
			values["statusLine"] = map[string]any{
				"type": "command", "command": r.statusLine, "refreshInterval": statusLineRefreshSeconds,
			}
		}
	}
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
