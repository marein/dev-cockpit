package activity

import (
	"reflect"
	"testing"
	"time"
)

func newTestTracker() (*Tracker, *time.Time) {
	at := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	t := NewTracker()
	t.now = func() time.Time { return at }
	return t, &at
}

// Shelf one: the coder's account decides, and a quiet screen does not argue
// with it. An open turn survives a tool call that paints nothing.
func TestAnOpenTurnNeedsNoMovement(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, false, *at)
	if !tracker.Working()["a"] {
		t.Fatal("an open turn is working")
	}
	*at = at.Add(10 * time.Minute)
	tracker.sweep()
	if !tracker.Working()["a"] {
		t.Fatal("an open turn survives a quiet screen")
	}
	tracker.SetTurn("a", false, false, *at)
	if tracker.Working()["a"] {
		t.Fatal("a turn the account calls over is not working")
	}
}

// Shelf one beats shelf two: while the account says the turn is over, screen
// movement (a blinking input line, a repainting draft) marks nothing.
func TestAClosedTurnSilencesMovement(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.Configure("a", Policy{}, at.Add(-time.Hour))
	tracker.SetTurn("a", false, false, *at)
	for i := 0; i < 20; i++ {
		*at = at.Add(500 * time.Millisecond)
		tracker.Output("a")
	}
	if tracker.Working()["a"] {
		t.Fatal("movement must not overrule the account")
	}
	tracker.SetTurn("a", true, false, *at)
	if !tracker.Working()["a"] {
		t.Fatal("the account speaking again wins immediately")
	}
}

// Shelf two: without an account, sustained output turns the mark on and
// quiet turns it off.
func TestMovementCarriesAnUnknownSession(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.Configure("a", Policy{}, at.Add(-time.Hour))
	tracker.Output("a")
	if tracker.Working()["a"] {
		t.Fatal("one chunk must not count as working")
	}
	*at = at.Add(200 * time.Millisecond)
	tracker.Output("a")
	if tracker.Working()["a"] {
		t.Fatal("a burst below minSpan must not count as working")
	}
	*at = at.Add(500 * time.Millisecond)
	tracker.Output("a")
	if !tracker.Working()["a"] {
		t.Fatal("a run past minSpan is working")
	}
	*at = at.Add(idleAfter + time.Second)
	tracker.sweep()
	if tracker.Working()["a"] {
		t.Fatal("the movement mark decays with a quiet screen")
	}
}

func TestLoneRepaintsStayQuiet(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.Configure("a", Policy{}, at.Add(-time.Hour))
	tracker.Output("a")
	*at = at.Add(100 * time.Millisecond)
	tracker.Output("a")
	// The next paint comes past runGap, so it starts a run of its own.
	*at = at.Add(3 * time.Second)
	tracker.Output("a")
	if tracker.Working()["a"] {
		t.Fatal("two lone repaints must not count as working")
	}
}

func TestEchoOfInputStaysQuiet(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.Configure("a", Policy{}, at.Add(-time.Hour))
	tracker.Input("a")
	for i := 0; i < 10; i++ {
		*at = at.Add(100 * time.Millisecond)
		tracker.Output("a")
	}
	if tracker.Working()["a"] {
		t.Fatal("output echoing input must not count as working")
	}
	// Past the echo window the coder's own repaints qualify again.
	*at = at.Add(2 * time.Second)
	tracker.Output("a")
	*at = at.Add(700 * time.Millisecond)
	tracker.Output("a")
	if !tracker.Working()["a"] {
		t.Fatal("output past the echo window is working")
	}
}

func TestForgetDropsTheSession(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, false, *at)
	tracker.Forget("a")
	if len(tracker.Working()) != 0 {
		t.Fatal("a forgotten session is not working")
	}
}

// Every mutation publishes through one place, and only actual changes of the
// working set reach the listener.
func TestOnlyChangesArePublished(t *testing.T) {
	tracker, at := newTestTracker()
	var published [][]string
	tracker.SetOnChange(func(ids []string) { published = append(published, ids) })
	tracker.SetTurn("b", true, false, *at)
	tracker.SetTurn("a", true, false, *at)
	tracker.SetTurn("a", true, false, *at) // no change, no publish
	tracker.Input("a")                     // input alone changes nothing visible
	tracker.SetTurn("a", false, false, *at)
	want := [][]string{{"b"}, {"a", "b"}, {"b"}}
	if !reflect.DeepEqual(published, want) {
		t.Fatalf("published %v, want %v", published, want)
	}
}

func TestSweepDropsStaleUnknownSessions(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.Output("stale")
	tracker.SetTurn("kept", false, false, *at)
	*at = at.Add(sweepKeep + time.Second)
	tracker.sweep()
	if _, ok := tracker.sessions["stale"]; ok {
		t.Fatal("a session without an account is dropped when it stops moving")
	}
	if _, ok := tracker.sessions["kept"]; !ok {
		t.Fatal("a session with an account waits for Forget")
	}
}

// An interrupt is a hint, not a verdict: the account gets its grace to
// react. An abort during plain streaming writes nothing, so the silence
// past the grace is what closes the turn.
func TestASilentInterruptClosesTheTurn(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, false, *at)
	tracker.Interrupt("a")
	tracker.sweep()
	if !tracker.Working()["a"] {
		t.Fatal("inside the grace the turn still stands")
	}
	*at = at.Add(interruptGrace + time.Second)
	tracker.sweep()
	if tracker.Working()["a"] {
		t.Fatal("an account silent past the grace closes the turn")
	}
}

// The account speaking withdraws the interrupt: the abort during a tool
// call writes its own marker, and an Escape that only closed a menu is
// contradicted by the record moving on.
func TestTheAccountAnswersAnInterrupt(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, false, *at)
	tracker.Interrupt("a")
	tracker.SetTurn("a", true, false, *at) // the record moved: still working
	*at = at.Add(interruptGrace + time.Second)
	tracker.sweep()
	if !tracker.Working()["a"] {
		t.Fatal("a withdrawn interrupt must not close the turn")
	}
}

// An interrupt on a session without an open turn hints at nothing.
func TestAnInterruptNeedsAnOpenTurn(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", false, false, *at)
	tracker.Interrupt("a")
	*at = at.Add(interruptGrace + time.Second)
	tracker.sweep()
	if tracker.Working()["a"] {
		t.Fatal("nothing to close")
	}
	// And it never resurrects one.
	tracker.SetTurn("a", true, false, *at)
	if !tracker.Working()["a"] {
		t.Fatal("the account reopening still wins")
	}
}

// The backstop is the coder's own knob: an open turn whose account says
// nothing for the configured cap falls back to quiet, and a session without
// a cap waits for its account forever.
func TestTheBackstopEndsASilentOpenTurn(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.Configure("a", Policy{OpenTurnCap: 30 * time.Minute}, *at)
	tracker.SetTurn("a", true, false, *at)
	tracker.SetTurn("b", true, false, *at) // no policy: no cap
	*at = at.Add(29 * time.Minute)
	tracker.sweep()
	if !tracker.Working()["a"] {
		t.Fatal("a long quiet tool call stays open below the cap")
	}
	*at = at.Add(2 * time.Minute)
	tracker.sweep()
	if tracker.Working()["a"] {
		t.Fatal("past the cap the turn falls back to quiet")
	}
	if !tracker.Working()["b"] {
		t.Fatal("without a cap the account is waited for")
	}
}

// The interrupt hint never applies inside a tool call: an abort there writes
// its own marker, while the same key over that phase is usually a dialog
// closing above the running turn (claude's /usage, say).
func TestAnInterruptInsideAToolCallIsIgnored(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, true, *at)
	tracker.Interrupt("a")
	*at = at.Add(interruptGrace + time.Second)
	tracker.sweep()
	if !tracker.Working()["a"] {
		t.Fatal("a turn inside a tool call ignores the hint")
	}
}

// The screen also answers an interrupt: a coder whose dialog just closed
// keeps painting its status line past the key's echo, and that sign of life
// withdraws the hint the way a record word does. Claude's /usage over a
// thinking turn is exactly this.
func TestTheScreenAnswersAnInterrupt(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, false, *at)
	tracker.Input("a") // the escape key itself
	tracker.Interrupt("a")
	// The dialog-closing repaint is the key's echo and counts for nothing.
	*at = at.Add(500 * time.Millisecond)
	tracker.Output("a")
	// The status line ticking on past the echo is the withdrawal.
	*at = at.Add(2 * time.Second)
	tracker.Output("a")
	*at = at.Add(interruptGrace + time.Second)
	tracker.sweep()
	if !tracker.Working()["a"] {
		t.Fatal("a screen alive past the echo withdraws the hint")
	}
}

// A freshly started TUI paints its boot without anybody working: the
// movement shelf sits the coder's chosen grace out, counted from the
// session's own birth, so a serve restart over an old session mutes
// nothing.
func TestBootPaintsSitOutTheStartGrace(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.Configure("fresh", Policy{MovementStartGrace: 20 * time.Second}, *at)
	tracker.Configure("old", Policy{MovementStartGrace: 20 * time.Second}, at.Add(-time.Hour))
	for i := 0; i < 6; i++ {
		*at = at.Add(500 * time.Millisecond)
		tracker.Output("fresh")
		tracker.Output("old")
	}
	if tracker.Working()["fresh"] {
		t.Fatal("boot paints inside the grace must not count")
	}
	if !tracker.Working()["old"] {
		t.Fatal("an old session's movement counts from the first paint")
	}
	*at = at.Add(20 * time.Second)
	tracker.Output("fresh")
	*at = at.Add(700 * time.Millisecond)
	tracker.Output("fresh")
	if !tracker.Working()["fresh"] {
		t.Fatal("past the grace the movement shelf speaks again")
	}
}

// The policy arrives a beat after the watchers start feeding, on a cold
// machine seconds late: until it is here, movement marks nothing, because
// the paints may be a TUI booting under a grace not yet delivered. The run
// is tracked regardless, so a turn already painting when the policy of an
// old session arrives marks with the very next paint.
func TestMovementWaitsForThePolicy(t *testing.T) {
	tracker, at := newTestTracker()
	for i := 0; i < 6; i++ {
		*at = at.Add(500 * time.Millisecond)
		tracker.Output("a")
	}
	if tracker.Working()["a"] {
		t.Fatal("movement must not count before the policy is known")
	}
	tracker.Configure("a", Policy{MovementStartGrace: 20 * time.Second}, at.Add(-time.Hour))
	*at = at.Add(500 * time.Millisecond)
	tracker.Output("a")
	if !tracker.Working()["a"] {
		t.Fatal("an old session's running paint marks right after the policy")
	}
}

// A cold machine restoring every session at once paints far past any fixed
// grace: the boot is only over once the grace ran out and the screen has
// stood still, and however long the boot paints, it never marks. The first
// movement after that rest counts again.
func TestABootPaintingPastTheGraceStaysQuiet(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.Configure("a", Policy{MovementStartGrace: 20 * time.Second}, *at)
	for i := 0; i < 70; i++ { // 35 seconds of boot paints, well past the grace
		*at = at.Add(500 * time.Millisecond)
		tracker.Output("a")
	}
	if tracker.Working()["a"] {
		t.Fatal("a boot painting past the grace must not count")
	}
	// The screen comes to rest, then something really moves.
	*at = at.Add(bootSettle + time.Second)
	tracker.Output("a")
	*at = at.Add(700 * time.Millisecond)
	tracker.Output("a")
	if !tracker.Working()["a"] {
		t.Fatal("movement after the first rest counts again")
	}
}

// The account's words are ordered by when they were written, not by which
// road delivered them first: a record entry carries its file's stamp, a
// signal the moment it was heard. The permission ask depends on this: the
// tool call entry says open, the ask signal written moments later says the
// coder waits on its person, and the signal wins even when the slower road
// delivers it first.
func TestAnOlderWordNeverOverrulesANewerOne(t *testing.T) {
	tracker, at := newTestTracker()
	entry := *at
	signal := at.Add(500 * time.Millisecond)
	tracker.SetTurn("a", false, false, signal)
	tracker.SetTurn("a", true, true, entry) // the record entry, delivered late
	if tracker.Working()["a"] {
		t.Fatal("the older record entry must not overrule the newer signal")
	}
	// The record moving on afterwards is a newer word and speaks again.
	tracker.SetTurn("a", true, false, at.Add(2*time.Second))
	if !tracker.Working()["a"] {
		t.Fatal("a newer word reopens the turn")
	}
}
