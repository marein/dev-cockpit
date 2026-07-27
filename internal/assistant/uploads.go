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
// nothing else ever collects it: deleting a conversation takes its whole
// directory, and a sent file is referenced forever.
//
// grace keeps a file that is waiting for its message: only uploads older than
// that are candidates, so a reap can never race a composer the user is still
// filling.
func (s *Service) ReapUploads(grace time.Duration) {
	root := s.store.UploadRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	known := map[string]bool{}
	for _, entry := range s.store.List() {
		known[entry.ID] = true
	}
	cutoff := s.now().Add(-grace)
	removed := 0
	for _, dir := range entries {
		if !dir.IsDir() {
			continue
		}
		removed += s.reapConversationUploads(filepath.Join(root, dir.Name()), dir.Name(), known[dir.Name()], cutoff)
	}
	if removed > 0 {
		log.Printf("assistant: removed %d unsent upload(s)", removed)
	}
}

// reapConversationUploads drops the unreferenced uploads of one conversation, or
// the whole directory when the conversation itself is gone.
func (s *Service) reapConversationUploads(dir, id string, known bool, cutoff time.Time) int {
	files, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	referenced := map[string]bool{}
	if known {
		if c, ok := s.store.Load(id); ok {
			for _, m := range c.Messages {
				for _, a := range m.Attachments {
					referenced[filepath.Base(a.Path)] = true
				}
			}
			// A draft is a message that was not sent yet, and its files are
			// exactly as referenced as a sent one's. A composer can stand open for
			// a day, so the grace period alone would not save them: reaping them
			// leaves a draft pointing at files that are gone, and sending it fails
			// on a file the user picked and can still see.
			for _, a := range c.Draft.Attachments {
				referenced[filepath.Base(a.Path)] = true
			}
		} else {
			// The index knows the conversation but its transcript is unreadable. Keeping
			// the files costs a little disk, deleting them could throw away what
			// a recovered transcript still points at.
			return 0
		}
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
	if !known && removed > 0 {
		// The conversation is gone, so an empty directory has nothing left to hold.
		_ = os.Remove(dir)
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
