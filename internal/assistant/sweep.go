package assistant

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
)

// SweepOrphans removes what belongs to no assistant any more: an instance
// directory the index does not list, with the transcript, the jobs and the
// workspace in it. It is invisible from every surface and nothing else ever
// collects it, so without this it stays on disk forever, one directory per
// assistant that was ever lost. Deleting an assistant takes its directory with
// it; this is for the ones that never got that far, an index entry that
// vanished under a crash or an older version.
//
// It runs once at startup, and it runs only over an index it could actually
// read. The index is read here and not through the store on purpose: a corrupt
// state file is quarantined as <path>.broken and reads as absent afterwards, so
// a sweep that trusted an empty read would delete every assistant on the disk
// the one time the index cannot be parsed. A missing, empty or unreadable index
// touches nothing. That costs nothing but a sweep skipped, and the alternative
// costs everything.
func (s *Store) SweepOrphans() {
	known, ok := s.indexIDs()
	if !ok {
		return
	}
	removed := 0
	// An instance directory is everything one assistant owns, so an unknown one
	// is a whole assistant nobody can open.
	for _, name := range orphanDirs(s.dir, known) {
		if err := os.RemoveAll(filepath.Join(s.dir, name)); err != nil {
			log.Printf("assistant: remove the instance directory %s nobody knows: %v", name, err)
			continue
		}
		removed++
	}
	if removed > 0 {
		log.Printf("assistant: removed %d instance directory/ies no assistant owns", removed)
	}
}

// indexIDs answers the assistants the index lists, and whether it was read at
// all. False is every reason not to sweep: no file, an empty file, a file that
// cannot be read or does not parse, and an index that lists nothing.
func (s *Store) indexIDs() (map[string]bool, bool) {
	data, err := os.ReadFile(s.indexPath)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	var index []Summary
	if err := json.Unmarshal(data, &index); err != nil || len(index) == 0 {
		return nil, false
	}
	known := make(map[string]bool, len(index))
	for _, entry := range index {
		known[entry.ID] = true
	}
	return known, true
}

// orphanDirs are the subdirectories of root that no known id claims.
func orphanDirs(root string, known map[string]bool) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if !entry.IsDir() || known[entry.Name()] {
			continue
		}
		out = append(out, entry.Name())
	}
	return out
}

// Gone reports an assistant whose directory is not on disk, which no
// assistant that lives is without. A transcript that cannot be read leaves
// the directory standing, so it never reads as gone, and neither does a
// directory the stat fails on for any other reason.
func (s *Store) Gone(id string) bool {
	if !ValidID(id) {
		return true
	}
	_, err := os.Lstat(s.InstanceDir(id))
	return errors.Is(err, os.ErrNotExist)
}

// Gone reports an assistant that is deleted for certain, however it went,
// and not one that only cannot be read right now.
func (s *Service) Gone(id string) bool { return s.store.Gone(id) }
