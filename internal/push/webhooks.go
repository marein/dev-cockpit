package push

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/local/dev-cockpit/internal/statefile"
)

// maxWebhooks bounds the webhook list of this single-user cockpit.
const maxWebhooks = 20

// ErrUnknownWebhook reports a send for a webhook id that is not registered
// anymore.
var ErrUnknownWebhook = errors.New("unknown webhook")

// Webhook is one registered notification webhook. The URL is a bearer
// credential, so the list lives in the 0600 channel config.
type Webhook struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// Webhooks posts every notification to each registered webhook. The JSON
// payload carries text, title, body, and url; the text field makes Slack
// incoming webhooks work as is, extra fields are ignored there.
type Webhooks struct {
	cfg *channelConfig
}

// List returns the registered webhooks.
func (w *Webhooks) List() []Webhook { return w.cfg.Webhooks() }

// Add registers a webhook URL. Duplicates are rejected.
func (w *Webhooks) Add(url string) error {
	var err error
	w.cfg.Update(func(state *channelsState) {
		for _, hook := range state.Webhooks {
			if hook.URL == url {
				err = errors.New("This webhook is already registered.")
				return
			}
		}
		if len(state.Webhooks) >= maxWebhooks {
			err = errors.New("Too many webhooks, remove one first.")
			return
		}
		state.Webhooks = append(state.Webhooks, Webhook{ID: statefile.NewID(), URL: url})
	})
	return err
}

// Remove drops a webhook by id and reports whether it existed.
func (w *Webhooks) Remove(id string) bool {
	removed := false
	w.cfg.Update(func(state *channelsState) {
		before := len(state.Webhooks)
		state.Webhooks = slices.DeleteFunc(state.Webhooks, func(hook Webhook) bool { return hook.ID == id })
		removed = len(state.Webhooks) != before
	})
	return removed
}

// Deliver posts msg to every registered webhook.
func (w *Webhooks) Deliver(msg Message) error {
	hooks := w.cfg.Webhooks()
	if len(hooks) == 0 {
		return nil
	}
	payload, err := w.payload(msg)
	if err != nil {
		return err
	}
	var errs []error
	for _, hook := range hooks {
		if err := postWebhook(hook.URL, payload); err != nil {
			errs = append(errs, fmt.Errorf("webhook %s: %w", hook.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Test posts msg to one webhook by id.
func (w *Webhooks) Test(id string, msg Message) error {
	for _, hook := range w.cfg.Webhooks() {
		if hook.ID == id {
			payload, err := w.payload(msg)
			if err != nil {
				return err
			}
			return postWebhook(hook.URL, payload)
		}
	}
	return ErrUnknownWebhook
}

// payload renders msg once per delivery. With a base URL configured the
// link becomes absolute and rides along in the text, so receivers like
// Slack render it clickable.
func (w *Webhooks) payload(msg Message) ([]byte, error) {
	link := msg.URL
	if base := w.cfg.BaseURL(); base != "" && strings.HasPrefix(link, "/") {
		link = base + link
	}
	text := msg.Title
	if msg.Body != "" {
		text += "\n" + msg.Body
	}
	if strings.HasPrefix(link, "http") {
		text += "\n" + link
	}
	return json.Marshal(map[string]string{
		"text":  text,
		"title": msg.Title,
		"body":  msg.Body,
		"url":   link,
	})
}

// postWebhook is the pure HTTP leg. Anything but a 2xx counts as failure,
// redirects are not followed, so a 3xx means the message did not arrive.
func postWebhook(url string, payload []byte) error {
	resp, err := pushHTTPClient.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("returned %d", resp.StatusCode)
	}
	return nil
}
