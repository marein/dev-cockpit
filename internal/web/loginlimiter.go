package web

import (
	"log"
	"sync"
	"time"
)

// rateLimiter throttles login attempts keyed by client IP. fail reports
// whether the recorded attempt tripped a new block.
type rateLimiter interface {
	allow(ip string) (bool, time.Duration)
	fail(ip string) (justBlocked bool)
	reset(ip string)
}

// loginLimiter throttles failed login attempts per client IP. It counts
// failures inside a sliding window and, once the threshold is reached, blocks
// the IP for a fixed duration. Successful logins clear the IP's record.
//
// State is in-memory and per process; it is intentionally lost on restart.
type loginLimiter struct {
	mu          sync.Mutex
	records     map[string]*loginAttempts
	lastCleanup time.Time

	max    int
	window time.Duration
	block  time.Duration
	now    func() time.Time
}

type loginAttempts struct {
	count        int
	windowStart  time.Time
	blockedUntil time.Time
}

func newLoginLimiter(max int, window, block time.Duration, now func() time.Time) *loginLimiter {
	if now == nil {
		now = time.Now
	}
	return &loginLimiter{
		records: make(map[string]*loginAttempts),
		max:     max,
		window:  window,
		block:   block,
		now:     now,
	}
}

// allow reports whether a login attempt from ip may proceed. When blocked it
// returns the remaining wait time.
func (l *loginLimiter) allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec := l.records[ip]
	if rec == nil {
		return true, 0
	}
	now := l.now()
	if now.Before(rec.blockedUntil) {
		return false, rec.blockedUntil.Sub(now)
	}
	return true, 0
}

// fail records a failed attempt from ip, blocking it once the threshold is hit
// within the window. It reports whether this attempt tripped a new block.
func (l *loginLimiter) fail(ip string) (justBlocked bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.cleanup(now)

	rec := l.records[ip]
	if rec == nil {
		rec = &loginAttempts{}
		l.records[ip] = rec
	}
	if now.Before(rec.blockedUntil) {
		return false
	}
	if rec.windowStart.IsZero() || now.Sub(rec.windowStart) > l.window {
		rec.windowStart = now
		rec.count = 0
	}
	rec.count++
	if rec.count >= l.max {
		rec.blockedUntil = now.Add(l.block)
		rec.count = 0
		rec.windowStart = time.Time{}
		return true
	}
	return false
}

// reset clears any record for ip, called after a successful login.
func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.records, ip)
	l.mu.Unlock()
}

// cleanup drops stale, non-blocked records so the map cannot grow unbounded.
// It runs at most once per window and assumes l.mu is held.
func (l *loginLimiter) cleanup(now time.Time) {
	if now.Sub(l.lastCleanup) < l.window {
		return
	}
	l.lastCleanup = now
	for ip, rec := range l.records {
		if now.Before(rec.blockedUntil) {
			continue
		}
		if rec.windowStart.IsZero() || now.Sub(rec.windowStart) > l.window {
			delete(l.records, ip)
		}
	}
}

// loggingLoginLimiter decorates a rateLimiter, logging failed attempts and the
// moment an IP gets blocked. It holds no rate-limiting state of its own.
type loggingLoginLimiter struct {
	inner rateLimiter
	block time.Duration
	max   int
}

func newLoggingLoginLimiter(inner rateLimiter, block time.Duration, max int) *loggingLoginLimiter {
	return &loggingLoginLimiter{inner: inner, block: block, max: max}
}

func (l *loggingLoginLimiter) allow(ip string) (bool, time.Duration) {
	return l.inner.allow(ip)
}

func (l *loggingLoginLimiter) fail(ip string) (justBlocked bool) {
	justBlocked = l.inner.fail(ip)
	log.Printf("login failed from %s", ip)
	if justBlocked {
		log.Printf("login rate limit: blocked %s for %s after %d failed attempts", ip, l.block, l.max)
	}
	return justBlocked
}

func (l *loggingLoginLimiter) reset(ip string) {
	l.inner.reset(ip)
}
