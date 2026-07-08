package push

import (
	"errors"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/statefile"
)

// maxStoredSubscriptions bounds the file as a safety net. The business cap
// of live devices sits in WebPush.Register, so stale entries left over from
// a key change cannot block re-enabling.
const maxStoredSubscriptions = 100

// Subscription is one registered Web Push device. VAPIDKey records the
// public key the device subscribed with, so a later key change can tell
// which entries are dead.
type Subscription struct {
	ID        string    `json:"id"`
	Endpoint  string    `json:"endpoint"`
	P256dh    string    `json:"p256dh"`
	Auth      string    `json:"auth"`
	Label     string    `json:"label"`
	VAPIDKey  string    `json:"vapidKey"`
	CreatedAt time.Time `json:"createdAt"`
}

// subscriptionStore is the file backed device list, read and written through
// on every call like the other state files.
type subscriptionStore struct {
	path string
	mu   sync.Mutex
}

func newSubscriptionStore(path string) *subscriptionStore {
	return &subscriptionStore{path: path}
}

func (s *subscriptionStore) List() []Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Add stores one subscription. Re-subscribing an endpoint replaces its entry.
func (s *subscriptionStore) Add(sub Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	kept := list[:0]
	for _, existing := range list {
		if existing.Endpoint == sub.Endpoint {
			continue
		}
		kept = append(kept, existing)
	}
	if len(kept) >= maxStoredSubscriptions {
		return errors.New("Too many registered devices, remove one first.")
	}
	sub.ID = statefile.NewID()
	sub.CreatedAt = time.Now().UTC()
	s.save(append(kept, sub))
	return nil
}

// Remove drops every subscription the predicate matches and reports whether
// anything was removed.
func (s *subscriptionStore) Remove(match func(Subscription) bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	kept := list[:0]
	changed := false
	for _, existing := range list {
		if match(existing) {
			changed = true
			continue
		}
		kept = append(kept, existing)
	}
	if changed {
		s.save(kept)
	}
	return changed
}

func (s *subscriptionStore) load() []Subscription {
	var list []Subscription
	statefile.Load(s.path, &list)
	return list
}

func (s *subscriptionStore) save(list []Subscription) {
	statefile.Save(s.path, 0o600, list)
}
