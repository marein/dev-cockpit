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

// idPattern guards every id that becomes a path component. Conversation ids are UUID
// shaped, so this is a whitelist, not an escape.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// ValidID reports whether an id is usable as a conversation identifier.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Store persists the conversation index and one file per transcript:
//
//	<index-path>                                     index, no messages
//	<dir>/<conversation-id>.json                     one transcript
//	<upload-root>/<conversation-id>/<name>           what a prompt carried
//
// Index and transcripts go through internal/statefile, so reads pick up outside
// changes, writes are atomic and a corrupt file is quarantined instead of
// overwritten.
type Store struct {
	indexPath  string
	dir        string
	uploadRoot string
	mu         sync.Mutex
}

// NewStore returns the assistant's store for a state directory, the layout
// Paths describes.
func NewStore(stateDir string) *Store { return NewStoreAt(Paths(stateDir)) }

// NewStoreAt returns a store over explicit paths, so the layout stays in one
// place: the caller decides where index, transcripts and uploads live.
func NewStoreAt(indexPath, dir, uploadRoot string) *Store {
	return &Store{indexPath: indexPath, dir: dir, uploadRoot: uploadRoot}
}

// UploadRoot is the directory holding one upload subdirectory per conversation.
func (s *Store) UploadRoot() string { return s.uploadRoot }

// UploadDir is where the uploads of one conversation live. It is created on
// demand by the upload path, never here.
func (s *Store) UploadDir(id string) (string, error) {
	if !ValidID(id) {
		return "", errors.New("Invalid conversation.")
	}
	return filepath.Join(s.uploadRoot, id), nil
}

// List returns the index, newest activity first.
func (s *Store) List() []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list()
}

func (s *Store) list() []Summary {
	var out []Summary
	statefile.Load(s.indexPath, &out)
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastMessageAt.After(out[j].LastMessageAt) })
	return out
}

// Load reads one transcript. A missing or unreadable transcript reports false
// without touching the index: the entry stays visible and recoverable instead
// of disappearing behind a silent self-heal.
func (s *Store) Load(id string) (Conversation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(id)
}

func (s *Store) load(id string) (Conversation, bool) {
	if !ValidID(id) {
		return Conversation{}, false
	}
	var c Conversation
	statefile.Load(s.transcriptPath(id), &c)
	if c.ID == "" {
		return Conversation{}, false
	}
	return c, true
}

// Save writes the transcript and refreshes its index entry.
func (s *Store) Save(c Conversation) {
	if !ValidID(c.ID) {
		log.Printf("assistant: refusing to save invalid conversation id %q", c.ID)
		return
	}
	c.summarize()
	s.mu.Lock()
	defer s.mu.Unlock()
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
		index = append(index, c.Summary)
	}
	sort.SliceStable(index, func(i, j int) bool { return index[i].LastMessageAt.After(index[j].LastMessageAt) })
	statefile.Save(s.indexPath, 0o600, index)
}

// Delete removes the transcript and its index entry.
func (s *Store) Delete(id string) error {
	if !ValidID(id) {
		return errors.New("Invalid conversation.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.transcriptPath(id)); err != nil && !os.IsNotExist(err) {
		log.Printf("assistant: remove transcript %s: %v", id, err)
		return errors.New("The conversation could not be deleted.")
	}
	// The uploads go with the transcript. A failure here is logged and does not
	// keep the conversation alive: the index entry is what the user sees.
	if err := os.RemoveAll(filepath.Join(s.uploadRoot, id)); err != nil {
		log.Printf("assistant: remove uploads of %s: %v", id, err)
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

func (s *Store) transcriptPath(id string) string {
	return filepath.Join(s.dir, id+".json")
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
