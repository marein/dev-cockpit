package terminal

import (
	"errors"
	"fmt"
	"strings"

	"github.com/local/dev-cockpit/internal/keys"
	"github.com/local/dev-cockpit/internal/tmux"
)

// Input is one queued user action; exactly one field is non-empty.
type Input struct {
	Prompt  string
	Control string
	Text    string
	Paste   string
	Raw     string
}

// Target is the minimal tmux input surface SendInput needs. Both the
// forking CLI client (*tmux.Client) and the persistent control client satisfy
// it, so input can take the fork-free path when a stream is attached.
type Target interface {
	SendRaw(name string, data []byte) error
	SendKey(name, key string) error
	SendLiteral(name, text string) error
	PasteLiteral(name, text string) error
}

// ControlInput routes keystrokes through a session's persistent control-mode
// connection (no fork per key) and falls back to the forking CLI for buffer
// pastes, which control mode can't express on a single command line. A send
// that fails after the control client has exited is reported as a gone session
// so the caller still answers 410 instead of a generic error.
type ControlInput struct {
	Ctl *tmux.Control
	CLI *tmux.Client
	// Gone is returned when a send fails on an exited control client, so each
	// caller reports its own flavor of "not running" to the browser.
	Gone error
}

func (c ControlInput) gone(err error) error {
	if err != nil && c.Ctl.Exited() {
		return c.Gone
	}
	return err
}

func (c ControlInput) SendRaw(name string, data []byte) error {
	return c.gone(c.Ctl.SendRaw(name, data))
}
func (c ControlInput) SendKey(name, key string) error { return c.gone(c.Ctl.SendKey(name, key)) }
func (c ControlInput) PaneForeground(name string) (tmux.PaneForeground, error) {
	return c.Ctl.PaneForeground(name)
}
func (c ControlInput) SendLiteral(name, text string) error {
	return c.gone(c.Ctl.SendLiteral(name, text))
}
func (c ControlInput) PasteLiteral(name, text string) error { return c.CLI.PasteLiteral(name, text) }

// SendInput dispatches one queued user action to a tmux target. Exactly one
// field of item is non-empty.
func SendInput(t Target, mapper ControlMapper, target string, item Input) error {
	switch {
	case item.Raw != "":
		return t.SendRaw(target, []byte(item.Raw))
	case strings.TrimSpace(item.Control) != "":
		return sendControl(t, mapper, target, item.Control)
	case item.Text != "":
		return sendText(t, target, item.Text)
	case item.Paste != "":
		return sendPaste(t, target, item.Paste)
	case item.Prompt != "":
		return sendPrompt(t, target, item.Prompt)
	}
	return errors.New("Input is required.")
}

func sendControl(t Target, mapper ControlMapper, name, raw string) error {
	if strings.EqualFold(strings.TrimSpace(raw), "shift-enter") {
		return sendShiftEnter(t, name)
	}
	mapped, ok := mapper.Map(raw)
	if !ok {
		return fmt.Errorf(`Unsupported control input "%s".`, raw)
	}
	return t.SendKey(name, mapped)
}

// ForegroundReporter is the optional Target capability behind Shift+Enter:
// both transports report the pane's foreground process, the fork-free control
// connection and the CLI fallback.
type ForegroundReporter interface {
	PaneForeground(name string) (tmux.PaneForeground, error)
}

// extendedKeysCommands are the foreground programs whose interactive TUIs
// enable the kitty keyboard protocol, verified by watching their startup
// sequences in the pane stream. Kept in sync with the coder CLIs by hand.
var extendedKeysCommands = map[string]bool{"claude": true, "copilot": true}

// kittyShiftEnter is Shift+Enter in the kitty extended keys encoding,
// CSI 13;2u, where 13 is Enter and 2 the Shift modifier.
var kittyShiftEnter = []byte("\x1b[13;2u")

// sendShiftEnter turns the shift-enter control into the real extended key for
// programs that understand it and plain Enter for everyone else. The decision
// is made here, per keystroke, because nobody else in the chain can make it.
//
// The legacy terminal protocol has no byte form for Shift+Enter, Enter and
// Shift+Enter are both 0x0D. Only a program that opted into the kitty
// protocol can receive the difference. xterm.js does not implement that
// protocol and emits a plain CR for Shift+Enter, so the browser cannot encode
// the key itself. The client only reports that Shift+Enter was pressed and
// the server picks the bytes.
//
// Delegating the pick to tmux with the named key S-Enter does not work. tmux
// 3.4 types the literal text "S-Enter" into panes without extended keys
// (verified, there is no legacy fallback). 3.5a degrades to \r instead, but
// gates pane encoding behind the extended-keys server option (default off)
// and keeps ignoring the kitty push, it honors only modifyOtherKeys. Sending
// the raw CSI u bytes ourselves keeps every tmux version out of the decision;
// they must never reach a program that did not negotiate the protocol, which
// would read them as junk keystrokes.
//
// tmux 3.4 exposes no format for a pane's negotiated keyboard mode either, so
// the foreground program stands in for it, the same proxy the mode 2031 color
// scheme report uses (terminaltheme.go). That way a coder started by hand
// inside a shell session gets the real key too, and the same pane falls back
// to plain Enter after the coder exits. From tmux 3.5a on, a real state query
// (#{pane_key_mode} != "VT10x") could join the condition.
//
// Tracking the kitty push/pop sequences in the session output stream (like
// the notification watchers do for bells and prompt marks) was considered and
// rejected: that state has loss windows (watcher attach race on session
// start, serve restart while sessions keep running, output ring resets, a
// crash without pop), while this check is stateless and fresh on every
// keystroke. The cost is one command round trip on the already open control
// connection, paid only for this key.
func sendShiftEnter(t Target, name string) error {
	if fr, ok := t.(ForegroundReporter); ok {
		if fg, err := fr.PaneForeground(name); err == nil && fg.AltScreen && extendedKeysCommands[fg.Command] {
			return t.SendRaw(name, kittyShiftEnter)
		}
	}
	return t.SendKey(name, "Enter")
}

func sendText(t Target, name, text string) error {
	if text == "" {
		return errors.New("Input text is required.")
	}
	for _, ev := range keys.Decode(text) {
		if ev.Key != "" {
			if err := t.SendKey(name, ev.Key); err != nil {
				return err
			}
			continue
		}
		if err := t.SendLiteral(name, ev.Text); err != nil {
			return err
		}
	}
	return nil
}

func sendPrompt(t Target, name, raw string) error {
	prompt := promptPayload(raw)
	if prompt == "" {
		return errors.New("Input text is required.")
	}
	if err := t.PasteLiteral(name, prompt); err != nil {
		return err
	}
	return t.SendKey(name, "Enter")
}

func sendPaste(t Target, name, raw string) error {
	paste := promptPayload(raw)
	if paste == "" {
		return errors.New("Input text is required.")
	}
	return t.PasteLiteral(name, paste)
}

func promptPayload(raw string) string {
	return strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
}
