// Package eventbus is a tiny in-process publish/subscribe hub for the server to
// client push channel. The web layer publishes typed events (a coder started, a
// shell was renamed, the tab order changed) and the event stream handler fans
// them out to every connected browser over one SSE connection. The bus keeps no
// history: a fresh subscriber is caught up by the snapshot the stream handler
// pushes on connect, not by replay.
package eventbus

import "sync"

// subBuffer is how many events a slow subscriber may fall behind before further
// events are dropped for it. A dropped subscriber recovers on its next snapshot,
// which the stream handler sends on every reconnect.
const subBuffer = 16

// Event is one server to client message. Type names the kind ("terminals",
// "notifications"); Data is any JSON serialisable payload and may be nil for
// events that only signal "something changed, pull the fresh state".
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// Bus fans events out to every current subscriber. Safe for concurrent use.
type Bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// New returns an empty bus.
func New() *Bus {
	return &Bus{subs: map[chan Event]struct{}{}}
}

// Publish delivers ev to every subscriber. A subscriber whose buffer is full is
// skipped rather than blocking the publisher, so one stalled browser never holds
// up a lifecycle handler.
func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe registers a channel and returns it together with a cancel func that
// must be called when the subscriber goes away.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}
