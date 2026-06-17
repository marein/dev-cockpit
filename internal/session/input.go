package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/local/dev-cockpit/internal/keys"
	"github.com/local/dev-cockpit/internal/provider"
	"github.com/local/dev-cockpit/internal/tmux"
)

// sendInput dispatches one queued user action to a tmux target. Exactly one
// field of item is non-empty.
func sendInput(t *tmux.Client, mapper provider.ControlMapper, target string, item Input) error {
	switch {
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

func sendControl(t *tmux.Client, mapper provider.ControlMapper, name, raw string) error {
	mapped, ok := mapper.Map(raw)
	if !ok {
		return fmt.Errorf(`Unsupported control input "%s".`, raw)
	}
	return t.SendKey(name, mapped)
}

func sendText(t *tmux.Client, name, text string) error {
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

func sendPrompt(t *tmux.Client, name, raw string) error {
	prompt := promptPayload(raw)
	if prompt == "" {
		return errors.New("Input text is required.")
	}
	if err := t.PasteLiteral(name, prompt); err != nil {
		return err
	}
	return t.SendKey(name, "Enter")
}

func sendPaste(t *tmux.Client, name, raw string) error {
	paste := promptPayload(raw)
	if paste == "" {
		return errors.New("Input text is required.")
	}
	return t.PasteLiteral(name, paste)
}

func promptPayload(raw string) string {
	return strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
}
