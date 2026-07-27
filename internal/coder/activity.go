package coder

import (
	"time"

	"github.com/local/dev-cockpit/internal/terminal"
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
