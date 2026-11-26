package codex

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
)

// runtime holds the store for the one id it has to map: a session the cockpit
// made for an assistant is listed under the cockpit's id, `codex resume`
// takes codex's own.
type runtime struct {
	sessions *sessionRepository
}

// sixteenColors starts codex at the 16 colors of the terminal palette. codex
// asks for the terminal's background once, at its start, and bakes a 256
// color palette from the answer: neither a focus event, a resize nor the mode
// 2031 report make it ask again (verified on 0.172.0), so a codex started in
// dark would stay dark after a switch to light. At 16 colors it draws with the
// default colors and the ANSI slots alone, which the browser's terminal theme
// paints, so a running codex follows a switch of the scheme and the palette
// at once, the way copilot does. It stands on the command line, after the
// login profile, so a TERM or COLORTERM the profile exports cannot undo it.
const sixteenColors = "env -u COLORTERM TERM=screen codex"

// paletteTheme puts code blocks, inline code and the commands codex runs
// under the terminal palette too. codex paints them from a syntax theme in
// 24 bit color whatever TERM says, and picks that theme from the background
// at its start (verified on 0.172.0), so they would keep the scheme codex
// started in. Its theme ansi draws with the ANSI slots and the default color
// alone. It rides the command line for this process, the user's own theme
// in config.toml stays as it is.
const paletteTheme = `tui.theme="ansi"`

// darkPane is the style a codex pane keeps in every theme. codex draws the
// line numbers of a diff in black once the background it asked for at its
// start is light, and dim on a dark or unanswered one (verified on 0.172.0,
// nothing else differs at 16 colors with the theme ansi). Black is a palette
// slot that vanishes on a dark background, and on the light one of some
// palettes, dim follows the foreground everywhere. So codex always finds a
// dark background, and the browser's palette alone decides what shows.
const darkPane = "bg=#000000,fg=#ffffff"

func (runtime) PaneStyle() string { return darkPane }

// The bell is how a codex session tells the cockpit that it has news: codex
// rings the terminal when a turn ends and when it asks its person something,
// here on every one of them and with the plain bell character (verified on
// 0.172.0; its default rings only a terminal that lost the focus, with an
// escape sequence where the terminal is known to take one). It rides the
// command line like the theme, so the user's own notification settings in
// config.toml stay as they are and a codex started outside the cockpit is
// not touched. codex's hooks would say the same in more words, but a hook
// set on the command line stops every start at a review question until its
// trust is written into config.toml, and the one flag that skips the review
// skips it for the hooks a repository brings along as well.
const (
	bellOn     = `tui.notifications=true`
	bellMethod = `tui.notification_method="bel"`
	bellAlways = `tui.notification_condition="always"`
)

// UsesProvidedSessionID is false: codex mints a session's id itself and no
// flag carries one in, `codex resume` only takes an id it has saved.
func (runtime) UsesProvidedSessionID() bool { return false }

// NamesSessions is false: codex has no name flag. The name is written into
// the session's record once it exists, see sessionRepository.NameSession.
func (runtime) NamesSessions() bool { return false }

func (runtime) Env() map[string]string { return nil }

// StartCommand builds the interactive session. The task is the positional
// prompt behind the end of options, so one that starts with a dash stays
// text. The model rides in the equals form for the same reason.
func (runtime) StartCommand(start coder.SessionStart) string {
	command := fmt.Sprintf("cd %s && exec %s%s",
		clirun.ShellQuote(start.Workdir), sixteenColors, flags(start.Workdir, start.AutomaticApproval))
	if model := strings.TrimSpace(start.Model); model != "" {
		command += " --model=" + clirun.ShellQuote(model)
	}
	if task := strings.TrimSpace(start.Task); task != "" {
		command += " -- " + clirun.ShellQuote(task)
	}
	return command
}

func (r runtime) ResumeCommand(sessionID, workdir string, automaticApproval bool) string {
	if r.sessions != nil {
		sessionID = r.sessions.nativeID(sessionID)
	}
	return fmt.Sprintf("cd %s && exec %s resume%s -- %s",
		clirun.ShellQuote(workdir), sixteenColors, flags(workdir, automaticApproval), clirun.ShellQuote(sessionID))
}

// flags carries the answers codex would otherwise ask for or need, and what
// the cockpit needs of it: the palette theme and the bell, see above.
//
// The folder trust rides the command line as a config override and is never
// written into config.toml: the dotted form of -c does not take a path as a
// key (verified on 0.172.0, the dialog still comes up), the inline table
// does. It holds for this process alone, so the user's own config stays the
// way codex wrote it.
//
// Automatic approval switches approvals and the sandbox off together. The
// sandbox alone refuses every socket (verified on 0.172.0, a connect to a
// unix socket fails with "Operation not permitted"), so a coder inside it
// could not reach the cockpit's local API, and the reviewed mode
// (--approve-for-me) keeps that sandbox.
//
// --no-daemon asks for what the overrides force anyway: codex runs them in a
// process of its own, never on its shared background server, and without the
// flag it warns about that at every start (verified on 0.172.0).
func flags(workdir string, automaticApproval bool) string {
	var flags strings.Builder
	flags.WriteString(" --no-daemon")
	flags.WriteString(" -c ")
	flags.WriteString(clirun.ShellQuote(trustOverride(workdir)))
	for _, override := range []string{paletteTheme, bellOn, bellMethod, bellAlways} {
		flags.WriteString(" -c ")
		flags.WriteString(clirun.ShellQuote(override))
	}
	if automaticApproval {
		flags.WriteString(" --dangerously-bypass-approvals-and-sandbox")
	}
	return flags.String()
}

func trustOverride(workdir string) string {
	dirs := []string{workdir}
	if resolved, err := filepath.EvalSymlinks(workdir); err == nil && resolved != workdir {
		dirs = append(dirs, resolved)
	}
	entries := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		entries = append(entries, tomlString(dir)+`={trust_level="trusted"}`)
	}
	return "projects={" + strings.Join(entries, ",") + "}"
}

// tomlString writes a TOML basic string, the one form that can hold every
// path: a literal string cannot hold a single quote.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
