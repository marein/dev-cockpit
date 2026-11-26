package coder

import (
	"fmt"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/tmux"
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

type lifeCoder struct{ Coder }

func (lifeCoder) ActivityProfile() ActivityProfile { return ActivityProfile{} }

// A coder stopped and resumed between two ticks of the watch is back under
// its id on a new pane before the watch could see it leave. That pane is a
// new life: it leaves and enters again, under its own start, so the tracker
// forgets the turn the previous life left open, exactly as after a slower
// resume. The same pane seen again is no new life.
func TestANewPaneUnderTheSameIDIsANewLife(t *testing.T) {
	m := NewManager(config.Config{SnapshotCacheTTL: time.Hour}, tmux.New(), lifeCoder{}, nil)
	first := time.Date(2026, 11, 25, 11, 50, 30, 0, time.UTC)
	resumed := time.Date(2026, 11, 25, 11, 50, 42, 695e6, time.UTC)
	life := func(pid string, at time.Time) {
		m.snapshots.put(Snapshot{Running: []Running{{Identifier: "work", TmuxSession: "work", PID: pid, Name: "work", StartedAt: at}}})
	}
	events := make(chan string, 16)
	life("100", first)
	go m.RunTurnWatch(5*time.Millisecond, TurnWatch{
		OnSeen:    func(id string, at time.Time) { events <- fmt.Sprintf("seen %s %s", id, at.Format(time.StampMilli)) },
		OnGone:    func(id string) { events <- "gone " + id },
		OnRenamed: func(id, name, cwd string) { events <- "renamed " + id },
		RecordOf:  func(id string) string { return id },
	})
	next := func() string {
		select {
		case e := <-events:
			return e
		case <-time.After(2 * time.Second):
			return "nothing"
		}
	}
	if got := next(); got != "seen work "+first.Format(time.StampMilli) {
		t.Fatalf("the first life reads %q", got)
	}
	time.Sleep(50 * time.Millisecond)
	select {
	case e := <-events:
		t.Fatalf("the same pane seen again reads %q", e)
	default:
	}
	life("200", resumed)
	for _, want := range []string{"gone work", "seen work " + resumed.Format(time.StampMilli)} {
		if got := next(); got != want {
			t.Fatalf("the new pane reads %q, want %q", got, want)
		}
	}
}
