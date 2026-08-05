package coder

import (
	"sync"
	"time"
)

// CapabilityProbe caches an expensive check of an installed CLI. A pass is
// final for the life of the process, the binary does not lose a flag while the
// server runs. A miss is retried once the pause is over, because a failed
// probe often describes the moment rather than the CLI, for example an update
// replacing the binary right when the probe ran. Freezing that first
// impression would keep the capability off until the next server restart.
type CapabilityProbe struct {
	check func() bool
	pause time.Duration

	mu      sync.Mutex
	passed  bool
	lastTry time.Time
}

// NewCapabilityProbe builds a probe around check. pause is how long a miss is
// trusted before the check may run again.
func NewCapabilityProbe(check func() bool, pause time.Duration) *CapabilityProbe {
	return &CapabilityProbe{check: check, pause: pause}
}

// Passed reports whether the check has succeeded, running it when its last
// answer is no longer usable. The lock stays held while the check runs, so
// concurrent callers wait for one probe instead of starting their own.
func (p *CapabilityProbe) Passed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.passed {
		return true
	}
	if !p.lastTry.IsZero() && time.Since(p.lastTry) < p.pause {
		return false
	}
	p.lastTry = time.Now()
	p.passed = p.check()
	return p.passed
}
