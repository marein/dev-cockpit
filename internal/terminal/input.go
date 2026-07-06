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
	mapped, ok := mapper.Map(raw)
	if !ok {
		return fmt.Errorf(`Unsupported control input "%s".`, raw)
	}
	return t.SendKey(name, mapped)
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
