package session

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/proctree"
	"github.com/local/dev-cockpit/internal/provider"
	"github.com/local/dev-cockpit/internal/sessionlabel"
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

// scanRunning correlates live tmux panes with stored resumable sessions to
// derive the verified Running list and the leftover Inactive resumables.
func scanRunning(panes []tmux.Pane, resumable []provider.Session, prov provider.Provider) (running []Running, inactive []provider.Session) {
	// Capture the process table once for the whole scan so platforms without
	// /proc read it in two ps calls instead of reforking ps per descendant PID.
	tree := proctree.Capture()
	resumableByID := map[string]provider.Session{}
	resumableByLegacyName := map[string][]provider.Session{}
	for _, r := range resumable {
		resumableByID[r.SessionID] = r
		n, err := SanitizeName(r.Name)
		if err != nil {
			continue
		}
		resumableByLegacyName[n] = append(resumableByLegacyName[n], r)
	}
	runningIDs := map[string]bool{}
	for _, p := range panes {
		var descendants []int
		if rootPID, err := strconv.Atoi(p.PID); err == nil {
			descendants = tree.Descendants(rootPID)
		}
		match := findMatchingResumable(descendants, p.Name, resumableByID, resumableByLegacyName, prov, tree)
		if match == nil {
			if info, ok := paneProviderInfo(descendants, prov, tree); ok {
				running = append(running, Running{
					Identifier:  p.Name,
					TmuxSession: p.Name,
					PID:         p.PID,
					Name:        sessionlabel.DisplayName(info.name, p.Name),
					StartedAt:   paneStartTime(p.StartedAt),
					CWD:         info.cwd,
				})
			}
			continue
		}
		running = append(running, Running{
			Identifier:    match.SessionID,
			TmuxSession:   p.Name,
			PID:           p.PID,
			Name:          sessionlabel.DisplayName(match.Name, match.SessionID),
			StartedAt:     paneStartTime(p.StartedAt),
			CWD:           match.CWD,
			RemoteControl: match.RemoteControl,
			TaskURL:       match.TaskURL,
		})
		runningIDs[match.SessionID] = true
	}
	for _, r := range resumable {
		if !runningIDs[r.SessionID] {
			inactive = append(inactive, r)
		}
	}
	return running, inactive
}

func findMatchingResumable(descendants []int, paneName string, byID map[string]provider.Session, byLegacyName map[string][]provider.Session, prov provider.Provider, tree *proctree.Tree) *provider.Session {
	// Modern sessions name the tmux pane after the session ID, so an exact ID
	// hit plus a live provider process is already a unique match. Skip the
	// working-dir comparison here: it is redundant and would cost a per-PID
	// lsof on platforms without /proc.
	if direct, ok := byID[paneName]; ok {
		if hasProviderProcess(descendants, prov, tree) {
			return &direct
		}
	}
	// Legacy sessions were named after the (non-unique) sanitized display name,
	// so fall back to the working dir to disambiguate collisions.
	candidates := byLegacyName[paneName]
	for i := range candidates {
		if matchesResumable(descendants, candidates[i], prov, tree) {
			return &candidates[i]
		}
	}
	return nil
}

func hasProviderProcess(descendants []int, prov provider.Provider, tree *proctree.Tree) bool {
	for _, pid := range descendants {
		if provider.OwnsProcess(tree.Environ(pid), prov.ID()) {
			return true
		}
	}
	return false
}

type paneProviderProcess struct {
	cwd  string
	name string
}

func paneProviderInfo(descendants []int, prov provider.Provider, tree *proctree.Tree) (paneProviderProcess, bool) {
	for _, pid := range descendants {
		if provider.OwnsProcess(tree.Environ(pid), prov.ID()) {
			return paneProviderProcess{
				cwd:  tree.CWD(pid),
				name: provider.RunningName(tree.Cmdline(pid)),
			}, true
		}
	}
	return paneProviderProcess{}, false
}

func matchesResumable(descendants []int, candidate provider.Session, prov provider.Provider, tree *proctree.Tree) bool {
	target, err := filepath.EvalSymlinks(candidate.CWD)
	if err != nil {
		target = candidate.CWD
	}
	for _, pid := range descendants {
		if !provider.OwnsProcess(tree.Environ(pid), prov.ID()) {
			continue
		}
		if tree.CWD(pid) == target {
			return true
		}
	}
	return false
}

func paneStartTime(raw string) time.Time {
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}
