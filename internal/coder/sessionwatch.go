package coder

import (
	"bytes"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/terminal"
)

// RunSessionWatch watches every running coder of this manager through a
// read-only control client and reports two raw facts, classifying neither:
// onOutput hears every output chunk, which is what feeds the working mark,
// and onBell hears terminal bells under the usual cooldown, as-is and without
// reading the pane: what a bell means is its coder's to say, see BellReader.
// Either callback may be nil. Blocks; run it in a goroutine.
//
// A session is listened to from the moment the manager started or resumed
// it, not from the next tick: a coder started with a short task rings for
// its end within a few seconds, and a bell nobody listens to yet is news
// that never arrives.
func (s *Manager) RunSessionWatch(interval time.Duration, onOutput, onBell func(targetID string)) {
	// A session's id may move while its pane stays, see LateSessionRecords,
	// and a watch lives as long as the pane. So each fact is reported under
	// the id the pane has when it happens: output under the one the last
	// tick saw, a bell, which is rare and is news, under the one a fresh
	// look finds, because a first answer can ring before any list was read
	// again.
	var mu sync.Mutex
	ids := map[string]string{}
	terminal.RunWatch(interval, s.changed, func() map[string]string {
		alive := map[string]string{}
		for _, r := range s.Snapshot().Running {
			alive[r.TmuxSession] = r.Identifier
		}
		mu.Lock()
		ids = alive
		mu.Unlock()
		return alive
	}, func(tmuxName, id string) (*terminal.Watch, error) {
		var lastBell time.Time
		return terminal.WatchOutput(tmuxName, func(out []byte, marks []string) {
			if onOutput != nil && len(out) > 0 {
				mu.Lock()
				if now, ok := ids[tmuxName]; ok {
					id = now
				}
				mu.Unlock()
				onOutput(id)
			}
			if onBell != nil && bytes.IndexByte(out, 0x07) >= 0 && time.Since(lastBell) > terminal.BellCooldown {
				lastBell = time.Now()
				fresh := s.compute()
				s.snapshots.put(fresh)
				for _, r := range fresh.Running {
					if r.TmuxSession == tmuxName {
						id = r.Identifier
					}
				}
				onBell(id)
			}
		})
	})
}
