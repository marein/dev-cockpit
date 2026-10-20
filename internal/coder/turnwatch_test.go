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

// A tail the coder's hooks speak for is no word of the record's: claude
// answers the `!` output, works on a task notification and leaves a
// compaction summary behind, and its hooks report all of it.
func TestAnAsideTailSaysNothing(t *testing.T) {
	started := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	newer := started.Add(time.Second)
	if open, spoken := openTurn(Activity{Aside: true, LastMessageAt: newer}, newer, started); open || spoken {
		t.Fatalf("an aside tail spoke: (%v, %v)", open, spoken)
	}
}

// The record's word is dated by its newest message, not by the file's stamp:
// bookkeeping moves the stamp, and the same message read again under a newer
// stamp would otherwise overrule a hook that fired after it.
func TestARecordWordIsDatedByItsMessage(t *testing.T) {
	message := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	stamp := message.Add(time.Minute)
	if got := wordTime(Activity{LastMessageAt: message}, stamp); !got.Equal(message) {
		t.Fatalf("wordTime = %v, want the message's time", got)
	}
	if got := wordTime(Activity{}, stamp); !got.Equal(stamp) {
		t.Fatalf("wordTime = %v, want the stamp without message dates", got)
	}
}
