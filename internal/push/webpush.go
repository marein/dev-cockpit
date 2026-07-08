package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// maxLiveSubscriptions caps the devices that can receive pushes; stale
// entries from an old key do not count, so they never block re-enabling.
const maxLiveSubscriptions = 20

// WebPush sends system notifications to the registered devices. The payload
// is rendered by the service worker (static/sw.js), which shows one
// notification per target via the tag.
type WebPush struct {
	keys  vapidState
	cfg   *channelConfig
	store *subscriptionStore
}

// PublicKey is the VAPID application server key the client subscribes with.
func (w *WebPush) PublicKey() string { return w.keys.PublicKey }

// Devices returns the registered subscriptions.
func (w *WebPush) Devices() []Subscription { return w.store.List() }

// Stale reports whether the subscription was made against older VAPID keys.
// The push service rejects sends to it, so delivery skips it and the
// settings page presents it as dead.
func (w *WebPush) Stale(sub Subscription) bool { return sub.VAPIDKey != w.keys.PublicKey }

// LiveCount returns how many registered devices can currently receive
// pushes.
func (w *WebPush) LiveCount() int {
	live := 0
	for _, sub := range w.store.List() {
		if !w.Stale(sub) {
			live++
		}
	}
	return live
}

// Register stores a device subscription posted by the settings page.
func (w *WebPush) Register(sub Subscription) error {
	sub.VAPIDKey = w.keys.PublicKey
	if w.LiveCount() >= maxLiveSubscriptions {
		return errors.New("Too many registered devices, remove one first.")
	}
	return w.store.Add(sub)
}

// RemoveDevice drops a subscription by id (the settings page device list)
// and reports whether it existed.
func (w *WebPush) RemoveDevice(id string) bool {
	return w.store.Remove(func(s Subscription) bool { return s.ID == id })
}

// RemoveEndpoint drops a subscription by endpoint, used when a device
// unsubscribes itself.
func (w *WebPush) RemoveEndpoint(endpoint string) bool {
	return w.store.Remove(func(s Subscription) bool { return s.Endpoint == endpoint })
}

// Deliver sends msg to every live device. Subscriptions the push service
// reports gone (404 or 410) prune themselves.
func (w *WebPush) Deliver(msg Message) error {
	subs := w.store.List()
	if len(subs) == 0 {
		return nil
	}
	payload, err := json.Marshal(map[string]string{
		"title": msg.Title,
		"body":  msg.Body,
		"url":   msg.URL,
		"tag":   msg.Tag,
	})
	if err != nil {
		return err
	}
	// webpush-go prepends mailto: itself to any subscriber that is not an
	// https URL, so hand it the bare address; a double mailto: makes Apple
	// reject the VAPID JWT with 403.
	subscriber := strings.TrimPrefix(w.cfg.Subscriber(), "mailto:")
	var errs []error
	for _, sub := range subs {
		if w.Stale(sub) {
			continue
		}
		resp, err := webpush.SendNotification(payload, &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
		}, &webpush.Options{
			HTTPClient:      pushHTTPClient,
			Subscriber:      subscriber,
			VAPIDPublicKey:  w.keys.PublicKey,
			VAPIDPrivateKey: w.keys.PrivateKey,
			TTL:             300,
			Urgency:         webpush.UrgencyHigh,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sub.Label, err))
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
			w.RemoveEndpoint(sub.Endpoint)
		case resp.StatusCode >= 300:
			errs = append(errs, fmt.Errorf("%s: push service returned %d %s", sub.Label, resp.StatusCode, strings.TrimSpace(string(body))))
		}
	}
	return errors.Join(errs...)
}
