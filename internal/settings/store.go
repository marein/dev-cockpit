// Package settings persists small cross-device user settings as a JSON map
// (key to value) in the state directory. The file is read and written through
// on every call, so several serve processes sharing the state dir see each
// other's changes and a fresh process picks up the latest values.
package settings

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
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
	if data, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func (s *Store) save(m map[string]string) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		log.Printf("settings: create state dir: %v", err)
		return
	}
	data, err := json.Marshal(m)
	if err != nil {
		log.Printf("settings: marshal state: %v", err)
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("settings: write state: %v", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		log.Printf("settings: replace state: %v", err)
	}
}
