package assistant

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// idPattern guards every id that becomes a path component. Instance ids are UUID
// shaped, so this is a whitelist, not an escape.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// ValidID reports whether an id is usable as an instance identifier.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// transcriptFileName is the thread of one assistant inside its directory. The
// draft store names it too, to carry an old draft out of it once.
const transcriptFileName = "transcript.json"

// Store persists the index of instances and one directory per instance:
//
//	<index-path>                                          index, no messages
//	<dir>/<instance-id>/transcript.json                   one transcript
//	<dir>/<instance-id>/jobs.json                         the jobs it steers
//	<dir>/<instance-id>/workspace                         where its turns run
//	<dir>/<instance-id>/workspace/user-upload/<name>      what a prompt carried
//	<dir>/<instance-id>/workspace/assistant-files/<name>  what the instance wrote
//
// An instance owning a directory instead of a file is what lets everything that
// belongs to one conversation sit together: the transcript, the jobs it steers
// and the workspace it works in are deleted, backed up and reasoned about as
// one thing.
//
// Index and transcripts go through internal/statefile, so reads pick up outside
// changes, writes are atomic and a corrupt file is quarantined instead of
// overwritten.
type Store struct {
	indexPath string
	dir       string
	mu        sync.Mutex
}

// NewStore returns the assistant's store for a state directory, the layout
// Paths describes.
func NewStore(stateDir string) *Store {
	index, instances, _ := Paths(stateDir)
	return NewStoreAt(index, instances)
}

// NewStoreAt returns a store over explicit paths, so the layout stays in one
// place: the caller decides where the index and the instances live.
func NewStoreAt(indexPath, dir string) *Store {
	return &Store{indexPath: indexPath, dir: dir}
}

// WorkspaceDir is the workspace of one instance, the directory its turns run
// in and the one its uploads and files sit under.
func (s *Store) WorkspaceDir(id string) string {
	return filepath.Join(s.InstanceDir(id), workspaceDirName)
}

// UploadDir is where the uploads of one instance live. It is created on
// demand by the upload path, never here.
func (s *Store) UploadDir(id string) (string, error) {
	if !ValidID(id) {
		return "", errors.New("Invalid assistant.")
	}
	return filepath.Join(s.WorkspaceDir(id), uploadDirName), nil
}

// List returns the index in the order the user put it in. The file is the
// order: the index array is written in the order the list shows, so nothing
// has to carry a position and nothing can disagree with anything.
func (s *Store) List() []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list()
}

func (s *Store) list() []Summary {
	var out []Summary
	statefile.Load(s.indexPath, &out)
	return out
}

// Reorder writes the index in the order the ids name. A posted order is read
// as a permutation of the places those assistants already hold, the way the
// tab strip reads one: the slots stay, only who sits in which changes, so an
// assistant the post never saw keeps its exact seat instead of being pushed
// aside. Ids the index does not carry are dropped, duplicates count once, and
// a post that resolves to nothing writes nothing.
func (s *Store) Reorder(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.list()
	at := make(map[string]int, len(index))
	for i, entry := range index {
		at[entry.ID] = i
	}
	moved := make([]Summary, 0, len(ids))
	slots := make([]int, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		i, ok := at[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		moved = append(moved, index[i])
		slots = append(slots, i)
	}
	if len(moved) == 0 {
		return
	}
	sort.Ints(slots)
	for i, slot := range slots {
		index[slot] = moved[i]
	}
	statefile.Save(s.indexPath, 0o600, index)
}

// Load reads one transcript. A missing or unreadable transcript reports false
// without touching the index: the entry stays visible and recoverable instead
// of disappearing behind a silent self-heal.
func (s *Store) Load(id string) (Instance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(id)
}

func (s *Store) load(id string) (Instance, bool) {
	if !ValidID(id) {
		return Instance{}, false
	}
	var c Instance
	statefile.Load(s.transcriptPath(id), &c)
	if c.ID == "" {
		return Instance{}, false
	}
	return c, true
}

// Save writes the transcript and refreshes its index entry.
func (s *Store) Save(c Instance) {
	if !ValidID(c.ID) {
		log.Printf("assistant: refusing to save invalid instance id %q", c.ID)
		return
	}
	c.summarize()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.InstanceDir(c.ID), 0o700); err != nil {
		log.Printf("assistant: create instance directory for %s: %v", c.ID, err)
		return
	}
	statefile.Save(s.transcriptPath(c.ID), 0o600, c)
	index := s.list()
	replaced := false
	for i := range index {
		if index[i].ID == c.ID {
			index[i] = c.Summary
			replaced = true
			break
		}
	}
	if !replaced {
		// A new assistant goes to the top. Every other one keeps the place the
		// user dragged it to, and the one just made is the one they are looking
		// at, so it is the only entry that may take a place nobody gave it.
		index = append([]Summary{c.Summary}, index...)
	}
	statefile.Save(s.indexPath, 0o600, index)
}

// Delete removes everything the instance owns and its index entry: the
// transcript, the jobs it steered, and its workspace with the files a prompt
// carried and the files the instance itself wrote. Deleting an assistant is
// deleting the whole of it, and the workspace is its as much as the transcript
// is: what is left of an assistant nobody can open is disk nobody can reach,
// and the answers that linked those files are gone with it.
func (s *Store) Delete(id string) error {
	if !ValidID(id) {
		return errors.New("Invalid assistant.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.RemoveAll(s.InstanceDir(id)); err != nil {
		log.Printf("assistant: remove instance directory %s: %v", id, err)
		return errors.New("The assistant could not be deleted.")
	}
	index := s.list()
	out := index[:0]
	for _, entry := range index {
		if entry.ID != id {
			out = append(out, entry)
		}
	}
	statefile.Save(s.indexPath, 0o600, out)
	return nil
}

// InstanceDir is everything one instance owns on disk. Exported because the
// jobs of an instance live in it and are stored by a store of their own.
func (s *Store) InstanceDir(id string) string { return filepath.Join(s.dir, id) }

func (s *Store) transcriptPath(id string) string {
	return filepath.Join(s.InstanceDir(id), transcriptFileName)
}

// preview shortens an assistant answer for the list page.
func preview(content string) string {
	text := strings.TrimSpace(content)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return truncateRunes(text, 140)
}

// truncateRunes cuts to a rune boundary, so a title or preview never splits a
// multi byte character.
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	count := 0
	for i := range s {
		if count == max {
			return strings.TrimSpace(s[:i]) + "…"
		}
		count++
	}
	return s
}
