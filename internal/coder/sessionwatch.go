package coder

import (
	"bytes"
	"time"

	"github.com/marein/dev-cockpit/internal/terminal"
)

// RunSessionWatch watches every running coder of this manager through a
// read-only control client and reports two raw facts, classifying neither:
// onOutput hears every output chunk, which is what feeds the working mark,
// and onBell hears terminal bells under the usual cooldown. Copilot's beep
// option rings BEL when a turn finishes and when a dialog waits for input;
// either way the coder has news, so bells are reported as-is without reading
// the pane. Either callback may be nil. Blocks; run it in a goroutine.
func (s *Manager) RunSessionWatch(interval time.Duration, onOutput, onBell func(targetID string)) {
	terminal.RunWatch(interval, func() map[string]string {
		alive := map[string]string{}
		for _, r := range s.Snapshot().Running {
			alive[r.TmuxSession] = r.Identifier
		}
		return alive
	}, func(tmuxName, id string) (chan struct{}, error) {
		var lastBell time.Time
		return terminal.WatchOutput(tmuxName, func(out []byte, marks []string) {
			if onOutput != nil && len(out) > 0 {
				onOutput(id)
			}
			if onBell != nil && bytes.IndexByte(out, 0x07) >= 0 && time.Since(lastBell) > terminal.BellCooldown {
				lastBell = time.Now()
				onBell(id)
			}
		})
	})
}
