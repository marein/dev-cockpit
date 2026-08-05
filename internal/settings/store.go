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

// Lookup returns the stored value for key and whether it is there at all. A
// setting whose empty value is a real choice, such as a list someone emptied on
// purpose, cannot tell that apart from never having been set with Get alone.
func (s *Store) Lookup(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.load()[key]
	return value, ok
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

// Delete takes the key out of the file, which is not the same as storing an
// empty value: a setting whose empty value is a real choice reads as set
// afterwards, and Lookup is what tells the two apart. Removing it is what puts
// a setting back to "never answered", so whatever the code calls its default
// applies again, including a default a later version changes.
func (s *Store) Delete(key string) {
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.load()
	if _, ok := m[key]; !ok {
		return
	}
	delete(m, key)
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
