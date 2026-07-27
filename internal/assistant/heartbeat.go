package assistant

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"time"
)

// The watcher used to hang on signals alone, and a signal is something a coder
// sends. No signal meant nothing was happening, which is exactly wrong for the
// one state a job most needs looking at: a coder that stopped. A coder with no
// room left to think in sends nothing, a coder waiting on a question sends
// nothing, a coder that quietly died sends nothing, and a job whose time ran out
// has nobody left to report it. All of those stood still forever.
//
// So an open job is looked at without a signal. The pass costs nothing by
// itself: it reads what the coder already wrote down, which is a file, and only
// buys a check when that says the coder is not moving, either because its turn
// is over or because the picture has stood still for too long to be work. A
// coder that is visibly moving is never checked, that is the whole point of
// looking first. Why it stopped is never decided here: that takes reading the
// screen, and the check does it.

const (
	// heartbeatInterval is how often the open jobs are looked at. The look is
	// free, so this is about how quickly a job that stopped is noticed, not
	// about cost.
	heartbeatInterval = 2 * time.Minute
	// heartbeatQuiet is how long after a check the heartbeat leaves a job alone.
	// A signal says something happened and buys a check after thirty seconds; a
	// heartbeat has no such news, so it waits far longer before spending one.
	heartbeatQuiet = 5 * time.Minute
	// stallAfter is when a coder that still looks busy is checked anyway.
	// Working is what the picture says, and a picture that has not changed for
	// this long is not work, it is a coder that is stuck in a way it cannot
	// report.
	stallAfter = 10 * time.Minute
	// vanishGrace is how long a job is given before a terminal that cannot be
	// found counts as gone. A coder that was just started is not in the session
	// list for a moment, and ending a job for that would be the worst kind of
	// wrong: silent and immediate.
	vanishGrace = time.Minute
)

// RunHeartbeat looks at the open jobs for as long as the process lives. Blocks;
// run it in a goroutine.
func (w *Watcher) RunHeartbeat(interval time.Duration) {
	if interval <= 0 {
		interval = heartbeatInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		w.Heartbeat()
	}
}

// Heartbeat is one pass over the open jobs. The first thing it does is end the
// jobs whose time or budget is up: that report used to need a signal, and a job
// that ran out is exactly the one that has none left to give.
func (w *Watcher) Heartbeat() {
	w.SweepExpired()
	for _, entry := range w.store.List() {
		if !entry.State.Open() {
			continue
		}
		w.look(entry.Terminal)
	}
}

// look is one job, without a signal. It walks the same gates a signal walks,
// which is where a job whose time or budget is up ends and says so, then reads
// the coder for free and only then decides whether anything is worth paying for.
func (w *Watcher) look(terminal string) {
	job, ok := w.gate(terminal)
	if !ok {
		return
	}
	w.mu.Lock()
	busy := w.running[terminal]
	w.mu.Unlock()
	if busy {
		// A check is already looking at this job, and it knows more than a
		// picture does.
		return
	}

	// A terminal that is not there any more can never meet its criterion, and
	// nothing else will ever say so: the signal that would have reported it died
	// with the session. This is the one thing a pass can settle without asking
	// anybody, so it does, and it says so.
	if w.vanished(job) {
		return
	}

	activity := w.observe(job)
	job = w.noteMovement(job, activity)

	if !w.worthChecking(job, activity) {
		return
	}
	// From here on it is the ordinary path a signal takes, including the queue
	// and the repeat rule, so a heartbeat check and a signal check are the same
	// thing and cost the same.
	w.Handle(terminal)
}

// vanished ends a job whose terminal is gone and reports it. Reports whether it
// took the job off the list.
//
// The grace period is what keeps this honest: a coder that was started a moment
// ago is not in the session list yet, and a job that ends for that would be a
// promise broken in the quietest possible way. Only a job that has been around
// long enough, and whose terminal is really not there, is closed.
func (w *Watcher) vanished(job Job) bool {
	if w.sessions == nil {
		return false
	}
	if w.now().UTC().Sub(job.CreatedAt) < w.vanishAfter {
		return false
	}
	if w.sessions.Running(job.CoderID, job.Terminal) {
		return false
	}
	log.Printf("assistant: the terminal of %s is gone, ending its job", job.Terminal)
	w.expire(job, "the coder it was steering is gone")
	return true
}

// worthChecking is where the cost of the heartbeat is decided, and it asks one
// question: is this coder moving. It stopped, so somebody has to look at why,
// and why is not a question this pass can answer. Or it looks busy but has shown
// the same picture for a long time, and then it is not busy, it is stuck in a way
// it cannot report itself. Anything else is work, and work is left alone.
func (w *Watcher) worthChecking(job Job, activity Activity) bool {
	now := w.now().UTC()
	// A job that was never checked counts from when it was made: a coder that
	// was just started and has not said anything yet is coming up, not stuck,
	// and the signal it will send is the cheaper way to hear about it.
	since := job.LastWakeAt
	if since.IsZero() {
		since = job.CreatedAt
	}
	if !since.IsZero() && now.Sub(since) < w.quiet {
		return false
	}
	if activity.Finished {
		// Its turn is over and the job is still open, so the criterion is not
		// met. What is in the way, a dialog, a full context window, an error it
		// gave up on, is what the check finds out.
		return true
	}
	return !job.ActivityAt.IsZero() && now.Sub(job.ActivityAt) >= w.stall
}

// noteMovement records what the coder last looked like, so a picture that stops
// changing becomes visible. It writes only when something actually moved, so a
// quiet job costs one file read and no write.
func (w *Watcher) noteMovement(job Job, activity Activity) Job {
	digest := activityDigest(activity)
	if digest == job.ActivityDigest && !job.ActivityAt.IsZero() {
		return job
	}
	fresh, ok := w.store.Update(job.Terminal, func(entry *Job) bool {
		entry.ActivityDigest = digest
		entry.ActivityAt = w.now().UTC()
		return true
	})
	if !ok {
		return job
	}
	return fresh
}

// activityDigest is a fingerprint of what the coder is showing, small enough to
// keep on the job and exact enough that one changed character counts as
// movement.
func activityDigest(activity Activity) string {
	sum := sha256.Sum256([]byte(activity.Text))
	return hex.EncodeToString(sum[:8])
}
