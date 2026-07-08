package push

import (
	"sync"

	"github.com/local/dev-cockpit/internal/statefile"
)

// channelsState is the per-channel configuration, one key per channel so new
// channels extend the file instead of scattering flat settings. Webhook URLs
// are bearer credentials, so the file is written 0600 and they stay out of
// the world-readable settings store.
type channelsState struct {
	BaseURL  string        `json:"baseUrl"`
	WebPush  webPushConfig `json:"webPush"`
	Webhooks []Webhook     `json:"webhooks"`
}

type webPushConfig struct {
	Subscriber string `json:"subscriber"`
}

// channelConfig is the file backed channel configuration, read and written
// through on every call like the other state files.
type channelConfig struct {
	path string
	mu   sync.Mutex
}

func newChannelConfig(path string) *channelConfig {
	return &channelConfig{path: path}
}

// Update applies fn to the current state under one lock, so read-modify-write
// callers cannot race each other.
func (c *channelConfig) Update(fn func(*channelsState)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.load()
	fn(&state)
	c.save(state)
}

// BaseURL returns the public address of the cockpit, or "" when unset.
// Channels that leave the app use it to build absolute links.
func (c *channelConfig) BaseURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.load().BaseURL
}

func (c *channelConfig) SetBaseURL(base string) {
	c.Update(func(state *channelsState) { state.BaseURL = base })
}

func (c *channelConfig) Webhooks() []Webhook {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.load().Webhooks
}

// Subscriber returns the VAPID contact claim, falling back to the default
// when none is configured.
func (c *channelConfig) Subscriber() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sub := c.load().WebPush.Subscriber; sub != "" {
		return sub
	}
	return defaultSubscriber
}

func (c *channelConfig) load() channelsState {
	state := channelsState{Webhooks: []Webhook{}}
	statefile.Load(c.path, &state)
	return state
}

func (c *channelConfig) save(state channelsState) {
	statefile.Save(c.path, 0o600, state)
}
