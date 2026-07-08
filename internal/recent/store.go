// Package recent persists last-used timestamps for named items so a list can
// offer a "recently used" sort that survives restarts. State is a small JSON map
// (name to unix seconds), read and written through the file on every call so a
// fresh process picks up the latest entries. The caller picks the file path, so
// several independent stores can coexist.
package recent

import (
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/statefile"
)

// Store is the file-backed last-used timestamp map. Safe for concurrent use.
// A zero path disables persistence: every operation becomes a no-op.
type Store struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

// New returns a store backed by path. The file is read on demand, not now.
func New(path string) *Store {
	return &Store{path: path, now: time.Now}
}

// Touch records the current time as the last-used time for the named project,
// merging into whatever is already on disk.
func (s *Store) Touch(name string) {
	if name == "" || s.path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.load()
	m[name] = s.now().Unix()
	s.save(m)
}

// Times returns the last-used timestamps keyed by project name.
func (s *Store) Times() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() map[string]int64 {
	m := map[string]int64{}
	if s.path == "" {
		return m
	}
	statefile.Load(s.path, &m)
	return m
}

func (s *Store) save(m map[string]int64) {
	statefile.Save(s.path, 0o644, m)
}
