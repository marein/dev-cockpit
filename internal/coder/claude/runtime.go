package claude

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/coder/claude/statusline"
	"github.com/marein/dev-cockpit/internal/ollama"
	"github.com/marein/dev-cockpit/internal/settings"
)

// statusLineRefreshSeconds is how often claude draws the status line again on
// its own. Ten seconds keep a countdown and the time left on a limit current,
// and the renderer costs nothing at that: the whole default line is a few
// milliseconds and asks the network at most once every two minutes, see
// usageMaxAge. The redraw paints the screen, which the working mark never
// reads for claude, see ActivityProfile.
const statusLineRefreshSeconds = 10

type runtime struct {
	notifyInbox string
	// statusLine is the rendered status line, written in every mode, and
	// statusLineCommand the renderer claude runs to draw it.
	statusLine        string
	statusLineCommand string
	// userSettings is claude's global settings file, the one place the
	// fallback mode looks for a status line of the user's own.
	userSettings string
	store        *settings.Store
	launcher     *ollama.Client
	sessions     *ollamaSessions
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
// model rides behind --model and an effort behind --effort, and only on a
// start: a resume carries neither, so a resumed session keeps the model and
// the level it has, whatever /model and /effort set inside it.
func (r runtime) StartCommand(start coder.SessionStart) string {
	args := r.flags(start.SessionID, start.AgentID, start.AutomaticApproval) + " --session-id " + clirun.ShellQuote(start.SessionID)
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
	if effort := strings.TrimSpace(start.Effort); effort != "" {
		args += " --effort " + clirun.ShellQuote(effort)
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
	args := r.flags(sessionID, "", automaticApproval) + " --resume " + clirun.ShellQuote(sessionID)
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

func (r runtime) flags(terminal, agentID string, automaticApproval bool) string {
	var flags strings.Builder
	if automaticApproval {
		flags.WriteString(" --permission-mode auto")
	}
	if agentID != "" {
		flags.WriteString(" --agent ")
		flags.WriteString(clirun.ShellQuote(agentID))
	}
	if settings := r.sessionSettings(terminal); settings != "" {
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
// no longer resume. It also wires the turn hooks, see turnHooks: each hook
// streams its stdin JSON into the notify inbox, wrapped with the terminal it
// belongs to, because claude's own session id moves with /clear and /resume
// while the terminal stays. The write goes to a .tmp name first so the poller
// only ever reads complete .json files, and the file's modification time is
// the moment the hook fired. A write that fails drops its .tmp and still
// exits 0 without output: claude prints every failing hook into the coder,
// two lines for each of the many hooks a turn fires, while a missing signal
// costs only the mark, which the record and the cap still close.
//
// The status line joins it the same way, as a command claude runs, see
// wantsStatusLine for when. It carries a refresh interval, because without one
// claude draws the line on its own state changes alone: the clock, the age of
// the last commit and the time left on a limit would then stand still between
// two answers, which is exactly the number somebody put them on the line for.
func (r runtime) sessionSettings(terminal string) string {
	values := map[string]any{"theme": "auto", "disableAgentView": true}
	if r.wantsStatusLine() {
		values["statusLine"] = map[string]any{
			"type": "command", "command": r.statusLineCommand, "refreshInterval": statusLineRefreshSeconds,
		}
	}
	if r.notifyInbox != "" {
		hook := []map[string]any{{
			"hooks": []map[string]any{{"type": "command", "command": hookCommand(r.notifyInbox, terminal)}},
		}}
		hooks := map[string]any{}
		for _, name := range turnHooks {
			hooks[name] = hook
		}
		values["hooks"] = hooks
	}
	settings, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(settings)
}

// hookCommand is the command every hook runs: it wraps the hook's stdin as
// the event of the terminal it belongs to, see notify's inbox, and drops it
// into the inbox dir.
func hookCommand(inbox, terminal string) string {
	id, _ := json.Marshal(terminal)
	head := clirun.ShellQuote(`{"terminal":` + string(id) + `,"event":`)
	return "{ d=" + clirun.ShellQuote(inbox) + `; f="$d"/$(date +%s%N)-$$; mkdir -p "$d" && { printf %s ` + head + ` && cat && printf '}'; } > "$f.tmp" && mv "$f.tmp" "$f.json" || rm -f "$f.tmp"; } >/dev/null 2>&1; exit 0`
}

// wantsStatusLine decides per session start and resume, from the mode stored
// right now, so a mode change reaches the sessions started after it and a line
// somebody sets in their own settings later wins from the next session on
// without a visit to the cockpit. A running session keeps what it started with.
// Only a line that is really written counts, never a command that draws
// nothing.
func (r runtime) wantsStatusLine() bool {
	if r.statusLine == "" || r.statusLineCommand == "" {
		return false
	}
	if info, err := os.Stat(r.statusLine); err != nil || !info.Mode().IsRegular() {
		return false
	}
	switch r.statusLineMode() {
	case statusline.ModeOff:
		return false
	case statusline.ModeAlways:
		return true
	}
	return !hasOwnStatusLine(r.userSettings)
}

// statusLineMode is the stored mode, the default one where nothing can be
// read, which is what the settings page shows then too.
func (r runtime) statusLineMode() statusline.Mode {
	if r.store == nil {
		return statusline.DefaultConfig().Mode
	}
	config, err := statusline.Decode(r.store.Get(statusline.SettingKey))
	if err != nil {
		return statusline.DefaultConfig().Mode
	}
	return config.Mode
}

// hasOwnStatusLine reports whether claude's global settings set a status line.
// A file that cannot be read or parsed counts as one: the cockpit steps back
// rather than replace a line it could not see.
func hasOwnStatusLine(path string) bool {
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return true
	}
	var own struct {
		StatusLine any `json:"statusLine"`
	}
	if err := json.Unmarshal(data, &own); err != nil {
		return true
	}
	return own.StatusLine != nil
}
