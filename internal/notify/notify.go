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
	"strings"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/statefile"
)

const maxStored = 100

// dedupeWindow swallows follow-up signals for a target that already has a
// fresh unread entry: a question dialog and the turn end can ring within
// seconds of each other, and one piece of news deserves one toast. News the
// resolver marked urgent passes it, see TargetInfo.Urgent: what says the
// opposite of the entry standing there is no follow-up.
const dedupeWindow = 30 * time.Second

// Notification is one entry in the notification center: this target (a
// coder or shell) has news. URL is the page the entry links to. Every entry
// is written as two lines: Title says what happened, Detail says which one it
// happened in. Title, when set, replaces the generic "Something new in ..."
// wording everywhere the entry surfaces; an entry without one is a target
// nobody could resolve or an entry from an older build, and falls back to it.
type Notification struct {
	ID         string `json:"id"`
	TargetID   string `json:"targetId"`
	TargetName string `json:"targetName"`
	Title      string `json:"title,omitempty"`
	// Detail is the line below the title, shown where the project would
	// stand (list, toast, push body). A title that only says that something
	// happened leaves the reader guessing what about, so this names it: the
	// coder, shell, job or backup in quotes plus its project, or the first
	// words of the assistant's answer.
	Detail    string    `json:"detail,omitempty"`
	Project   string    `json:"project"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"createdAt"`
	Read      bool      `json:"read"`
	// Silent marks an entry that was written read from the start, see
	// SetSilent. It is what lets the next silent entry of the same target
	// replace this one without touching the lines a person really read.
	Silent bool `json:"silent,omitempty"`
}

// BackupTarget is the well known target id for finished backup jobs. It is
// no terminal, so the restore prune keeps it alive explicitly and it can
// never collide with the UUID shaped session ids.
const BackupTarget = "backup"

// DockerTargetPrefix names the targets of finished docker compose runs, one
// per project (`docker:<project>`), under the same rules as BackupTarget.
//
// One target per project and not one for all of docker, because a target is
// what holds at most one unread entry: bringing two projects down at the same
// moment is two pieces of news and has to read as two, while a down and an up
// of the same project seconds apart is one and still collapses.
const DockerTargetPrefix = "docker:"

// DockerTarget is the target id one project's compose runs report under.
func DockerTarget(project string) string { return DockerTargetPrefix + project }

// IsDockerTarget reports whether an id is one of them.
func IsDockerTarget(targetID string) bool { return strings.HasPrefix(targetID, DockerTargetPrefix) }

// DockerTargetProject answers the project such an id names.
func DockerTargetProject(targetID string) string {
	return strings.TrimPrefix(targetID, DockerTargetPrefix)
}

// GitPromptTargetPrefix names the targets of standing askpass questions, one
// per project (`gitprompt:<project>`), under the same rules as the docker
// targets: per project because a target holds at most one unread entry, and
// two projects asking at the same moment are two pieces of news.
//
// The entry is how a question reaches somebody with no page open at all, the
// phone in the pocket: the unread entry rides the push channels, and any page
// it opens shows the app-wide dialog. A question that is answered, cancelled
// or timed out marks its target read again, so the bell never claims a
// question that no longer stands.
const GitPromptTargetPrefix = "gitprompt:"

// GitPromptTarget is the target id one project's standing questions report
// under.
func GitPromptTarget(project string) string { return GitPromptTargetPrefix + project }

// IsGitPromptTarget reports whether an id is one of them.
func IsGitPromptTarget(targetID string) bool {
	return strings.HasPrefix(targetID, GitPromptTargetPrefix)
}

// GitPromptTargetProject answers the project such an id names.
func GitPromptTargetProject(targetID string) string {
	return strings.TrimPrefix(targetID, GitPromptTargetPrefix)
}

// TargetInfo carries display context resolved at ingest time.
type TargetInfo struct {
	Name    string
	Title   string
	Detail  string
	Project string
	URL     string
	// Urgent marks news the dedupe window must not swallow: a compose run
	// that failed right after one that went through says the opposite of the
	// fresh unread entry standing there, and that word is owed. The resolver
	// decides at ingest, this service still classifies nothing.
	Urgent bool
}

// Resolver looks up display context for a target.
type Resolver func(targetID string) TargetInfo

// Event is one fan-out message to SSE subscribers. Targets carries the ids
// of every target with an unread entry, so pages can mark target lists
// live. Added is set only when a new unread notification was ingested (the
// toast trigger); events without it follow a read-state change or an entry
// that was written read.
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
	// signal hears every ingested signal, see SetSignal.
	signal func(targetID string)
	// silent decides whether a target's news is written quietly, see SetSilent.
	silent func(targetID string) bool

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

// SetSignal installs a listener that hears every signal this service ingests,
// before anything is collapsed or deduplicated. It exists because the entries
// here are for a person, with their read state and their quiet window, while
// another consumer needs the raw fact that a target reported. This service does
// not know what the listener does with it and must not: it classifies nothing.
// Set it before the pollers start.
func (s *Service) SetSignal(listen func(targetID string)) { s.signal = listen }

// SetSilent installs the predicate that decides whether a target's news is
// written read from the start. It exists for the one case where somebody else
// is already looking at that target: the assistant steers a job on it, it looks
// into that coder when it reports, and its report is the message that reaches
// the user. Ringing for the coder as well would say the same thing twice, and
// the raw one would say it first and with less to say. The entry is still
// written so the history stays complete, and it surfaces nowhere: it counts as
// no unread, marks neither coder nor project, and raises no toast, no jingle
// and no push.
//
// Like SetSignal it is set after construction, because this service classifies
// nothing and must not learn what a job is. Set it before the pollers start.
func (s *Service) SetSilent(quiet func(targetID string) bool) { s.silent = quiet }

// Signal is one ingested signal: the notification for the person, and the raw
// fact for whoever else listens. Every source of news goes through here, the
// inbox files a coder's hooks drop and the bell a pane rings.
func (s *Service) Signal(targetID string) {
	s.Add(targetID)
	if s.signal != nil {
		s.signal(targetID)
	}
}

// Add ingests one event, collapses older unread entries of the same target,
// and notifies subscribers. A target therefore holds at most one unread
// entry, no matter how many signals fired. Entries start unread unless the
// silent predicate claims the target; the client marks them read when the
// target's page is visibly open.
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
	silent := s.silent != nil && s.silent(targetID)
	n := Notification{
		ID:         statefile.NewID(),
		TargetID:   targetID,
		TargetName: name,
		Title:      info.Title,
		Detail:     info.Detail,
		Project:    info.Project,
		URL:        url,
		CreatedAt:  s.now().UTC(),
		Read:       silent,
		Silent:     silent,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	if !silent && !info.Urgent {
		for _, existing := range list {
			if !existing.Read && existing.TargetID == targetID && s.now().UTC().Sub(existing.CreatedAt) < dedupeWindow {
				return
			}
		}
	}
	kept := list[:0]
	for _, existing := range list {
		if silent {
			// A silent entry is history and nothing else, so it leaves the
			// lines a person still has to read alone and replaces only the
			// target's previous silent one. Without that a steered coder that
			// reports ten times would write ten read lines and push real
			// history off the end of the list.
			if existing.Silent && existing.TargetID == targetID {
				continue
			}
		} else if !existing.Read && existing.TargetID == targetID {
			continue
		}
		kept = append(kept, existing)
	}
	list = append([]Notification{n}, kept...)
	if len(list) > maxStored {
		list = list[:maxStored]
	}
	s.save(list)
	ev := Event{Unread: countUnread(list), Targets: unreadIDs(list)}
	if !silent {
		// Added is the toast trigger on the client and what the push channels
		// deliver on, so the silent entry carries none: it only joins the list.
		ev.Added = &n
	}
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
// the restore pass, their entries would link nowhere forever. A compose run's
// target is kept whatever the caller says, and a git question's with it: both
// name a project and not a terminal, so no terminal pass can know them.
// Returns how many entries were removed.
func (s *Service) PruneTargets(keep map[string]bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	kept := list[:0]
	for _, n := range list {
		if keep[n.TargetID] || IsDockerTarget(n.TargetID) || IsGitPromptTarget(n.TargetID) {
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
