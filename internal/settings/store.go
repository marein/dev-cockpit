// Package settings persists small cross-device user settings as a JSON map
// (key to value) in the state directory. The file is read and written through
// on every call, so several serve processes sharing the state dir see each
// other's changes and a fresh process picks up the latest values.
package settings

import (
	"sync"

	"github.com/local/dev-cockpit/internal/statefile"
)

// Store is the file-backed settings map. Safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
}

// New returns a store backed by path. The file is read on demand, not now.
func New(path string) *Store {
	return &Store{path: path}
}

// Get returns the stored value for key, or "" when absent.
func (s *Store) Get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()[key]
}

// Set stores the value for key, merging into whatever is already on disk.
func (s *Store) Set(key, value string) {
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.load()
	m[key] = value
	s.save(m)
}

func (s *Store) load() map[string]string {
	m := map[string]string{}
	statefile.Load(s.path, &m)
	return m
}

func (s *Store) save(m map[string]string) {
	statefile.Save(s.path, 0o644, m)
}
