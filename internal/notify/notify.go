// Package notify records that a target has news, whether it finished the
// current request, asks a question, or waits for a permission, and fans that
// out to the web UI. Events are deliberately not classified further. They are
// produced by provider-native signals: Claude Code Stop/Notification hooks
// dropping JSON files into the provider inbox, the copilot terminal bell, and
// shell prompt marks. State is one small JSON file in the dev-cockpit state
// directory, read and written through the file on every call so a fresh
// process picks up the latest entries.
package notify

import (
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/statefile"
)

const maxStored = 100

// dedupeWindow swallows follow-up signals for a target that already has a
// fresh unread entry: a question dialog and the turn end can ring within
// seconds of each other, and one piece of news deserves one toast.
const dedupeWindow = 30 * time.Second

// Notification is one entry in the notification center: this target (a
// coder or shell) has news. URL is the page the entry links to. Title, when
// set, replaces the generic "Something new in ..." wording everywhere the
// entry surfaces, system targets like the backup use it for a real sentence.
type Notification struct {
	ID         string    `json:"id"`
	TargetID   string    `json:"targetId"`
	TargetName string    `json:"targetName"`
	Title      string    `json:"title,omitempty"`
	Project    string    `json:"project"`
	URL        string    `json:"url"`
	CreatedAt  time.Time `json:"createdAt"`
	Read       bool      `json:"read"`
}

// BackupTarget is the well known target id for finished backup jobs. It is
// no terminal, so the restore prune keeps it alive explicitly and it can
// never collide with the UUID shaped session ids.
const BackupTarget = "backup"

// TargetInfo carries display context resolved at ingest time.
type TargetInfo struct {
	Name    string
	Title   string
	Project string
	URL     string
}

// Resolver looks up display context for a target.
type Resolver func(targetID string) TargetInfo

// Event is one fan-out message to SSE subscribers. Targets carries the ids
// of every target with an unread entry, so pages can mark target lists
// live. Added is set only when a new unread notification was ingested (the
// toast trigger); events without it follow read-state changes.
type Event struct {
	Unread  int           `json:"unread"`
	Targets []string      `json:"targets"`
	Added   *Notification `json:"added,omitempty"`
}

// Service owns the persistent notification list and its subscribers.
// Safe for concurrent use.
type Service struct {
	path     string
	resolver Resolver
	now      func() time.Time

	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// NewService returns a service persisting to path. The resolver may be nil,
// then names fall back to the target identifier.
func NewService(path string, resolver Resolver) *Service {
	return &Service{
		path:     path,
		resolver: resolver,
		now:      time.Now,
		subs:     map[chan Event]struct{}{},
	}
}

// Add ingests one event, collapses older unread entries of the same target,
// and notifies subscribers. A target therefore holds at most one unread
// entry, no matter how many signals fired. Entries always start unread; the
// client marks them read when the target's page is visibly open.
func (s *Service) Add(targetID string) {
	info := TargetInfo{}
	if s.resolver != nil {
		info = s.resolver(targetID)
	}
	name := info.Name
	if name == "" {
		name = shortID(targetID)
	}
	url := info.URL
	if url == "" {
		url = "/coders/" + targetID
	}
	n := Notification{
		ID:         statefile.NewID(),
		TargetID:   targetID,
		TargetName: name,
		Title:      info.Title,
		Project:    info.Project,
		URL:        url,
		CreatedAt:  s.now().UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	for _, existing := range list {
		if !existing.Read && existing.TargetID == targetID && s.now().UTC().Sub(existing.CreatedAt) < dedupeWindow {
			return
		}
	}
	kept := list[:0]
	for _, existing := range list {
		if !existing.Read && existing.TargetID == targetID {
			continue
		}
		kept = append(kept, existing)
	}
	list = append([]Notification{n}, kept...)
	if len(list) > maxStored {
		list = list[:maxStored]
	}
	s.save(list)
	ev := Event{Unread: countUnread(list), Targets: unreadIDs(list), Added: &n}
	s.publishLocked(ev)
}

// List returns the stored notifications, newest first, capped at limit
// (0 means all).
func (s *Service) List(limit int) []Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list
}

// UnreadCount returns how many stored notifications are unread.
func (s *Service) UnreadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return countUnread(s.load())
}

// UnreadTargets returns the ids of every target with an unread
// notification, so lists can mark targets that have news.
func (s *Service) UnreadTargets() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for _, n := range s.load() {
		if !n.Read {
			out[n.TargetID] = true
		}
	}
	return out
}

// MarkRead marks one notification read and reports the new unread count.
func (s *Service) MarkRead(id string) int {
	return s.mark(func(n *Notification) bool { return n.ID == id })
}

// MarkAllRead marks every notification read.
func (s *Service) MarkAllRead() int {
	return s.mark(func(n *Notification) bool { return true })
}

// MarkTargetRead marks every notification of one target read. Called when
// the target's attach page is opened, so seen targets clear themselves.
func (s *Service) MarkTargetRead(targetID string) int {
	return s.mark(func(n *Notification) bool { return n.TargetID == targetID })
}

// PruneTargets drops stored notifications whose target id is not in keep.
// The startup terminal restore calls it for targets that stayed dead through
// the restore pass, their entries would link nowhere forever. Returns how
// many entries were removed.
func (s *Service) PruneTargets(keep map[string]bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	kept := list[:0]
	for _, n := range list {
		if keep[n.TargetID] {
			kept = append(kept, n)
		}
	}
	removed := len(list) - len(kept)
	if removed > 0 {
		s.save(kept)
		s.publishLocked(Event{Unread: countUnread(kept), Targets: unreadIDs(kept)})
	}
	return removed
}

func (s *Service) mark(match func(*Notification) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	changed := false
	for i := range list {
		if !list[i].Read && match(&list[i]) {
			list[i].Read = true
			changed = true
		}
	}
	unread := countUnread(list)
	if changed {
		s.save(list)
		s.publishLocked(Event{Unread: unread, Targets: unreadIDs(list)})
	}
	return unread
}

// UnreadEvent returns the current unread state as a fan-out event, used as
// the initial SSE payload.
func (s *Service) UnreadEvent() Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	return Event{Unread: countUnread(list), Targets: unreadIDs(list)}
}

// Subscribe registers a fan-out channel. The returned cancel func must be
// called when the subscriber goes away.
func (s *Service) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 16)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
	return ch, cancel
}

func (s *Service) publishLocked(ev Event) {
	for ch := range s.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (s *Service) load() []Notification {
	var list []Notification
	statefile.Load(s.path, &list)
	return list
}

func (s *Service) save(list []Notification) {
	statefile.Save(s.path, 0o644, list)
}

func countUnread(list []Notification) int {
	unread := 0
	for _, n := range list {
		if !n.Read {
			unread++
		}
	}
	return unread
}

func unreadIDs(list []Notification) []string {
	ids := make([]string, 0, len(list))
	for _, n := range list {
		if !n.Read {
			ids = append(ids, n.TargetID)
		}
	}
	return ids
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
