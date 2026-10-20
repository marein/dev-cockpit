// Package activity keeps the live answer to one question: in which coder
// sessions is something happening right now. It is the signal behind the
// working mark on the terminal icons.
//
// The answer falls through two shelves, best first, and working() below is
// that fall in code:
//
//  1. The coder's own account of its turn. It is fed by whoever can read or
//     hear that account: a coder's hooks and plugin events through the
//     notification inbox, the record watcher tailing the coder's transcript
//     or event log, and the bells the notification center ingests. While any
//     of them has spoken, the screen has no vote: a turn is open even when a
//     quiet tool call paints nothing, and over even while an idle input line
//     keeps blinking. Every word is ordered by when it was written, and the
//     newest wins, see SetTurn. The account also knows a turn that waits on
//     its person, an ask answered with one key (SetWaiting) or a dialog
//     filled in (SetQuestion), which is no work either.
//
//  2. Movement on the screen (Output/Input), for a session whose account
//     nobody has read yet, a turn already running when the serve process
//     started, or a coder that keeps no readable record. Sustained output
//     that no input explains is somebody working in there, and the mark
//     decays when the screen goes quiet. Whether a coder gets this shelf at
//     all is its own choice: one whose hooks cover every turn never feeds it.
//
// One abort leaves no trace on either shelf: interrupting a coder before its
// first answer writes nothing into its record and fires no hook. The
// interrupt keys are therefore taken as a hint (Interrupt) that the account
// may answer within a grace, and its silence is what closes the turn; the
// per-coder Policy.OpenTurnCap is the backstop behind everything else. Both
// are the tracker's own guess, which a sign of life (Alive) takes back.
//
// The account survives a serve restart: every change of it is written to the
// state file Persist names and read back from it at start, so a session
// keeps its last word, and the words that come after are ordered against it.
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
// inside a tool call, the one phase whose abort provably writes itself down,
// or whether it waits on its person.
type turnState int

const (
	turnUnknown turnState = iota
	turnOpen
	turnOpenInTool
	turnOver
	// turnWaiting is a turn that stands at an ask its person answers with
	// one key: the coder waits, and the key starts the tool, see Answered.
	turnWaiting
	// turnQuestion is a turn that stands at a dialog its person fills in:
	// keys do not end it, only the coder's next word does.
	turnQuestion
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
	// react before it closes the turn itself, see Interrupt. An abort while
	// text streams writes its marker well within this; an abort before the
	// first answer writes nothing at all, which is what the grace catches.
	interruptGrace = 4 * time.Second
	// bootSettle is the rest that ends a session's boot on the movement
	// shelf: the start grace names the least a boot is granted, but a cold
	// machine restoring every session at once paints far past any fixed
	// span, so the boot is only over once the screen has stood still this
	// long after the grace ran out. A real turn starts from that rest.
	bootSettle = 10 * time.Second
	// sweepKeep bounds how long a session with no turn account outlives its
	// last movement before the sweep drops it, so sessions that ended do
	// not accumulate. A session with an account is dropped by Forget, or
	// after this long by the sweep when no watcher ever claimed it: a
	// restored session whose terminal ended while the server was down, or
	// a hook naming no terminal at all.
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
	heardAt       time.Time // when the account last said anything, signs of life included: the cap counts from here
	guessed       bool      // the turn is over by the tracker's own guess, see sweep and Alive
	answeredAt    time.Time // the person's newest answer key, see Answered
	wroteAt       time.Time // the person's newest text written into the terminal, see Wrote
	askAt         time.Time // when the ask the turn waits at was put, see Answered
	interruptedAt time.Time // a pending interrupt key, waiting out its grace
	record        string    // the id the coder records this session under now, see SetRecord
	recordAt      time.Time
	bornAt        time.Time // when the tracker first held this session, see sweepKeep
	lastInput     time.Time
	lastOutput    time.Time // last qualifying output
	runStart      time.Time // start of the current run of output
	moving        bool
	since         time.Time // when the working mark went up, zero while it is down, see Working
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
	onChange  func(working map[string]int64)
	now       func() time.Time
	// path is the state file the account is kept in, see Persist; changed
	// says the file is behind on a word, heard that it is behind only on
	// when a session was last heard, which the sweep catches up on.
	path    string
	changed bool
	heard   bool
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
// set, called with the full working set, the map Working answers, so a
// consumer can publish it as a snapshot. Called outside the tracker's lock.
func (t *Tracker) SetOnChange(listen func(working map[string]int64)) {
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
// again or Forget drops the session.
//
// at is when the word was written, not when it arrived: a hook carries the
// moment it fired, a record entry its message's own time. The words reach
// the tracker through different roads with different delays, so they are
// ordered by their own time, and an older word never overrules a newer one.
// The permission ask is the case that needs this: the record's tool call
// entry says open, the ask moments later says the coder now waits on its
// person, and the ask must win no matter which road was faster, also when
// the same entry is read again. A word written after a pending interrupt
// key withdraws it, it is the account answering the key.
func (t *Tracker) SetTurn(id string, open, inTool bool, at time.Time) {
	state := turnOver
	switch {
	case open && inTool:
		state = turnOpenInTool
	case open:
		state = turnOpen
	}
	t.mu.Lock()
	t.hear(t.session(id), state, at)
	t.reconcile()
}

// SetWaiting records the coder's account that its turn stands at an ask its
// person answers with one key, a permission: the coder waits, the mark goes
// down. A key the person typed after the ask was shown but before this word
// arrived, the inbox is polled, already answered it, and the turn is open
// from that key on. A dialog the person fills in stays one when the coder
// says again that its ask still stands.
func (t *Tracker) SetWaiting(id string, at time.Time) {
	t.mu.Lock()
	s := t.session(id)
	switch {
	case s.turn == turnQuestion:
		t.hear(s, turnQuestion, at)
	case s.answeredAt.After(at) && !at.Before(s.turnAt):
		t.hear(s, turnOpenInTool, s.answeredAt)
	default:
		t.hear(s, turnWaiting, at)
	}
	t.reconcile()
}

// SetQuestion records the coder's account that its turn stands at a dialog
// its person fills in, one or more answers and a submit: keys do not end it,
// the coder's next word does, the tool's end when the dialog is submitted or
// the abort marker when it is dismissed.
func (t *Tracker) SetQuestion(id string, at time.Time) {
	t.mu.Lock()
	t.hear(t.session(id), turnQuestion, at)
	t.reconcile()
}

// Answered records that the person answered an ask with one key, at the
// moment the key went out: Enter, which submits whatever the ask shows, or a
// digit picking an option, which is text once the person has written into the
// ask since it was put, see Wrote, or since the account's newest word while
// the ask is not heard yet. An approved tool runs from there without any hook
// saying so until it ends, so a turn standing at an ask is open again, inside
// that tool. The key is also kept: an ask heard only after it is already
// answered, see SetWaiting. A denial is corrected by the coder's abort marker,
// written after the key. Nothing corrects a key that only led into a field
// the person fills in: the coder waits there and says nothing until the
// person submits it, so an ask that has such a field behind a key is a
// question, see SetQuestion, and keys never end it.
func (t *Tracker) Answered(id string, at time.Time, submits bool) {
	t.mu.Lock()
	s := t.session(id)
	since := s.turnAt
	if s.turn == turnWaiting {
		since = s.askAt
	}
	if !submits && s.wroteAt.After(since) {
		t.mu.Unlock()
		return
	}
	if at.After(s.answeredAt) {
		s.answeredAt = at
	}
	if s.turn == turnWaiting {
		t.hear(s, turnOpenInTool, at)
	}
	t.reconcile()
}

// Wrote records that the person wrote text into the terminal, at the moment
// it went out: a digit typed after it is part of that text, see Answered.
func (t *Tracker) Wrote(id string, at time.Time) {
	t.mu.Lock()
	s := t.session(id)
	if at.After(s.wroteAt) {
		s.wroteAt = at
	}
	t.mu.Unlock()
}

// Alive records a sign of life from the coder that holds no place in the
// turn's order, claude's streamed text: it withdraws a pending interrupt key
// it came after, and takes back a close that was only the tracker's own
// guess, the interrupt grace or the cap. It never overrules a word of the
// account, because it may arrive after the word that ended its turn.
func (t *Tracker) Alive(id string, at time.Time) {
	t.mu.Lock()
	s := t.session(id)
	t.alive(s, at)
	if s.guessed && s.turn == turnOver && at.After(s.turnAt) {
		s.turn = turnOpen
		s.guessed = false
		t.changed = true
	}
	t.reconcile()
}

// SetRecord records under which id the coder keeps this session's record
// now, as of a word written at at: claude starts a new transcript under a
// new id on /clear and on /resume, while the terminal stays the same. The
// newest word wins. The record watcher reads where Record points.
func (t *Tracker) SetRecord(id, record string, at time.Time) {
	if record == "" {
		return
	}
	if record == id {
		record = ""
	}
	t.mu.Lock()
	s := t.session(id)
	if at.After(s.recordAt) {
		t.changed = t.changed || s.record != record
		s.record = record
		s.recordAt = at
	}
	t.reconcile()
}

// Record answers the id the coder keeps a session's record under, the
// session's own id unless SetRecord moved it.
func (t *Tracker) Record(id string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.sessions[id]; ok && s.record != "" {
		return s.record
	}
	return id
}

// hear applies one word of the account, ordered by when it was written: an
// older word than the newest one is dropped. The caller holds the lock.
func (t *Tracker) hear(s *session, state turnState, at time.Time) {
	if at.Before(s.turnAt) {
		return
	}
	t.changed = t.changed || s.turn != state || !s.turnAt.Equal(at) || s.guessed
	if state == turnWaiting && s.turn != turnWaiting {
		s.askAt = at
	}
	s.turn = state
	s.turnAt = at
	s.guessed = false
	t.alive(s, at)
}

// alive takes a word or sign of life written at at: the cap counts from the
// newest one, and one after a pending interrupt key answers it. The caller
// holds the lock.
func (t *Tracker) alive(s *session, at time.Time) {
	if at.After(s.heardAt) {
		s.heardAt = at
		t.heard = true
	}
	if !s.interruptedAt.IsZero() && at.After(s.interruptedAt) {
		s.interruptedAt = time.Time{}
	}
}

// guess closes a turn on the tracker's own judgement, as of at. Words of the
// account written before that moment no longer reopen it, the same record
// entry read again included; a sign of life after it does, see Alive.
func (t *Tracker) guess(s *session, at time.Time) {
	s.turn = turnOver
	if at.After(s.turnAt) {
		s.turnAt = at
	}
	s.guessed = true
	s.interruptedAt = time.Time{}
	t.changed = true
}

// Interrupt records an interrupt key, Escape or Ctrl+C, for a coder whose
// profile chose the hint, at the moment the key went out. It closes nothing
// itself: any word or sign of life of the account written after the key
// withdraws it, because an aborted coder falls truly silent while one whose
// dialog just closed goes on. The screen is no such sign, it paints for
// reasons of its own. Only total silence past interruptGrace lets the sweep
// close the turn, the abort that writes nothing. A turn inside a tool call or
// at an ask ignores the hint entirely: an abort there writes its own marker.
func (t *Tracker) Interrupt(id string, at time.Time) {
	t.mu.Lock()
	s := t.session(id)
	if s.turn == turnOpen && s.interruptedAt.IsZero() && !at.Before(s.heardAt) {
		s.interruptedAt = at
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
	if _, ok := t.sessions[id]; ok {
		delete(t.sessions, id)
		t.changed = true
	}
	t.reconcile()
}

// Working answers the working set as a copy: every working session's id
// with the unix millisecond its mark went up. The page pins the mark's
// animation to that moment, so a mark drawn again stands where it stood
// and two marks run apart the way their turns started apart. A session
// that is not working is absent and reads as zero.
func (t *Tracker) Working() map[string]int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.workingLocked()
}

// Mark answers a moment in the shape Working answers it, the unix
// millisecond a mark went up, with the zero time as zero, no mark. The
// assistants' marks go through here too, their turns never reach this
// tracker but their icons are pinned the same way.
func Mark(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.UnixMilli()
}

// Earliest answers the older of two marks, zero standing for no mark: a
// row that carries several sessions wears the mark of whichever went up
// first.
func Earliest(a, b int64) int64 {
	if a == 0 || (b != 0 && b < a) {
		return b
	}
	return a
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
		// An interrupt the account never answered, and the backstop against
		// an end that was never written anywhere.
		open := s.turn == turnOpen || s.turn == turnOpenInTool
		switch {
		case s.turn == turnOpen && !s.interruptedAt.IsZero() && now.Sub(s.interruptedAt) > interruptGrace:
			t.guess(s, s.interruptedAt)
		case open && s.policy.OpenTurnCap > 0 && now.Sub(s.heardAt) > s.policy.OpenTurnCap:
			t.guess(s, s.heardAt.Add(s.policy.OpenTurnCap))
		}
		stale := now.Sub(s.lastOutput) > sweepKeep && now.Sub(s.lastInput) > sweepKeep
		unclaimed := !s.configured && now.Sub(s.bornAt) > sweepKeep && now.Sub(s.heardAt) > sweepKeep
		if (s.turn == turnUnknown && stale) || unclaimed {
			delete(t.sessions, id)
			t.changed = t.changed || s.turn != turnUnknown || s.record != ""
		}
	}
	if t.heard {
		t.changed = true
	}
	t.reconcile()
}

// session answers the tracked state of an id, creating it on first sight.
// The caller holds the lock.
func (t *Tracker) session(id string) *session {
	s, ok := t.sessions[id]
	if !ok {
		s = &session{bornAt: t.now()}
		t.sessions[id] = s
	}
	return s
}

// reconcile is the one place the working set is compared and published:
// every mutation ends here, so no feeder has to know whether it changed
// anything visible. The caller holds the lock, which is released here.
func (t *Tracker) reconcile() {
	if t.changed {
		t.save()
	}
	now := t.now()
	for _, s := range t.sessions {
		switch {
		case !s.working():
			s.since = time.Time{}
		case s.since.IsZero():
			s.since = now
		}
	}
	ids := t.workingIDsLocked()
	if equal(ids, t.published) {
		t.mu.Unlock()
		return
	}
	t.published = ids
	listen := t.onChange
	working := t.workingLocked()
	t.mu.Unlock()
	if listen != nil {
		listen(working)
	}
}

func (t *Tracker) workingLocked() map[string]int64 {
	out := map[string]int64{}
	for id, s := range t.sessions {
		if s.working() {
			out[id] = Mark(s.since)
		}
	}
	return out
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
