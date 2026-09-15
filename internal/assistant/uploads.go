package assistant

import (
	"log"
	"os"
	"path/filepath"
	"time"
)

// ReapUploads removes uploads nothing points at, neither a message nor a draft.
// A file is stored the moment it is picked, before the message that carries it
// exists, so a composer that is closed instead of sent leaves it behind and
// nothing else ever collects it: deleting an assistant takes its whole
// directory, and a sent file is referenced forever.
//
// grace keeps a file that is waiting for its message: only uploads older than
// that are candidates, so a reap can never race a composer the user is still
// filling.
func (s *Service) ReapUploads(grace time.Duration) {
	cutoff := s.now().Add(-grace)
	removed := 0
	for _, entry := range s.store.List() {
		dir, err := s.store.UploadDir(entry.ID)
		if err != nil {
			continue
		}
		removed += s.reapInstanceUploads(dir, entry.ID, cutoff)
	}
	if removed > 0 {
		log.Printf("assistant: removed %d unsent upload(s)", removed)
	}
}

// reapInstanceUploads drops the unreferenced uploads of one instance.
func (s *Service) reapInstanceUploads(dir, id string, cutoff time.Time) int {
	files, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	c, ok := s.store.Load(id)
	if !ok {
		// The index knows the instance but its transcript is unreadable. Keeping
		// the files costs a little disk, deleting them could throw away what
		// a recovered transcript still points at.
		return 0
	}
	referenced := map[string]bool{}
	for _, m := range c.Messages {
		for _, a := range m.Attachments {
			referenced[filepath.Base(a.Path)] = true
		}
	}
	// A draft is a message that was not sent yet, and its files are exactly as
	// referenced as a sent one's. A composer can stand open for a day, so the
	// grace period alone would not save them: reaping them leaves a draft
	// pointing at files that are gone, and sending it fails on a file the user
	// picked and can still see. It is a file of its own, so it is read from
	// there and never from the thread.
	for _, a := range s.drafts.Of(id).Get().Attachments {
		referenced[filepath.Base(a.Path)] = true
	}
	removed := 0
	for _, file := range files {
		if file.IsDir() || referenced[file.Name()] {
			continue
		}
		info, err := file.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, file.Name())); err == nil {
			removed++
		}
	}
	return removed
}

// RunUploadReaper reaps on an interval until the process ends. Never returns,
// run it on a goroutine.
func (s *Service) RunUploadReaper(interval, grace time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		s.ReapUploads(grace)
	}
}
