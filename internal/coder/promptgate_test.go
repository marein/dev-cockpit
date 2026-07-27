package coder

import (
	"testing"
	"time"

	"github.com/local/dev-cockpit/internal/tmux"
)

// gateClock is a clock the gate can be run against without waiting: sleeping
// advances it, so the test reads what the gate would have waited.
type gateClock struct {
	at    time.Time
	slept []time.Duration
}

func (c *gateClock) now() time.Time { return c.at }
func (c *gateClock) sleep(d time.Duration) {
	c.slept = append(c.slept, d)
	c.at = c.at.Add(d)
}

func (c *gateClock) total() time.Duration {
	var sum time.Duration
	for _, d := range c.slept {
		sum += d
	}
	return sum
}

// A prompt into a session older than the loss window costs nothing: the gate
// neither polls nor sleeps, which is what keeps every normal send immediate.
func TestTheGateIgnoresSettledSessions(t *testing.T) {
	clock := &gateClock{at: time.Unix(1000, 0)}
	polls := 0
	awaitPromptReady(clock.at.Add(-promptGateWindow), "claude", func() (tmux.PaneForeground, bool) {
		polls++
		return tmux.PaneForeground{Command: "claude", AltScreen: true}, true
	}, clock.now, clock.sleep)
	if polls != 0 || len(clock.slept) != 0 {
		t.Fatalf("an old session must pass without a wait, got %d polls and %v sleeps", polls, clock.slept)
	}
}

// A fresh pane whose TUI is not up yet is polled until the TUI stands on the
// alternate screen, and the send still waits the settle margin behind that:
// the flip alone measured lossy.
func TestTheGateWaitsForTheTUIAndThenSettles(t *testing.T) {
	clock := &gateClock{at: time.Unix(1000, 0)}
	polls := 0
	awaitPromptReady(clock.at, "claude", func() (tmux.PaneForeground, bool) {
		polls++
		if polls < 4 {
			return tmux.PaneForeground{Command: "claude", AltScreen: false}, true
		}
		return tmux.PaneForeground{Command: "claude", AltScreen: true}, true
	}, clock.now, clock.sleep)
	if polls != 4 {
		t.Fatalf("want the pane polled until ready, got %d polls", polls)
	}
	if len(clock.slept) == 0 || clock.slept[len(clock.slept)-1] != promptGateSettle {
		t.Fatalf("want the settle margin after the flip, got %v", clock.slept)
	}
}

// A pane that never shows the coder's TUI, because the CLI crashed or a shell
// holds the pane, is sent to anyway once the bound is spent. The gate must
// never turn a send into a hang.
func TestTheGateGivesUpOnAPaneThatNeverComesUp(t *testing.T) {
	clock := &gateClock{at: time.Unix(1000, 0)}
	awaitPromptReady(clock.at, "claude", func() (tmux.PaneForeground, bool) {
		return tmux.PaneForeground{Command: "bash", AltScreen: false}, true
	}, clock.now, clock.sleep)
	if clock.total() < promptGateMax || clock.total() > promptGateMax+promptGatePoll {
		t.Fatalf("want the wait bounded at the maximum, waited %v", clock.total())
	}
	if clock.slept[len(clock.slept)-1] == promptGateSettle {
		t.Fatal("a pane that never came up must not earn the settle margin")
	}
}

// The wrong TUI is not readiness: a pane where another full screen program
// runs must not count as the coder being ready to take the Enter.
func TestTheGateChecksTheCodersOwnCommand(t *testing.T) {
	clock := &gateClock{at: time.Unix(1000, 0)}
	awaitPromptReady(clock.at, "copilot", func() (tmux.PaneForeground, bool) {
		return tmux.PaneForeground{Command: "vim", AltScreen: true}, true
	}, clock.now, clock.sleep)
	if clock.total() < promptGateMax {
		t.Fatalf("a foreign TUI must not open the gate, waited only %v", clock.total())
	}
}
