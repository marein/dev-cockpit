// Package push forwards notification news to external channels: Web Push to
// registered devices (the installed web app on iPhone, or any browser) and
// registered webhooks. It listens on the notifier fan-out, waits a short
// delay, and re-checks the unread state before sending, so news read on a
// visibly open page never rings a phone.
package push

import (
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/local/dev-cockpit/internal/notify"
)

// deliverDelay mirrors the client's grace window: a target page open in a
// visible tab marks fresh news read within about one second (POST plus SSE
// round trip). Waiting slightly longer and re-checking keeps external pushes
// silent for news the user is already looking at.
const deliverDelay = 2 * time.Second

// Message is one delivery to an external channel. Tag carries the target id,
// so channels that support it collapse to one notification per target,
// matching the one unread entry per target model.
type Message struct {
	Title string
	Body  string
	URL   string
	Tag   string
}

// Notifier is the slice of notify.Service the dispatcher needs.
type Notifier interface {
	Subscribe() (<-chan notify.Event, func())
	UnreadTargets() map[string]bool
}

// Service bundles the push channels and their state.
type Service struct {
	WebPush  *WebPush
	Webhooks *Webhooks
	cfg      *channelConfig
	delay    time.Duration
}

// BaseURL returns the configured public address of the cockpit.
func (s *Service) BaseURL() string { return s.cfg.BaseURL() }

// SetBaseURL stores the public address, "" turns absolute links off.
func (s *Service) SetBaseURL(base string) { s.cfg.SetBaseURL(base) }

// NewService loads or creates the push state in stateDir: the VAPID identity,
// the device subscriptions, and the per-channel configuration.
func NewService(stateDir string) (*Service, error) {
	keys, err := loadOrCreateVAPID(filepath.Join(stateDir, "push-vapid.json"))
	if err != nil {
		return nil, fmt.Errorf("web push keys: %w", err)
	}
	cfg := newChannelConfig(filepath.Join(stateDir, "push-channels.json"))
	return &Service{
		WebPush:  &WebPush{keys: keys, cfg: cfg, store: newSubscriptionStore(filepath.Join(stateDir, "push-subscriptions.json"))},
		Webhooks: &Webhooks{cfg: cfg},
		cfg:      cfg,
		delay:    deliverDelay,
	}, nil
}

// Start subscribes to the notifier fan-out before returning, so no event
// published afterwards can be missed, and drains it in a goroutine for the
// process lifetime: news still unread after the delay goes to every channel.
func (s *Service) Start(n Notifier) {
	events, cancel := n.Subscribe()
	go func() {
		defer cancel()
		for ev := range events {
			if ev.Added == nil {
				continue
			}
			added := *ev.Added
			time.AfterFunc(s.delay, func() {
				if !n.UnreadTargets()[added.TargetID] {
					return
				}
				s.deliver(Message{
					Title: fmt.Sprintf("Something new in %q.", added.TargetName),
					Body:  added.Project,
					URL:   added.URL,
					Tag:   added.TargetID,
				})
			})
		}
	}()
}

func (s *Service) deliver(msg Message) {
	if err := s.WebPush.Deliver(msg); err != nil {
		log.Printf("push: web push: %v", err)
	}
	if err := s.Webhooks.Deliver(msg); err != nil {
		log.Printf("push: webhooks: %v", err)
	}
}

// TestMessage is what the settings page test buttons send.
func TestMessage() Message {
	return Message{
		Title: "Test notification.",
		Body:  "The push channel works.",
		URL:   "/settings/notifications",
	}
}
