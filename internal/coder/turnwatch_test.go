package coder

import (
	"testing"
	"time"
)

// An open turn needs a word from the running terminal's life. A message
// older than the terminal was left by a previous life, a turn aborted
// without a written end and resumed later, and its record touched by boot
// bookkeeping must not resurrect it; a reader that cannot date its messages
// is judged by the record's stamp instead. A record without a single message
// is no word at all: claude writes its transcript at boot with a note about
// the instruction files it loaded, and that file's fresh stamp read as a
// prompt just received until the watcher learned to say nothing for it.
func TestAnOpenTurnNeedsAWordFromThisLife(t *testing.T) {
	started := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	older := started.Add(-time.Hour)
	newer := started.Add(time.Second)
	cases := []struct {
		name     string
		activity Activity
		stamp    time.Time
		open     bool
		spoken   bool
	}{
		{"a message of this life testifies", Activity{LastMessageAt: newer}, newer, true, true},
		{"a previous life's message does not", Activity{LastMessageAt: older}, newer, false, true},
		{"bookkeeping moving the file changes nothing", Activity{LastMessageAt: older}, older, false, true},
		{"without message dates the stamp decides", Activity{}, newer, true, true},
		{"a stale stamp without dates does not testify", Activity{}, older, false, true},
		{"a finished turn is never open", Activity{Finished: true, LastMessageAt: newer}, newer, false, true},
		{"an unanswered ask is waiting, not working", Activity{AwaitingApproval: true, LastMessageAt: newer}, newer, false, true},
		{"a boot note alone with a fresh stamp says nothing", Activity{Empty: true}, newer, false, false},
		{"an empty record is never judged by its stamp", Activity{Empty: true}, older, false, false},
	}
	for _, c := range cases {
		open, spoken := openTurn(c.activity, c.stamp, started)
		if open != c.open || spoken != c.spoken {
			t.Errorf("%s: openTurn = (%v, %v), want (%v, %v)", c.name, open, spoken, c.open, c.spoken)
		}
	}
}
