package opencode

import (
	"fmt"
	"log"
	"strings"

	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
)

// createFunc pre-creates one opencode session in workdir, named title, and
// returns its id. cockpitID is written into the session's metadata when it is
// set, which is how a conversation session stays addressable under the id the
// cockpit chose, see assistant.go and session.go.
type createFunc func(workdir, title, cockpitID string) (string, error)

type runtime struct {
	sessions *sessionRepository
	// create is the session pre-creation, injected so tests never spawn a
	// server.
	create createFunc
	// notifyInbox is the directory the injected notification plugin drops
	// its event files into; empty disables the injection, like claude's.
	notifyInbox string
	// ensurePlugin writes that plugin into opencode's config directory,
	// injected so tests never touch the real one.
	ensurePlugin func() error
	// ensureConfig writes the generated terminal theme, its pin config and
	// the tui config, and answers the two paths the environment names, see
	// theme.go; injected like ensurePlugin.
	ensureConfig func() (pin, tui string, err error)
}

// UsesProvidedSessionID is false: opencode mints its own session ids
// (`ses_…`), the TUI's --session flag only resumes ids that exist (verified
// on 1.18.23, an unknown id exits with "Session not found"), and there is no
// flag that could carry a caller's id in.
func (runtime) UsesProvidedSessionID() bool { return false }

// NamesSessions is false: the TUI has no name or title flag either, so a
// session it creates itself carries a placeholder title until the model
// invents one. The promote step therefore matches the fresh session on the
// working directory, see coder.SessionNaming.
func (runtime) NamesSessions() bool { return false }

// explicitWidthEnv switches OpenTUI's explicit width off. OpenTUI probes once
// at start whether the terminal understands OSC 66 and asks the cursor
// position for the answer; under boot load render output slips between the
// probe and that query, the answer reads as column 2 or 3, and the process
// keeps explicit width on for its whole life, there is no second check. Every
// non-ASCII grapheme then leaves as an OSC 66 sequence, tmux does not know it
// and drops it, so the cell stays empty and umlauts disappear. The cockpit
// runs every session through tmux, where explicit width buys nothing anyway.
const explicitWidthEnv = "OPENTUI_FORCE_EXPLICIT_WIDTH"

// Env carries the notify inbox, the terminal theme's pin config and the
// explicit width switch into the session. All of them ride the tmux session
// environment and nothing else, so an opencode the cockpit did not start
// never sees them: the plugin stays inert, the user's own theme stands and
// the probe runs as it always did. The theme files are ensured here because
// the pin path must not travel unless both files landed on the disk, and a
// failed write costs the theming alone, never the session.
func (r runtime) Env() map[string]string {
	env := map[string]string{explicitWidthEnv: "0"}
	if r.notifyInbox != "" {
		env[notifyEnv] = r.notifyInbox
	}
	if r.ensureConfig != nil {
		if pin, tui, err := r.ensureConfig(); err == nil {
			env[themeEnv] = pin
			env[tuiEnv] = tui
		} else {
			log.Printf("opencode: the session config could not be written, sessions keep opencode's own: %v", err)
		}
	}
	return env
}

// injectNotifyPlugin puts the notification plugin in place for the session
// about to start. A failed write is logged and costs the notifications
// alone, never the session: the plugin is help and no requirement.
func (r runtime) injectNotifyPlugin() {
	if r.notifyInbox == "" || r.ensurePlugin == nil {
		return
	}
	if err := r.ensurePlugin(); err != nil {
		log.Printf("opencode: the notification plugin could not be written: %v", err)
	}
}

// StartCommand builds the interactive session, and which shape it takes
// depends on whether a task travels along, because opencode creates its
// session record lazily and the two needs pull apart:
//
// With a task the TUI is started fresh with --prompt, which its home screen
// submits on mount, so the session record appears at once and the task cannot
// be lost. --prompt is only read by that home screen; combined with --session
// it is dropped (verified on 1.18.23), so a pre-created session cannot carry
// a task.
//
// Without a task nothing would create the session record until the first
// typed message, long after the promote window. So the session is created
// ahead of the start through opencode's own server API, named like the
// coder, and the TUI resumes it with --session. A creation that fails is
// logged and falls back to a plain start: the session then works, only its
// stored record appears late and stays under opencode's own naming.
//
// A model rides in front of either shape, in the equals form for the reason
// --prompt takes it, yargs reads `--model=x` as one thing whatever x starts
// with (`-m, --model  model to use in the format of provider/model`, verified
// on 1.18.32). It rides on a start alone: a resume carries none, so a resumed
// session keeps the model it has, whatever was set inside it.
func (r runtime) StartCommand(start coder.SessionStart) string {
	r.injectNotifyPlugin()
	base := fmt.Sprintf("cd %s && exec opencode%s",
		clirun.ShellQuote(start.Workdir), flags(start.AgentID, start.AutomaticApproval))
	if model := strings.TrimSpace(start.Model); model != "" {
		base += " --model=" + clirun.ShellQuote(model)
	}
	if task := strings.TrimSpace(start.Task); task != "" {
		// The equals form is what keeps a task that starts with a dash text:
		// yargs reads a separate `--prompt -dfoo` as the flag without a value
		// followed by an unknown option, while `--prompt=-dfoo` is
		// unambiguous (verified on 1.18.23). The shell quoting protects the
		// shell, the equals sign protects opencode's parser.
		return base + " --prompt=" + clirun.ShellQuote(task)
	}
	if r.create != nil {
		if id, err := r.create(start.Workdir, start.Name, ""); err == nil {
			return base + " --session " + clirun.ShellQuote(id)
		} else {
			log.Printf("opencode: session could not be created ahead of the start, starting plain: %v", err)
		}
	}
	return base
}

func (r runtime) ResumeCommand(sessionID, workdir string, automaticApproval bool) string {
	r.injectNotifyPlugin()
	return fmt.Sprintf("cd %s && exec opencode%s --session %s",
		clirun.ShellQuote(workdir), flags("", automaticApproval),
		clirun.ShellQuote(r.nativeID(sessionID)))
}

// nativeID maps a session id the cockpit holds onto the id opencode knows.
// They differ only for a conversation handed over to a terminal: its store
// row is listed under the cockpit's id, while --session needs opencode's own.
func (r runtime) nativeID(sessionID string) string {
	if r.sessions == nil {
		return sessionID
	}
	return r.sessions.nativeID(sessionID)
}

func flags(agentID string, automaticApproval bool) string {
	var flags strings.Builder
	if automaticApproval {
		// --auto approves every permission that is not explicitly denied,
		// opencode's own flag for exactly this choice.
		flags.WriteString(" --auto")
	}
	if agentID != "" {
		flags.WriteString(" --agent ")
		flags.WriteString(clirun.ShellQuote(agentID))
	}
	return flags.String()
}
