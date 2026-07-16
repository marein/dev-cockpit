package coder

import (
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/tmux"
)

// snapshotCache memoises Snapshot for a short TTL to soak up bursts of
// requests during page renders.
type snapshotCache struct {
	mu      sync.Mutex
	value   *Snapshot
	expires time.Time
	ttl     time.Duration
}

func (c *snapshotCache) get() (Snapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value != nil && time.Now().Before(c.expires) {
		return *c.value, true
	}
	return Snapshot{}, false
}

func (c *snapshotCache) put(s Snapshot) {
	c.mu.Lock()
	c.value = &s
	c.expires = time.Now().Add(c.ttl)
	c.mu.Unlock()
}

func (c *snapshotCache) invalidate() {
	c.mu.Lock()
	c.value = nil
	c.mu.Unlock()
}

// scanRunning derives the verified Running list and the leftover Inactive
// resumables. Panes are attributed through the @dc_coder tmux options set at
// launch; the pane name doubles as the session id (for copilot after the
// promote rename), so a store hit needs no further verification. Panes without
// the options predate the option tagging and go through the legacy process
// scan.
func scanRunning(panes []tmux.Pane, resumable []Session, prov Coder) (running []Running, inactive []Session) {
	resumableByID := map[string]Session{}
	for _, r := range resumable {
		resumableByID[r.SessionID] = r
	}
	runningIDs := map[string]bool{}
	legacy := legacyScanner{prov: prov, resumable: resumable}
	for _, p := range panes {
		if p.Coder != "" {
			if p.Coder != prov.ID() {
				continue
			}
			if match, ok := resumableByID[p.Name]; ok {
				running = append(running, Running{
					Identifier:   match.SessionID,
					TmuxSession:  p.Name,
					PID:          p.PID,
					Name:         DisplayName(match.Name, match.SessionID),
					StartedAt:    p.StartTime(),
					CWD:          match.CWD,
					TabPos:       p.TabPosition(),
					TabGroup:     p.TabGroup,
					TabGroupPos:  p.TabGroupPosition(),
					TabGroupName: p.TabGName,
				})
				runningIDs[match.SessionID] = true
				continue
			}
			// No store record yet (e.g. copilot before its session file
			// appears): fall back to the launch-time option values.
			running = append(running, Running{
				Identifier:   p.Name,
				TmuxSession:  p.Name,
				PID:          p.PID,
				Name:         DisplayName(p.CoderName, p.Name),
				StartedAt:    p.StartTime(),
				CWD:          p.CoderDir,
				TabPos:       p.TabPosition(),
				TabGroup:     p.TabGroup,
				TabGroupPos:  p.TabGroupPosition(),
				TabGroupName: p.TabGName,
			})
			continue
		}
		if p.ShellName != "" {
			continue
		}
		if r, id, ok := legacy.match(p); ok {
			running = append(running, r)
			if id != "" {
				runningIDs[id] = true
			}
		}
	}
	for _, r := range resumable {
		if !runningIDs[r.SessionID] {
			inactive = append(inactive, r)
		}
	}
	return running, inactive
}
