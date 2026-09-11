// Package recent persists last-used timestamps for named items so a list can
// offer a "recently used" sort that survives restarts. State is a small JSON map
// (name to unix seconds), read and written through the file on every call so a
// fresh process picks up the latest entries. The caller picks the file path, so
// several independent stores can coexist.
package recent

import (
	"sort"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// Store is the file-backed last-used timestamp map. Safe for concurrent use.
// A zero path disables persistence: every operation becomes a no-op.
type Store struct {
	path string
	// keep bounds the file to that many newest entries, 0 keeps every one.
	// See NewCapped.
	keep int
	mu   sync.Mutex
	now  func() time.Time
}

// New returns a store backed by path. The file is read on demand, not now.
func New(path string) *Store {
	return &Store{path: path, now: time.Now}
}

// NewCapped returns a store that keeps only the keep newest entries, dropping
// the older ones on every write. A keep of 0 or less is unbounded, like New.
// A store that is read back as "where was I, and before that" needs a handful
// of names and nothing else, so its file cannot grow with every name that was
// ever touched. A store a list sorts itself by must stay unbounded: there a
// dropped entry is an item that silently loses its place.
func NewCapped(path string, keep int) *Store {
	s := New(path)
	s.keep = keep
	return s
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
	s.save(s.trim(m))
}

// Times returns the last-used timestamps keyed by project name.
func (s *Store) Times() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Names returns the stored names newest first, so a caller can walk them until
// one is still valid.
func (s *Store) Names() []string {
	return order(s.Times())
}

// trim drops everything past the newest keep entries.
func (s *Store) trim(m map[string]int64) map[string]int64 {
	if s.keep <= 0 || len(m) <= s.keep {
		return m
	}
	for _, name := range order(m)[s.keep:] {
		delete(m, name)
	}
	return m
}

// order sorts the names of m newest first. Two touches inside one second carry
// the same timestamp, so the name decides between them: whichever way it falls,
// it falls the same way on every read.
func order(m map[string]int64) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if m[names[i]] != m[names[j]] {
			return m[names[i]] > m[names[j]]
		}
		return names[i] < names[j]
	})
	return names
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
