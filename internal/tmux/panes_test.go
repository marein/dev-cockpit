package tmux

import (
	"maps"
	"testing"
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
