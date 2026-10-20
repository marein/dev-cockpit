package activity

import (
	"maps"
	"reflect"
	"slices"
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
	if tracker.Working()["a"] == 0 {
		t.Fatal("an open turn is working")
	}
	*at = at.Add(10 * time.Minute)
	tracker.sweep()
	if tracker.Working()["a"] == 0 {
		t.Fatal("an open turn survives a quiet screen")
	}
	tracker.SetTurn("a", false, false, *at)
	if tracker.Working()["a"] != 0 {
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
	if tracker.Working()["a"] != 0 {
		t.Fatal("movement must not overrule the account")
	}
	tracker.SetTurn("a", true, false, *at)
	if tracker.Working()["a"] == 0 {
		t.Fatal("the account speaking again wins immediately")
	}
}

// Shelf two: without an account, sustained output turns the mark on and
// quiet turns it off.
func TestMovementCarriesAnUnknownSession(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.Configure("a", Policy{}, at.Add(-time.Hour))
	tracker.Output("a")
	if tracker.Working()["a"] != 0 {
		t.Fatal("one chunk must not count as working")
	}
	*at = at.Add(200 * time.Millisecond)
	tracker.Output("a")
	if tracker.Working()["a"] != 0 {
		t.Fatal("a burst below minSpan must not count as working")
	}
	*at = at.Add(500 * time.Millisecond)
	tracker.Output("a")
	if tracker.Working()["a"] == 0 {
		t.Fatal("a run past minSpan is working")
	}
	*at = at.Add(idleAfter + time.Second)
	tracker.sweep()
	if tracker.Working()["a"] != 0 {
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
	if tracker.Working()["a"] != 0 {
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
	if tracker.Working()["a"] != 0 {
		t.Fatal("output echoing input must not count as working")
	}
	// Past the echo window the coder's own repaints qualify again.
	*at = at.Add(2 * time.Second)
	tracker.Output("a")
	*at = at.Add(700 * time.Millisecond)
	tracker.Output("a")
	if tracker.Working()["a"] == 0 {
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
	tracker.SetOnChange(func(working map[string]int64) { published = append(published, slices.Sorted(maps.Keys(working))) })
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
	tracker.Configure("kept", Policy{}, *at)
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
	tracker.Interrupt("a", *at)
	tracker.sweep()
	if tracker.Working()["a"] == 0 {
		t.Fatal("inside the grace the turn still stands")
	}
	*at = at.Add(interruptGrace + time.Second)
	tracker.sweep()
	if tracker.Working()["a"] != 0 {
		t.Fatal("an account silent past the grace closes the turn")
	}
}

// The account speaking after the key withdraws the interrupt: an Escape
// that only closed a menu is contradicted by the coder going on. A word
// written before the key says nothing about what came after it.
func TestTheAccountAnswersAnInterrupt(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, false, *at)
	key := at.Add(time.Second)
	tracker.Interrupt("a", key)
	tracker.SetTurn("a", true, false, key.Add(-100*time.Millisecond))
	*at = key.Add(interruptGrace + time.Second)
	tracker.sweep()
	if tracker.Working()["a"] != 0 {
		t.Fatal("a word written before the key must not withdraw it")
	}

	tracker.SetTurn("b", true, false, *at)
	key = at.Add(time.Second)
	tracker.Interrupt("b", key)
	tracker.SetTurn("b", true, false, key.Add(100*time.Millisecond))
	*at = key.Add(interruptGrace + time.Second)
	tracker.sweep()
	if tracker.Working()["b"] == 0 {
		t.Fatal("a withdrawn interrupt must not close the turn")
	}
}

// An interrupt on a session without an open turn hints at nothing.
func TestAnInterruptNeedsAnOpenTurn(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", false, false, *at)
	tracker.Interrupt("a", *at)
	*at = at.Add(interruptGrace + time.Second)
	tracker.sweep()
	if tracker.Working()["a"] != 0 {
		t.Fatal("nothing to close")
	}
	// And it never resurrects one.
	tracker.SetTurn("a", true, false, *at)
	if tracker.Working()["a"] == 0 {
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
	if tracker.Working()["a"] == 0 {
		t.Fatal("a long quiet tool call stays open below the cap")
	}
	*at = at.Add(2 * time.Minute)
	tracker.sweep()
	if tracker.Working()["a"] != 0 {
		t.Fatal("past the cap the turn falls back to quiet")
	}
	if tracker.Working()["b"] == 0 {
		t.Fatal("without a cap the account is waited for")
	}
}

// The interrupt hint never applies inside a tool call: an abort there writes
// its own marker, while the same key over that phase is usually a dialog
// closing above the running turn (claude's /usage, say).
func TestAnInterruptInsideAToolCallIsIgnored(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, true, *at)
	tracker.Interrupt("a", *at)
	*at = at.Add(interruptGrace + time.Second)
	tracker.sweep()
	if tracker.Working()["a"] == 0 {
		t.Fatal("a turn inside a tool call ignores the hint")
	}
}

// The screen never answers an interrupt: it paints for reasons of its own,
// a status line redrawn on a timer among them, so only the account's own
// sign of life after the key withdraws the hint.
func TestTheScreenDoesNotAnswerAnInterrupt(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, false, *at)
	tracker.Input("a")
	tracker.Interrupt("a", *at)
	for i := 0; i < 10; i++ {
		*at = at.Add(time.Second)
		tracker.Output("a")
	}
	tracker.sweep()
	if tracker.Working()["a"] != 0 {
		t.Fatal("a painting screen must not withdraw the hint")
	}

	tracker.SetTurn("b", true, false, *at)
	tracker.Interrupt("b", *at)
	tracker.Alive("b", at.Add(time.Second))
	*at = at.Add(interruptGrace + 2*time.Second)
	tracker.sweep()
	if tracker.Working()["b"] == 0 {
		t.Fatal("a sign of life after the key withdraws the hint")
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
	if tracker.Working()["fresh"] != 0 {
		t.Fatal("boot paints inside the grace must not count")
	}
	if tracker.Working()["old"] == 0 {
		t.Fatal("an old session's movement counts from the first paint")
	}
	*at = at.Add(20 * time.Second)
	tracker.Output("fresh")
	*at = at.Add(700 * time.Millisecond)
	tracker.Output("fresh")
	if tracker.Working()["fresh"] == 0 {
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
	if tracker.Working()["a"] != 0 {
		t.Fatal("movement must not count before the policy is known")
	}
	tracker.Configure("a", Policy{MovementStartGrace: 20 * time.Second}, at.Add(-time.Hour))
	*at = at.Add(500 * time.Millisecond)
	tracker.Output("a")
	if tracker.Working()["a"] == 0 {
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
	if tracker.Working()["a"] != 0 {
		t.Fatal("a boot painting past the grace must not count")
	}
	// The screen comes to rest, then something really moves.
	*at = at.Add(bootSettle + time.Second)
	tracker.Output("a")
	*at = at.Add(700 * time.Millisecond)
	tracker.Output("a")
	if tracker.Working()["a"] == 0 {
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
	if tracker.Working()["a"] != 0 {
		t.Fatal("the older record entry must not overrule the newer signal")
	}
	// The record moving on afterwards is a newer word and speaks again.
	tracker.SetTurn("a", true, false, at.Add(2*time.Second))
	if tracker.Working()["a"] == 0 {
		t.Fatal("a newer word reopens the turn")
	}
}

// The moment the mark went up stays with it: the account speaking again
// moves nothing, and a mark that went down and up again is a new one.
func TestTheMarkKeepsTheMomentItWentUp(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, false, *at)
	up := tracker.Working()["a"]
	if up != at.UnixMilli() {
		t.Fatalf("the mark went up at %d, want %d", up, at.UnixMilli())
	}
	*at = at.Add(time.Minute)
	tracker.SetTurn("a", true, true, *at)
	if tracker.Working()["a"] != up {
		t.Fatal("a tool call moved the moment the mark went up")
	}
	tracker.SetTurn("a", false, false, *at)
	*at = at.Add(time.Minute)
	tracker.SetTurn("a", true, false, *at)
	if tracker.Working()["a"] != at.UnixMilli() {
		t.Fatal("a mark that went up again carries its new moment")
	}
}

func TestEarliestIgnoresAMissingMark(t *testing.T) {
	for _, c := range []struct{ a, b, want int64 }{{0, 0, 0}, {0, 5, 5}, {5, 0, 5}, {5, 3, 3}, {3, 5, 3}} {
		if got := Earliest(c.a, c.b); got != c.want {
			t.Fatalf("Earliest(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// The zero time is no mark, not a moment two thousand years ago a page would
// pin an animation to.
func TestTheZeroTimeIsNoMark(t *testing.T) {
	if Mark(time.Time{}) != 0 {
		t.Fatal("the zero time marks something")
	}
	at := time.Date(2026, 10, 14, 12, 0, 0, 123e6, time.UTC)
	if Mark(at) != at.UnixMilli() {
		t.Fatalf("a moment marks %d, want %d", Mark(at), at.UnixMilli())
	}
}
