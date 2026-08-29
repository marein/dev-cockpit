package coder

import (
	"testing"
	"time"
)

// An open turn needs a word from the running terminal's life. A message
// older than the terminal was left by a previous life, a turn aborted
// without a written end and resumed later, and its record touched by boot
// bookkeeping must not resurrect it; a reader that cannot date its messages
// is judged by the record's stamp instead.
func TestAnOpenTurnNeedsAWordFromThisLife(t *testing.T) {
	started := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	older := started.Add(-time.Hour)
	newer := started.Add(time.Second)
	cases := []struct {
		name     string
		activity Activity
		stamp    time.Time
		open     bool
	}{
		{"a message of this life testifies", Activity{LastMessageAt: newer}, newer, true},
		{"a previous life's message does not", Activity{LastMessageAt: older}, newer, false},
		{"bookkeeping moving the file changes nothing", Activity{LastMessageAt: older}, older, false},
		{"without message dates the stamp decides", Activity{}, newer, true},
		{"a stale stamp without dates does not testify", Activity{}, older, false},
		{"a finished turn is never open", Activity{Finished: true, LastMessageAt: newer}, newer, false},
		{"an unanswered ask is waiting, not working", Activity{AwaitingApproval: true, LastMessageAt: newer}, newer, false},
	}
	for _, c := range cases {
		if got := openTurn(c.activity, c.stamp, started); got != c.open {
			t.Errorf("%s: openTurn = %v, want %v", c.name, got, c.open)
		}
	}
}
