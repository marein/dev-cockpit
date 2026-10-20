package activity

import (
	"testing"
	"time"
)

// A sign of life holds no place in the turn's order: claude does not wait for
// the hook it fires while text streams, so that hook's file can land after
// the Stop of the same turn. Read as a word it would open what the Stop
// closed, with nothing left to close it.
func TestASignOfLifeNeverReopensWhatTheAccountClosed(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, false, *at)
	stop := at.Add(time.Second)
	tracker.SetTurn("a", false, false, stop)
	tracker.Alive("a", stop.Add(time.Millisecond))
	if tracker.Working()["a"] != 0 {
		t.Fatal("a late sign of life must not reopen a turn the account closed")
	}
	tracker.Alive("b", *at)
	if tracker.Working()["b"] != 0 {
		t.Fatal("a sign of life alone opens nothing")
	}
}

// A close that was only the tracker's own guess, the interrupt grace, is
// taken back by the coder going on, and the turn's real end still closes it
// although its file is a moment older than the sign of life before it.
func TestASignOfLifeTakesBackAGuess(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, false, *at)
	key := at.Add(time.Second)
	tracker.Interrupt("a", key)
	*at = key.Add(interruptGrace + time.Second)
	tracker.sweep()
	if tracker.Working()["a"] != 0 {
		t.Fatal("the grace closes a silent turn")
	}
	streamed := at.Add(time.Second)
	tracker.Alive("a", streamed)
	if tracker.Working()["a"] == 0 {
		t.Fatal("text streaming after the guess reopens the turn")
	}
	tracker.SetTurn("a", false, false, streamed.Add(-time.Millisecond))
	if tracker.Working()["a"] != 0 {
		t.Fatal("the turn's end closes it, whatever the order of the two files")
	}
}

// After a guess the record's same word, read again when its file moves with
// bookkeeping, must not reopen the turn: it was written before the guess.
func TestTheSameRecordWordDoesNotUndoAGuess(t *testing.T) {
	tracker, at := newTestTracker()
	prompt := *at
	tracker.SetTurn("a", true, false, prompt)
	tracker.Interrupt("a", prompt.Add(time.Second))
	*at = prompt.Add(interruptGrace + 2*time.Second)
	tracker.sweep()
	tracker.SetTurn("a", true, false, prompt)
	if tracker.Working()["a"] != 0 {
		t.Fatal("the prompt read again reopened an aborted turn")
	}
}

// The cap counts from when the account was last heard, signs of life
// included: a long answer that only streams is not silent.
func TestTheCapCountsSignsOfLife(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.Configure("a", Policy{OpenTurnCap: 30 * time.Minute}, *at)
	tracker.SetTurn("a", true, false, *at)
	for i := 0; i < 40; i++ {
		*at = at.Add(time.Minute)
		tracker.Alive("a", *at)
		tracker.sweep()
	}
	if tracker.Working()["a"] == 0 {
		t.Fatal("a streaming turn outlives the cap")
	}
	*at = at.Add(31 * time.Minute)
	tracker.sweep()
	if tracker.Working()["a"] != 0 {
		t.Fatal("silence past the cap closes the turn")
	}
}

// An ask takes the mark down, the person's key brings it back up, and a
// denial's marker written after the key closes it again.
func TestAnAskWaitsForTheAnswerKey(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, true, *at)
	ask := at.Add(time.Second)
	tracker.SetWaiting("a", ask)
	if tracker.Working()["a"] != 0 {
		t.Fatal("a coder waiting on its person is not working")
	}
	key := ask.Add(3 * time.Second)
	tracker.Answered("a", key, false)
	if tracker.Working()["a"] == 0 {
		t.Fatal("the answer key starts the tool")
	}
	tracker.SetTurn("a", false, false, key)
	if tracker.Working()["a"] != 0 {
		t.Fatal("a denial marker in the key's own millisecond closes the turn")
	}
}

// The inbox is polled, so a quick key can come before the ask is heard: the
// ask is already answered then. A key before the ask was shown answers
// nothing.
func TestAKeyBeforeTheAskIsHeardAnswersIt(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, true, *at)
	ask := at.Add(time.Second)
	tracker.Answered("a", ask.Add(300*time.Millisecond), false)
	tracker.SetWaiting("a", ask)
	if tracker.Working()["a"] == 0 {
		t.Fatal("an ask answered before it was heard stays working")
	}

	tracker.SetTurn("b", true, true, *at)
	tracker.Answered("b", at.Add(500*time.Millisecond), false)
	tracker.SetWaiting("b", ask)
	if tracker.Working()["b"] != 0 {
		t.Fatal("a key before the ask answers nothing")
	}
}

// A question is filled in with several keys and a submit: keys do not end
// it, the coder's notification that it still stands keeps it one, and only
// the tool's own end opens the turn again.
func TestAQuestionIsNotAnsweredByKeys(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, false, *at)
	tracker.SetQuestion("a", at.Add(time.Second))
	tracker.Answered("a", at.Add(2*time.Second), false)
	tracker.SetWaiting("a", at.Add(6*time.Second))
	tracker.Answered("a", at.Add(8*time.Second), false)
	if tracker.Working()["a"] != 0 {
		t.Fatal("a question stays down through its answers")
	}
	tracker.SetTurn("a", true, false, at.Add(9*time.Second))
	if tracker.Working()["a"] == 0 {
		t.Fatal("the tool's end opens the turn again")
	}
}

// The record follows the coder: the newest word names where it keeps the
// session now, and the session's own id is the default.
func TestTheRecordFollowsTheNewestWord(t *testing.T) {
	tracker, at := newTestTracker()
	if got := tracker.Record("a"); got != "a" {
		t.Fatalf("Record = %q, want the session's own id", got)
	}
	tracker.SetRecord("a", "cleared", at.Add(time.Second))
	tracker.SetRecord("a", "old", *at)
	if got := tracker.Record("a"); got != "cleared" {
		t.Fatalf("Record = %q, want the newest word's", got)
	}
	tracker.SetRecord("a", "a", at.Add(2*time.Second))
	if got := tracker.Record("a"); got != "a" {
		t.Fatalf("Record = %q, want the session's own id back", got)
	}
}

// A digit typed into an ask after the person started writing there is text,
// not a pick: the amend field of a permission ask is opened with Tab and
// claude waits in it without a word, also past its own notification that
// the ask still stands. Enter submits what was written, and claude goes on.
func TestADigitWrittenIntoAnAskIsText(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, true, *at)
	ask := at.Add(time.Second)
	tracker.SetWaiting("a", ask)
	tracker.Wrote("a", ask.Add(time.Second))
	tracker.Answered("a", ask.Add(2*time.Second), false)
	tracker.SetWaiting("a", ask.Add(6*time.Second))
	tracker.Answered("a", ask.Add(7*time.Second), false)
	if tracker.Working()["a"] != 0 {
		t.Fatal("a digit typed into the field answered the ask")
	}
	tracker.Answered("a", ask.Add(9*time.Second), true)
	if tracker.Working()["a"] == 0 {
		t.Fatal("Enter submits what was written")
	}
}

// Text written before the ask was put, a prompt typed ahead while claude
// worked, leaves a digit at the ask a pick.
func TestTextTypedAheadLeavesTheAskToItsKeys(t *testing.T) {
	tracker, at := newTestTracker()
	tracker.SetTurn("a", true, true, *at)
	tracker.Wrote("a", at.Add(time.Second))
	ask := at.Add(2 * time.Second)
	tracker.SetWaiting("a", ask)
	tracker.Answered("a", ask.Add(time.Second), false)
	if tracker.Working()["a"] == 0 {
		t.Fatal("a digit at the ask picks its option")
	}
}
