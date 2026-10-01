package claude

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/ollama"
)

type runtime struct {
	notifyInbox string
	launcher    *ollama.Client
	sessions    *ollamaSessions
}

func (runtime) UsesProvidedSessionID() bool { return true }

func (r runtime) Env() map[string]string {
	env := map[string]string{"CLAUDE_CODE_NO_FLICKER": "1"}
	if host := r.launcher.Host(); host != "" {
		env[ollamaHostEnv] = host
	}
	return env
}

// StartCommand builds the interactive session. The name is optional, see the
// flag below. A task is passed as claude's
// positional prompt (`claude [options] [prompt]`), so the session comes up
// already working on it. It goes behind endOfOptions, because the shell quoting
// protects the shell and not claude's own flag parser: a task that starts with a
// dash would otherwise be parsed as an option and never reach the session. A
// model rides behind --model and only on a start: a resume carries none, so a
// resumed session keeps the model it has, whatever /model set inside it.
func (r runtime) StartCommand(start coder.SessionStart) string {
	args := r.flags(start.AgentID, start.AutomaticApproval) + " --session-id " + clirun.ShellQuote(start.SessionID)
	// A session without a name gets no flag at all; an empty --name would be a
	// value claude has to refuse. What such a session is called is read out of
	// its transcript afterwards, see promptTitle in session.go.
	if name := strings.TrimSpace(start.Name); name != "" {
		args += " --name " + clirun.ShellQuote(name)
	}
	model := strings.TrimSpace(start.Model)
	ollamaName, viaOllama := ollamaPick(model)
	if model != "" && !viaOllama {
		args += " --model " + clirun.ShellQuote(model)
	}
	if task := strings.TrimSpace(start.Task); task != "" {
		args += " " + endOfOptions + " " + clirun.ShellQuote(task)
	}
	return r.command(start.Workdir, ollamaName, args)
}

func (r runtime) SessionStarted(start coder.SessionStart) {
	if ollamaName, viaOllama := ollamaPick(start.Model); viaOllama {
		r.sessions.remember(start.SessionID, ollamaName)
	}
}

func (r runtime) ResumeCommand(sessionID, workdir string, automaticApproval bool) string {
	args := r.flags("", automaticApproval) + " --resume " + clirun.ShellQuote(sessionID)
	return r.command(workdir, r.sessions.modelOf(sessionID), args)
}

// command puts claude, or the launcher in front of it, behind the cd. claude
// takes a model it does not know for a 200K window, and the launcher only sets
// the auto compact window, which claude caps at that. The served window is
// handed over as the hard limit, unless the session's shell already carries one.
func (r runtime) command(workdir, ollamaName, args string) string {
	head := "exec claude"
	if ollamaName != "" {
		head = "exec " + ollama.Executable
		for _, arg := range ollamaLaunch(ollamaName) {
			head += " " + clirun.ShellQuote(arg)
		}
		if window := r.launcher.ServedWindow(ollamaName); window > 0 {
			head = maxContextEnv + `="${` + maxContextEnv + ":-" + strconv.Itoa(window) + `}" ` + head
		}
	}
	return "cd " + clirun.ShellQuote(workdir) + " && " + head + args
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
