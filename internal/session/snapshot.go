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
	"github.com/local/dev-cockpit/internal/term"
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

// scanRunning correlates live sessions with stored resumable sessions to derive
// the verified Running list and the leftover Inactive resumables.
func scanRunning(panes []term.Pane, resumable []provider.Session, prov provider.Provider) (running []Running, inactive []provider.Session) {
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
		match := findMatchingResumable(p, resumableByID, resumableByLegacyName, prov)
		if match == nil {
			if info, ok := paneProviderInfo(p, prov); ok {
				running = append(running, Running{
					Identifier: p.Name,
					SessionKey: p.Name,
					PID:        p.PID,
					Name:       sessionlabel.DisplayName(info.name, p.Name),
					StartedAt:  paneStartTime(p.StartedAt),
					CWD:        info.cwd,
				})
			}
			continue
		}
		running = append(running, Running{
			Identifier:    match.SessionID,
			SessionKey:    p.Name,
			PID:           p.PID,
			Name:          sessionlabel.DisplayName(match.Name, match.SessionID),
			StartedAt:     paneStartTime(p.StartedAt),
			CWD:           match.CWD,
			SizeBytes:     match.SizeBytes,
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

func findMatchingResumable(p term.Pane, byID map[string]provider.Session, byLegacyName map[string][]provider.Session, prov provider.Provider) *provider.Session {
	rootPID, err := strconv.Atoi(p.PID)
	if err != nil {
		return nil
	}
	descendants := proctree.Descendants(rootPID)
	if direct, ok := byID[p.Name]; ok {
		if matchesResumable(descendants, direct, prov) {
			return &direct
		}
	}
	candidates := byLegacyName[p.Name]
	for i := range candidates {
		if matchesResumable(descendants, candidates[i], prov) {
			return &candidates[i]
		}
	}
	return nil
}

type paneProviderProcess struct {
	cwd  string
	name string
}

func paneProviderInfo(p term.Pane, prov provider.Provider) (paneProviderProcess, bool) {
	rootPID, err := strconv.Atoi(p.PID)
	if err != nil {
		return paneProviderProcess{}, false
	}
	for _, pid := range proctree.Descendants(rootPID) {
		if provider.OwnsProcess(proctree.Environ(pid), prov.ID()) {
			return paneProviderProcess{
				cwd:  proctree.CWD(pid),
				name: provider.RunningName(proctree.Cmdline(pid)),
			}, true
		}
	}
	return paneProviderProcess{}, false
}

func matchesResumable(descendants []int, candidate provider.Session, prov provider.Provider) bool {
	target, err := filepath.EvalSymlinks(candidate.CWD)
	if err != nil {
		target = candidate.CWD
	}
	for _, pid := range descendants {
		if !provider.OwnsProcess(proctree.Environ(pid), prov.ID()) {
			continue
		}
		if proctree.CWD(pid) == target {
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
