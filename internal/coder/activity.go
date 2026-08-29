package coder

import (
	"time"

	"github.com/marein/dev-cockpit/internal/terminal"
)

// Asking a coder what one of its sessions is doing is a coder question, not a
// terminal question. The screen is what a person looks at: it carries the
// coder's input line, and a CLI that drafts the next prompt for its user paints
// that draft into the same box. Reading it as if it were a message turns the
// coder's own suggestion into an instruction nobody gave, and a suggestion
// appearing counts as movement although nothing happened. A coder that keeps a
// record of its session answers from that record instead.

// Activity is what a coder says about one of its sessions without attaching to
// it: what it last did, and whether it is still doing something. Why it stopped
// is not answered here and cannot be: a coder waiting on a dialog, one with no
// room left to think in and one that is simply done all look the same from
// outside, and telling them apart takes reading the screen and understanding it.
// That is what the check does, with the screen in front of it.
type Activity struct {
	// Text is what the session last did, newest last, bounded to what a reader
	// can take in.
	Text string
	// Finished says the session's turn is over: it is waiting, not working.
	Finished bool
	// Screen says Text is the terminal picture rather than a recorded
	// conversation, so it carries the coder's input line and whatever draft
	// stands in it. A reader has to be told that, because a draft is not a
	// message.
	Screen bool
	// InToolCall says the record ends inside a tool call the coder still
	// owes a result to. It refines an unfinished turn for the interrupt
	// heuristic, see ActivityProfile.InterruptKeys; a reader that cannot
	// tell leaves it false.
	InToolCall bool
	// AwaitingApproval says the record ends on a permission ask nobody has
	// answered yet: the turn is not over, but the coder is waiting on its
	// person, not working. A reader whose record does not mark the ask
	// leaves it false; claude's transcript is such a record, its ask is
	// heard as a hook signal instead.
	AwaitingApproval bool
	// LastMessageAt is when the newest recorded message was written, zero
	// when the reader cannot say. The record watcher needs it to tell a
	// message of the running terminal's life from one a previous life left
	// behind: a coder touches its record with bookkeeping at boot and exit,
	// so the file moving proves nothing about the conversation moving.
	LastMessageAt time.Time
}

// ActivityBudget is how many runes a whole activity reading may cost by
// default. Callers pass it as the budget; zero lifts the cap, for the one
// answer that needs a message whole.
const ActivityBudget = 4000

// ActivityReporter is the optional capability of a coder to report a session's
// activity from its own records. A coder implements it when its CLI keeps a
// record of the session on disk; for a coder that keeps none, the manager
// falls back to the terminal picture, which is the only source left.
//
// entries is how many recorded messages the reading keeps, newest last; zero
// or less means the coder's own default. budget is how many runes the whole
// reading may cost; zero or less means unlimited, and a message the budget
// cuts has to say in the text how much of it is shown, never end in a bare
// ellipsis. Fewer entries leave more room per message within the same budget.
type ActivityReporter interface {
	SessionActivity(sessionID string, entries, budget int) (Activity, error)
}

// ActivityStamper is the optional capability of a coder to say when a
// session's record last moved, without reading it. It is what lets the
// record watcher (RunTurnWatch) follow every running session cheaply: a
// stamp per tick, a real reading only when the stamp moved. A coder whose
// record cannot be statted, opencode's lives behind its CLI, simply does not
// implement it and is followed by its own push events instead.
type ActivityStamper interface {
	SessionActivityStamp(sessionID string) (time.Time, error)
}

// ActivityProfile is a coder's choice among the shared working-mark
// heuristics. The heuristics themselves live once, in internal/activity and
// the watchers; whether one applies to a coder is decided here and nowhere
// else, because a rule that saves one coder can break another: an interrupt
// key means abort to claude and may mean nothing of the sort elsewhere.
// Every coder answers this through the required Coder.ActivityProfile.
type ActivityProfile struct {
	// WatchRecord follows the session's record through RunTurnWatch. A
	// coder setting it must implement ActivityReporter and ActivityStamper;
	// a coder without a readable record leaves it off and reports its turns
	// itself (opencode's plugin events).
	WatchRecord bool
	// InterruptKeys takes a typed Escape or Ctrl+C as the hint that a turn
	// was aborted, for a coder whose aborts may leave no written trace. The
	// hint never applies inside a tool call (see Activity.InToolCall): an
	// abort there writes its own marker, while the same key may just be
	// closing a dialog over the running turn.
	InterruptKeys bool
	// OpenTurnCap ends an open turn whose record and signals have said
	// nothing at all for this long, the backstop against an end that was
	// never written anywhere; zero or less disables it. Generous on
	// purpose: a quiet tool call is a legitimate long silence.
	OpenTurnCap time.Duration
	// MovementStartGrace mutes the movement shelf for a session's first
	// moments: a freshly started TUI paints its whole boot without anybody
	// working, and after a reboot every restored session boots at once. A
	// real first turn is visible through the account regardless; zero or
	// less mutes nothing.
	MovementStartGrace time.Duration
}

// activityLines is how much of a terminal the screen fallback reads. Enough to
// see what the coder said last, bounded because every line is paid for by
// whoever reads it.
const activityLines = 120

// screenSettleGap is how long the screen fallback waits between its reads, and
// screenSettleReads is how many it takes. A working coder redraws its own status
// while it thinks, so identical pictures over this span are evidence that
// nothing is happening, not a guess about it. Three reads rather than two,
// because the one thing this fallback can get wrong is calling a coder idle that
// is busy inside a long tool call: a redraw anywhere in the window is enough to
// see it working, and the extra seconds are only spent for a coder that keeps no
// record of its own.
const (
	screenSettleGap   = 2 * time.Second
	screenSettleReads = 3
)

// Activity says what a session last did and whether its turn is over. The coder
// answers when it can, because its own record is reliable; only for a coder
// without one does this fall back to the picture on the screen. entries and
// budget are passed through to the reporter, see ActivityReporter; the screen
// fallback ignores them, it is bounded by lines instead.
func (s *Manager) Activity(rawID string, entries, budget int) (Activity, error) {
	id, err := terminal.ValidateIdentifier(rawID)
	if err != nil {
		return Activity{}, err
	}
	if reporter, ok := s.coder.(ActivityReporter); ok {
		activity, err := reporter.SessionActivity(id, entries, budget)
		if err == nil {
			return activity, nil
		}
		// A session that is running but has recorded nothing yet is starting,
		// not finished. Saying "I cannot read it" would reach the watcher as a
		// coder that is gone, and a gone coder is idle by definition: a job
		// checked in the first seconds of its coder would be reported as
		// standing still while the coder is coming up.
		if _, running := s.ResolveRunning(id); running == nil {
			return Activity{Text: "", Finished: false}, nil
		}
		return Activity{}, err
	}
	// Every coder that ships with the cockpit reports from its own record by
	// now, so no installed coder reaches this branch. It is not dead code: it
	// is the contract for a runtime that keeps no record of its sessions, the
	// screen is the only source such a coder has, and this stays even while
	// nothing uses it. Do not remove it in a cleanup.
	running, err := s.ResolveRunning(id)
	if err != nil {
		return Activity{}, err
	}
	return screenActivity(func() (string, error) {
		return s.tmux.CapturePane(running.TmuxSession, activityLines)
	}, s.screenGap)
}

// screenActivity reads the terminal screenSettleReads times over and calls a
// picture that never changed a session that is not working. It is the fallback,
// so it says so: Screen is set, and whoever reads the text has to know that the
// input line at its bottom belongs to the coder, not to a person.
//
// With every installed coder carrying its own ActivityReporter this function
// has no caller left in a normal setup. It is kept on purpose: it is the
// contract for any runtime without a record of its sessions, its tests run it
// directly, and deleting it would silently change what Manager.Activity
// promises for such a coder. Do not remove it because it looks unused.
func screenActivity(capture func() (string, error), gap time.Duration) (Activity, error) {
	last, err := capture()
	if err != nil {
		return Activity{}, err
	}
	still := true
	for i := 1; i < screenSettleReads; i++ {
		if gap > 0 {
			time.Sleep(gap)
		}
		next, err := capture()
		if err != nil {
			// What it last showed is still what it last showed, and a terminal
			// that stopped answering is not working.
			return Activity{Text: last, Finished: true, Screen: true}, nil
		}
		if next != last {
			still = false
		}
		last = next
	}
	return Activity{Text: last, Finished: still, Screen: true}, nil
}
