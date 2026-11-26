package tmux

import (
	"maps"
	"testing"
	"time"
)

// Every pane of every window names its session, a session name may hold the
// separator itself, and a line that names no process or no session is
// skipped.
func TestParsePaneSessions(t *testing.T) {
	got := parsePaneSessions("101\twork\n102\twork\n203\tmy\tlab\n\nx\tbad\n0\tzero\n-4\tneg\n305\t\n")
	want := map[int]string{101: "work", 102: "work", 203: "my\tlab"}
	if !maps.Equal(got, want) {
		t.Fatalf("the panes read %v, want %v", got, want)
	}
}

// A coder's pane starts when the cockpit launched it, to the millisecond: a
// resume within the second its previous life last spoke in must still tell
// that word from the first of its own. A pane without the stamp, a shell or
// one launched before the stamp existed, starts at tmux's whole second.
func TestAPaneStartsWhenItsCoderWasLaunched(t *testing.T) {
	line := func(created, at string) string {
		return "work\t4711\t" + created + "\t0\t0\t\t\tcodex\twork\t/work\t\t\t\t\t\t" + at + "\n"
	}
	cases := []struct {
		name, at string
		want     time.Time
	}{
		{"the launch moment", "1764071442695", time.UnixMilli(1764071442695).UTC()},
		{"no stamp", "", time.Unix(1764071442, 0).UTC()},
		{"a broken stamp", "soon", time.Unix(1764071442, 0).UTC()},
	}
	for _, c := range cases {
		panes := parsePanes(line("1764071442", c.at))
		if len(panes) != 1 {
			t.Fatalf("%s: parsed %d panes, want one", c.name, len(panes))
		}
		if got := panes[0].StartTime(); !got.Equal(c.want) {
			t.Errorf("%s: the pane starts at %v, want %v", c.name, got, c.want)
		}
	}
}
