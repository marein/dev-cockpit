// Package activity keeps the live answer to one question: in which coder
// sessions is something happening right now. It is the signal behind the
// working mark on the terminal icons.
//
// The answer falls through two shelves, best first, and working() below is
// that fall in code:
//
//  1. The coder's own account of its turn (SetTurn). It is fed by whoever
//     can read or hear that account: the record watcher tailing the coder's
//     transcript or event log, the turn-end hooks and bells the notification
//     center already ingests, and a coder's own plugin events. While any of
//     them has spoken, the screen has no vote: a turn is open even when a
//     quiet tool call paints nothing, and over even while an idle input
//     line keeps blinking.
//
//  2. Movement on the screen (Output/Input), for a session whose account
//     nobody has read yet, a turn already running when the serve process
//     started, or a coder that keeps no readable record. Sustained output
//     that no input explains is somebody working in there, and the mark
//     decays when the screen goes quiet.
//
// One abort leaves no trace on either shelf: interrupting a coder mid
// streaming writes nothing into its record at all. The interrupt keys are
// therefore taken as a hint (Interrupt) that the account may answer within
// a grace, and its silence is what closes the turn; the per-coder
// Policy.OpenTurnCap is the backstop behind everything else.
//
// The heuristics live here once, but whether one applies to a session is
// its coder's choice: the feeders read coder.ActivityProfile and call only
// what the profile allows, and Configure hands the tracker the knobs it
// needs itself. The tracker knows nothing about coders, transcripts or
// tmux: it holds session ids, takes the raw facts, and publishes the
// working set.
package activity

import (
	"sort"
	"sync"
	"time"
)

// turnState is what shelf one knows about a session: nothing yet, or the
// coder's account of whether a turn is open, and if so whether it stands
// inside a tool call, the one phase whose abort provably writes itself down.
type turnState int

const (
	turnUnknown turnState = iota
	turnOpen
	turnOpenInTool
	turnOver
)

const (
	// echoGap is how long after an input the output stays disqualified on
	// the movement shelf: a keystroke comes back as a repaint of the input
	// line, and a person typing must not light their own session up as
	// working. Every input starts the window again, so continuous typing
	// stays quiet throughout.
	echoGap = 1500 * time.Millisecond
	// runGap is the longest silence that still continues a run of output.
	// A thinking coder repaints its status line at least about once a
	// second; a single repaint that stands alone is followed by nothing
	// and its run ends here.
	runGap = 2 * time.Second
	// minSpan is how long a run has to last before it counts: one repaint
	// arrives as a burst of writes in the same instant and never spans
	// this, a coder at work repaints past it within a beat.
	minSpan = 600 * time.Millisecond
	// idleAfter ends the movement mark when the screen stands still: on
	// that shelf, quiet is the only evidence of idleness there is.
	idleAfter = 15 * time.Second
	// interruptGrace is how long an interrupt key waits for the account to
	// react before it closes the turn itself, see Interrupt. An abort during
	// a tool call writes its marker well within this; an abort during plain
	// streaming writes nothing at all, which is what the grace catches.
	interruptGrace = 4 * time.Second
	// bootSettle is the rest that ends a session's boot on the movement
	// shelf: the start grace names the least a boot is granted, but a cold
	// machine restoring every session at once paints far past any fixed
	// span, so the boot is only over once the screen has stood still this
	// long after the grace ran out. A real turn starts from that rest.
	bootSettle = 10 * time.Second
	// sweepKeep bounds how long a session with no turn account outlives its
	// last movement before the sweep drops it, so sessions that ended do
	// not accumulate. A session with an account is dropped by Forget.
	sweepKeep = time.Hour
)

// session is everything the tracker holds about one terminal.
type session struct {
	policy        Policy
	configured    bool      // movement only counts once the policy is known, see Output
	mutedUntil    time.Time // the least a boot is granted, see Policy.MovementStartGrace
	booting       bool      // the movement shelf sits out the boot paints until the first rest
	turn          turnState
	turnAt        time.Time // when the account's newest word was written, see SetTurn
	interruptedAt time.Time // a pending interrupt key, waiting out its grace
	lastInput     time.Time
	lastOutput    time.Time // last qualifying output
	runStart      time.Time // start of the current run of output
	moving        bool
}

// working answers the one question, falling through the shelves: the
// coder's account when there is one, the screen's movement otherwise.
func (s *session) working() bool {
	if s.turn != turnUnknown {
		return s.turn == turnOpen || s.turn == turnOpenInTool
	}
	return s.moving
}

// Policy is what a coder's ActivityProfile means to this tracker: the knobs
// of the shared heuristics that vary per coder. Which feeder calls what is
// already decided outside (the interrupt hint, the record watch); what
// remains here is what the sweep has to know.
type Policy struct {
	// OpenTurnCap ends an open turn whose account has said nothing for
	// this long; zero or less disables it.
	OpenTurnCap time.Duration
	// MovementStartGrace mutes the movement shelf for at least this long
	// after the session started, and past that until the screen first
	// stands still for bootSettle: a booting TUI paints without anybody
	// working, and a cold machine restoring every session at once paints
	// far past any fixed span. Zero or less mutes nothing.
	MovementStartGrace time.Duration
}

// Tracker holds the working state per session id. Safe for concurrent use.
type Tracker struct {
	mu        sync.Mutex
	sessions  map[string]*session
	published []string
	onChange  func(working []string)
	now       func() time.Time
}

// NewTracker returns an empty tracker. Wire SetOnChange before the feeders
// start.
func NewTracker() *Tracker {
	return &Tracker{
		sessions: map[string]*session{},
		now:      time.Now,
	}
}

// SetOnChange installs the listener that hears every change of the working
// set, called with the full sorted set so a consumer can publish it as a
// snapshot. Called outside the tracker's lock.
func (t *Tracker) SetOnChange(listen func(working []string)) {
	t.mu.Lock()
	t.onChange = listen
	t.mu.Unlock()
}

// Configure hands a session its coder's policy, called when the session
// enters the running set, with when it started: the movement grace counts
// from the session's own birth, so a serve restart over long-running
// sessions mutes nothing. Until the policy is here, movement does not
// count (see Output), so however late this call arrives, no boot paint has
// marked anything in the meantime.
func (t *Tracker) Configure(id string, policy Policy, startedAt time.Time) {
	t.mu.Lock()
	s := t.session(id)
	s.policy = policy
	s.configured = true
	if policy.MovementStartGrace > 0 {
		s.mutedUntil = startedAt.Add(policy.MovementStartGrace)
		s.booting = t.now().Before(s.mutedUntil)
	}
	t.reconcile()
}

// SetTurn records the coder's account: a turn opened, inside a tool call or
// not, or a turn is over. Shelf one; it stands until the account speaks
// again or Forget drops the session. Every word of the account also
// withdraws a pending interrupt: the record moving on is the account
// answering it.
//
// at is when the word was written, not when it arrived: a record entry
// carries its file's stamp, a signal the moment it was heard. The words
// reach the tracker through different roads with different delays, so they
// are ordered by their own time, and an older word never overrules a newer
// one. The permission ask is the case that needs this: the record's tool
// call entry says open, the ask signal moments later says the coder now
// waits on its person, and the signal must win no matter which road was
// faster.
func (t *Tracker) SetTurn(id string, open, inTool bool, at time.Time) {
	t.mu.Lock()
	s := t.session(id)
	if at.Before(s.turnAt) {
		t.mu.Unlock()
		return
	}
	switch {
	case open && inTool:
		s.turn = turnOpenInTool
	case open:
		s.turn = turnOpen
	default:
		s.turn = turnOver
	}
	s.turnAt = at
	s.interruptedAt = time.Time{}
	t.reconcile()
}

// Interrupt records an interrupt key, Escape or Ctrl+C, for a coder whose
// profile chose the hint. It closes nothing itself: any sign of life
// withdraws it, the record moving on (SetTurn) and the screen painting past
// the key's own echo alike (Output), because an aborted coder falls truly
// silent while one whose dialog just closed keeps ticking its status line.
// Only total silence past interruptGrace lets the sweep close the turn, the
// abort that writes nothing. A turn inside a tool call ignores the hint
// entirely: an abort there writes its own marker, while the phase itself
// paints nothing that could withdraw a wrong hint.
func (t *Tracker) Interrupt(id string) {
	t.mu.Lock()
	s := t.session(id)
	if s.turn == turnOpen && s.interruptedAt.IsZero() {
		s.interruptedAt = t.now()
	}
	t.mu.Unlock()
}

// Output records that output flowed in a session: the movement shelf.
// Output within echoGap of the last input is the echo of that input and
// counts for nothing; a run of qualifying output that spans minSpan makes
// the session moving. Marking waits for two things the run itself cannot
// know: the coder's policy (a session seen before Configure may be a TUI
// booting under a grace not yet delivered), and the end of the boot, which
// outlasts its grace until the screen first stands still. The run and the
// timestamps are tracked throughout, so a turn already painting when the
// policy arrives marks immediately, and the sweep can drop stale sessions.
func (t *Tracker) Output(id string) {
	t.mu.Lock()
	s := t.session(id)
	now := t.now()
	if now.Sub(s.lastInput) < echoGap {
		t.mu.Unlock()
		return
	}
	// The screen painting past the interrupt key's echo is the turn still
	// running: the key closed a dialog, not the turn, see Interrupt. A
	// sign of life is one whether or not it may count as movement yet.
	if !s.interruptedAt.IsZero() && now.After(s.interruptedAt) {
		s.interruptedAt = time.Time{}
	}
	// The boot is over once its grace ran out and the screen has stood
	// still since: this paint is the first of something new. A paint
	// before that rest is still the boot painting, however long it takes.
	if s.booting && now.After(s.mutedUntil) && now.Sub(s.lastOutput) >= bootSettle {
		s.booting = false
	}
	if now.Sub(s.lastOutput) > runGap {
		s.runStart = now
	}
	s.lastOutput = now
	if s.configured && !s.booting && now.Sub(s.runStart) >= minSpan {
		s.moving = true
	}
	t.reconcile()
}

// Input records that input was sent to a session, which starts the echo
// window: whatever the terminal paints right after is the input coming
// back, not the coder working.
func (t *Tracker) Input(id string) {
	t.mu.Lock()
	t.session(id).lastInput = t.now()
	t.mu.Unlock()
}

// Forget drops a session that is gone. The record watcher calls it when a
// session leaves the running set, so a dead session's mark cannot outlive
// its terminal.
func (t *Tracker) Forget(id string) {
	t.mu.Lock()
	delete(t.sessions, id)
	t.reconcile()
}

// Working answers the current working set as a copy.
func (t *Tracker) Working() map[string]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]bool{}
	for id, s := range t.sessions {
		if s.working() {
			out[id] = true
		}
	}
	return out
}

// WorkingIDs answers the current working set sorted, the shape the event
// snapshot carries.
func (t *Tracker) WorkingIDs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.workingIDsLocked()
}

// Run expires movement marks whose screens went quiet and drops sessions
// that stopped mattering long ago. Blocks; run it in a goroutine.
func (t *Tracker) Run(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		t.sweep()
	}
}

func (t *Tracker) sweep() {
	t.mu.Lock()
	now := t.now()
	for id, s := range t.sessions {
		if s.moving && now.Sub(s.lastOutput) > idleAfter {
			s.moving = false
		}
		if s.turn == turnOpen || s.turn == turnOpenInTool {
			// An interrupt the account never answered, and the backstop
			// against an end that was never written anywhere.
			interrupted := !s.interruptedAt.IsZero() && now.Sub(s.interruptedAt) > interruptGrace
			capped := s.policy.OpenTurnCap > 0 && now.Sub(s.turnAt) > s.policy.OpenTurnCap
			if interrupted || capped {
				s.turn = turnOver
				s.interruptedAt = time.Time{}
			}
		}
		stale := now.Sub(s.lastOutput) > sweepKeep && now.Sub(s.lastInput) > sweepKeep
		if s.turn == turnUnknown && stale {
			delete(t.sessions, id)
		}
	}
	t.reconcile()
}

// session answers the tracked state of an id, creating it on first sight.
// The caller holds the lock.
func (t *Tracker) session(id string) *session {
	s, ok := t.sessions[id]
	if !ok {
		s = &session{}
		t.sessions[id] = s
	}
	return s
}

// reconcile is the one place the working set is compared and published:
// every mutation ends here, so no feeder has to know whether it changed
// anything visible. The caller holds the lock, which is released here.
func (t *Tracker) reconcile() {
	ids := t.workingIDsLocked()
	if equal(ids, t.published) {
		t.mu.Unlock()
		return
	}
	t.published = ids
	listen := t.onChange
	t.mu.Unlock()
	if listen != nil {
		listen(ids)
	}
}

func (t *Tracker) workingIDsLocked() []string {
	ids := make([]string, 0, len(t.sessions))
	for id, s := range t.sessions {
		if s.working() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
