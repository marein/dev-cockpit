package coder

import (
	"bytes"
	"time"

	"github.com/local/dev-cockpit/internal/terminal"
)

// RunBellWatch watches every running coder of this manager and
// reports terminal bells. Copilot's beep option rings BEL when a turn
// finishes and when a dialog waits for input; either way the coder has
// news, so bells are reported as-is without classifying the pane. Blocks;
// run it in a goroutine.
func (s *Manager) RunBellWatch(interval time.Duration, onBell func(targetID string)) {
	terminal.RunWatch(interval, func() map[string]string {
		alive := map[string]string{}
		for _, r := range s.Snapshot().Running {
			alive[r.TmuxSession] = r.Identifier
		}
		return alive
	}, func(tmuxName, id string) (chan struct{}, error) {
		var lastBell time.Time
		return terminal.WatchOutput(tmuxName, func(out []byte, marks []string) {
			if bytes.IndexByte(out, 0x07) >= 0 && time.Since(lastBell) > terminal.BellCooldown {
				lastBell = time.Now()
				onBell(id)
			}
		})
	})
}
