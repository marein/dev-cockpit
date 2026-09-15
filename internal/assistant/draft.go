package assistant

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// draftFileName is where the unsent message of one assistant lives, next to
// its transcript and its jobs.
const draftFileName = "draft.json"

// DraftStore persists the unsent message of one assistant. It is a file of its
// own, like the jobs, for one reason: a draft is saved while somebody types and
// a transcript grows without bound, so writing the draft into the transcript
// meant rewriting the whole thread, and the index entry with it, for every
// pause in the typing. Here a save touches a few hundred bytes and wakes
// nothing that reads the index.
//
// Read through on every call like every other state file, so a draft typed on
// one device is what the next one reads.
type DraftStore struct {
	dir  string
	path string
	mu   sync.Mutex
}

// NewDraftStore returns the store for one instance's directory.
func NewDraftStore(dir string) *DraftStore {
	return &DraftStore{dir: dir, path: filepath.Join(dir, draftFileName)}
}

// Get is what the composer holds right now.
func (s *DraftStore) Get() Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adoptLegacyLocked()
	var d Draft
	statefile.Load(s.path, &d)
	return d
}

// Save stores a draft and says whether that changed anything. A repeated save
// writes nothing and announces nothing, so a composer that is looked at but
// not typed in wakes no other device.
func (s *DraftStore) Save(d Draft) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adoptLegacyLocked()
	var stored Draft
	statefile.Load(s.path, &stored)
	if stored.Same(d.Text, d.Attachments) {
		return false
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return false
	}
	statefile.Save(s.path, 0o600, d)
	return true
}

// legacyTranscript reads the one field a transcript written before the draft
// moved out of it still carries.
// TODO(v2.0.0): drop it together with adoptLegacyLocked.
type legacyTranscript struct {
	Draft Draft `json:"draft,omitempty"`
}

// adoptLegacyLocked moves the draft a transcript still holds into the file that
// owns it now. It runs once per assistant, on the first read or save: the
// missing file is what says the move has not happened, and the file is written
// even when there was nothing to carry over, so the transcript is opened for
// this exactly once. What stays behind in the transcript is a key nothing reads
// any more, and the next turn that saves the thread writes it out.
// TODO(v2.0.0): drop, together with the key in the transcripts.
func (s *DraftStore) adoptLegacyLocked() {
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		return
	}
	var old legacyTranscript
	statefile.Load(filepath.Join(s.dir, transcriptFileName), &old)
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return
	}
	statefile.Save(s.path, 0o600, old.Draft)
}

// Drafts is every assistant's draft at once: one store per instance, remembered
// only to keep its lock, never its contents. It is the same shape as the jobs
// registry, and for the same reason: the file is the state, the store is only
// the door to it.
type Drafts struct {
	store *Store

	mu     sync.Mutex
	stores map[string]*DraftStore
}

// NewDrafts wires the registry over the index of instances.
func NewDrafts(store *Store) *Drafts {
	return &Drafts{store: store, stores: map[string]*DraftStore{}}
}

// Of is the draft store of one assistant.
func (d *Drafts) Of(id string) *DraftStore {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, ok := d.stores[id]; ok {
		return s
	}
	s := NewDraftStore(d.store.InstanceDir(id))
	d.stores[id] = s
	return s
}

// Forget drops the store of an assistant that is gone, so a long running
// cockpit does not keep a lock per assistant it ever had. The file went with
// the instance directory.
func (d *Drafts) Forget(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.stores, id)
}

// clearDraft empties the draft of an assistant whose composer just sent what it
// held. The empty draft keeps a timestamp: the other devices decide by it, and
// a cleared draft without one would look older than what they still hold. The
// send announces itself anyway, and the pages read the draft again with it.
func (s *Service) clearDraft(id string, now time.Time) {
	s.drafts.Of(id).Save(Draft{UpdatedAt: now})
}
