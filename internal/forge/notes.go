package forge

import (
	"context"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// notesKept is how long the record of a change request whose notes moved no
// more stands. One still read is written again after half of it, so only a
// change request nobody reads any more is forgotten.
const notesKept = 30 * 24 * time.Hour

// ReviewNote is a note on the change request of a branch the poller keeps
// that a read of its review threads saw first: the branch, the change
// request, the thread the note stands in with all of its notes, and the
// note.
type ReviewNote struct {
	Watch  Watch
	Change Change
	Thread Thread
	Note   Note
}

// notesFile holds, by project, the change requests whose review threads the
// poller read there.
type notesFile struct {
	Projects map[string][]notesRecord `json:"projects"`
}

// notesRecord is a change request by where it is and its number, the ids of
// its notes the poller read, sorted, and when that last moved.
type notesRecord struct {
	Host   string    `json:"host"`
	Path   string    `json:"path"`
	Change int       `json:"change"`
	Notes  []string  `json:"notes"`
	At     time.Time `json:"at"`
}

func (s *Service) notesPath() string {
	return filepath.Join(s.dir, "notes.json")
}

// OnReviewNote sets what hears of a note on the change request of a branch
// the poller keeps, once per note, also across a restart: one somebody other
// than the account of the host's token wrote after the poller first read the
// review threads of that change request. The poller reads them while
// somebody hears of them.
func (s *Service) OnReviewNote(fn func(ReviewNote)) {
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	s.onNote = fn
}

func (s *Service) hearsNotes() bool {
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	return s.onNote != nil
}

// noteNotes keeps in the record of w's project the notes a read of the
// review threads of change found, and publishes those the record did not
// hold yet that self did not write, oldest first, once the record holds
// them: a file it cannot read or write publishes nothing, so no note is ever
// published twice. A change request the record does not hold is read for the
// first time, and its notes were written before anybody watched. A partial
// read forgets no note it did not reach.
func (s *Service) noteNotes(w Watch, change Change, review Review, self string) {
	now := s.poll.now()
	s.notesMu.Lock()
	var f notesFile
	if err := statefile.Read(s.notesPath(), &f); err != nil {
		s.notesMu.Unlock()
		log.Printf("forge: %v", err)
		return
	}
	records := f.Projects[w.Project]
	at := slices.IndexFunc(records, func(r notesRecord) bool {
		return r.Host == w.Binding.Host.ID && r.Path == w.Binding.Path && r.Change == change.Number
	})
	seen := map[string]bool{}
	if at >= 0 {
		for _, id := range records[at].Notes {
			seen[id] = true
		}
	}
	var ids []string
	var fresh []ReviewNote
	for _, t := range slices.Concat(review.Diff, review.General) {
		for _, n := range t.Notes {
			ids = append(ids, n.ID)
			if at >= 0 && !seen[n.ID] && (self == "" || !strings.EqualFold(n.Author, self)) {
				fresh = append(fresh, ReviewNote{Watch: w, Change: change, Thread: t, Note: n})
			}
		}
	}
	if review.Partial && at >= 0 {
		ids = append(ids, records[at].Notes...)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if at >= 0 && slices.Equal(ids, records[at].Notes) && now.Sub(records[at].At) < notesKept/2 {
		s.notesMu.Unlock()
		return
	}
	record := notesRecord{Host: w.Binding.Host.ID, Path: w.Binding.Path, Change: change.Number, Notes: ids, At: now}
	if at >= 0 {
		records[at] = record
	} else {
		records = append(records, record)
	}
	if f.Projects == nil {
		f.Projects = map[string][]notesRecord{}
	}
	f.Projects[w.Project] = records
	for project, list := range f.Projects {
		list = slices.DeleteFunc(list, func(r notesRecord) bool { return now.Sub(r.At) > notesKept })
		if len(list) == 0 {
			delete(f.Projects, project)
			continue
		}
		f.Projects[project] = list
	}
	if err := s.write(s.notesPath(), f); err != nil {
		s.notesMu.Unlock()
		log.Printf("forge: the review notes could not be saved: %v", err)
		return
	}
	s.notesMu.Unlock()
	s.reviewMu.Lock()
	hear := s.onNote
	s.reviewMu.Unlock()
	if hear == nil {
		return
	}
	slices.SortStableFunc(fresh, func(a, b ReviewNote) int { return a.Note.Created.Compare(b.Note.Created) })
	for _, n := range fresh {
		hear(n)
	}
}

// forgetNotes takes away the record of a project that is gone, so one made
// again under its name reads its change requests for the first time. A file
// it cannot read stays as it is.
func (s *Service) forgetNotes(project string) {
	s.notesMu.Lock()
	defer s.notesMu.Unlock()
	var f notesFile
	if err := statefile.Read(s.notesPath(), &f); err != nil {
		log.Printf("forge: %v, the review notes of %s stay", err, project)
		return
	}
	if _, ok := f.Projects[project]; !ok {
		return
	}
	delete(f.Projects, project)
	if err := s.write(s.notesPath(), f); err != nil {
		log.Printf("forge: the review notes of %s could not be taken away: %v", project, err)
	}
}

// account is who a host's token belongs to as a read in a turn of the host
// found, or why it could not tell.
type account struct {
	turn   int
	name   string
	failed string
}

// account answers who the token of h belongs to, asked of the forge once per
// turn of the host, and whether it could tell: a new token is a new turn. A
// failure is logged once per turn and reason, and asked again by the next
// read.
func (s *Service) account(ctx context.Context, h Host, turn int) (string, bool) {
	p := s.poll
	p.mu.Lock()
	known, ok := p.accounts[h.ID]
	p.mu.Unlock()
	ok = ok && known.turn == turn
	if ok && known.failed == "" {
		return known.name, true
	}
	conn, err := s.connect(ctx, h)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.turns[h.ID] != turn {
		return "", false
	}
	if err != nil {
		if !ok || known.failed != err.Error() {
			log.Printf("forge: who the token of %s belongs to could not be read, its new review notes wait: %v", h.WebURL, err)
		}
		p.accounts[h.ID] = account{turn: turn, failed: err.Error()}
		return "", false
	}
	p.accounts[h.ID] = account{turn: turn, name: conn.Account}
	return conn.Account, true
}
