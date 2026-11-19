package web

import (
	"os"
	"testing"
	"time"
)

// The walk names the nearest process the cockpit knows, a turn's or a pane's,
// reads the panes only once it needs them and ends at the cockpit's own
// process, above which nothing runs for a caller.
func TestTheNearestKnownProcessNamesTheCaller(t *testing.T) {
	const own = 99
	for _, c := range []struct {
		name          string
		chain         []int
		turns, panes  map[int]string
		turn, session string
		reads         int
	}{
		{"a pane below a turn", []int{10, 20, 30, 1}, map[int]string{30: "a1"}, map[int]string{20: "s1"}, "", "s1", 2},
		{"a turn below a pane", []int{10, 20, 30, 1}, map[int]string{20: "a1"}, map[int]string{30: "s1"}, "a1", "", 1},
		{"the process itself", []int{20, 1}, nil, map[int]string{20: "s1"}, "", "s1", 1},
		{"above the cockpit", []int{10, own, 20, 1}, map[int]string{20: "a1"}, map[int]string{20: "s1"}, "", "", 1},
		{"the cockpit itself", []int{own, 20}, nil, map[int]string{20: "s1"}, "", "", 0},
		{"nothing known", []int{10, 20, 1}, map[int]string{}, map[int]string{}, "", "", 3},
		{"no tmux", []int{10, 20, 1}, nil, nil, "", "", 3},
		{"no process", []int{0, 20}, nil, map[int]string{20: "s1"}, "", "", 0},
		{"no chain", nil, nil, map[int]string{20: "s1"}, "", "", 0},
	} {
		reads := 0
		turn, session := anchorOf(c.chain, own, c.turns, func() map[int]string { reads++; return c.panes })
		if turn != c.turn || session != c.session || reads != c.reads {
			t.Errorf("%s reads %q %q after %d reads of the panes, want %q %q after %d", c.name, turn, session, reads, c.turn, c.session, c.reads)
		}
	}
}

// A lookup answers its names within the bound, one past it answers none in
// the bound's time, so it never holds a question longer.
func TestALookupNeverHoldsAQuestionPastItsBound(t *testing.T) {
	if got := within(2, func() []string { return []string{"shell in shop", ""} }); len(got) != 2 || got[0] != "shell in shop" || got[1] != "" {
		t.Fatalf("a quick lookup reads %q", got)
	}
	release := make(chan struct{})
	defer close(release)
	start := time.Now()
	got := within(2, func() []string { <-release; return []string{"late", "late"} })
	if took := time.Since(start); len(got) != 2 || got[0] != "" || got[1] != "" || took < callerNameWait || took > 3*callerNameWait {
		t.Fatalf("a slow lookup reads %q after %v", got, took)
	}
}

// The cockpit's own process runs for nobody, the way a git action of the UI
// does not, and neither does a process that is gone.
func TestTheCockpitsOwnProcessIsNamedByNobody(t *testing.T) {
	s := &Server{}
	if got := s.processName(os.Getpid()); got != "" {
		t.Fatalf("the cockpit's own process reads %q", got)
	}
	if got := s.callerNames([][]int{{os.Getpid()}, nil}); len(got) != 2 || got[0] != "" || got[1] != "" {
		t.Fatalf("the callers read %q", got)
	}
}
