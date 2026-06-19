package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/local/dev-cockpit/internal/keys"
	"github.com/local/dev-cockpit/internal/provider"
	"github.com/local/dev-cockpit/internal/tmux"
)

// inputTarget is the minimal tmux input surface sendInput needs. Both the
// forking CLI client (*tmux.Client) and the persistent control client satisfy
// it, so input can take the fork-free path when a stream is attached.
type inputTarget interface {
	SendRaw(name string, data []byte) error
	SendKey(name, key string) error
	SendLiteral(name, text string) error
	PasteLiteral(name, text string) error
}

// controlInput routes keystrokes through a session's persistent control-mode
// connection (no fork per key) and falls back to the forking CLI for buffer
// pastes, which control mode can't express on a single command line. A send
// that fails after the control client has exited is reported as a gone session
// so the caller still answers 410 instead of a generic error.
type controlInput struct {
	ctl *tmux.Control
	cli *tmux.Client
}

func (c controlInput) gone(err error) error {
	if err != nil && c.ctl.Exited() {
		return ErrNoActiveSession
	}
	return err
}

func (c controlInput) SendRaw(name string, data []byte) error {
	return c.gone(c.ctl.SendRaw(name, data))
}
func (c controlInput) SendKey(name, key string) error { return c.gone(c.ctl.SendKey(name, key)) }
func (c controlInput) SendLiteral(name, text string) error {
	return c.gone(c.ctl.SendLiteral(name, text))
}
func (c controlInput) PasteLiteral(name, text string) error { return c.cli.PasteLiteral(name, text) }

// sendInput dispatches one queued user action to a tmux target. Exactly one
// field of item is non-empty.
func sendInput(t inputTarget, mapper provider.ControlMapper, target string, item Input) error {
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

func sendControl(t inputTarget, mapper provider.ControlMapper, name, raw string) error {
	mapped, ok := mapper.Map(raw)
	if !ok {
		return fmt.Errorf(`Unsupported control input "%s".`, raw)
	}
	return t.SendKey(name, mapped)
}

func sendText(t inputTarget, name, text string) error {
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

func sendPrompt(t inputTarget, name, raw string) error {
	prompt := promptPayload(raw)
	if prompt == "" {
		return errors.New("Input text is required.")
	}
	if err := t.PasteLiteral(name, prompt); err != nil {
		return err
	}
	return t.SendKey(name, "Enter")
}

func sendPaste(t inputTarget, name, raw string) error {
	paste := promptPayload(raw)
	if paste == "" {
		return errors.New("Input text is required.")
	}
	return t.PasteLiteral(name, paste)
}

func promptPayload(raw string) string {
	return strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
}
