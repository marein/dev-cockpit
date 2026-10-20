package activity

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func persistedTracker(t *testing.T, path string, at *time.Time) *Tracker {
	t.Helper()
	tracker := NewTracker()
	tracker.now = func() time.Time { return *at }
	tracker.Persist(path)
	return tracker
}

// The account outlives the process: a restart keeps every session's last
// word, and the words that come after are ordered against it by their own
// time, the newer one winning.
func TestTheAccountSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "working-marks.json")
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	before := persistedTracker(t, path, &at)
	before.SetTurn("working", true, true, at)
	before.SetWaiting("asking", at)
	before.SetTurn("idle", false, false, at)
	before.SetRecord("idle", "cleared", at)

	after := persistedTracker(t, path, &at)
	if after.Working()["working"] == 0 || after.Working()["asking"] != 0 || after.Working()["idle"] != 0 {
		t.Fatalf("the restart lost a word: %v", after.Working())
	}
	if got := after.Record("idle"); got != "cleared" {
		t.Fatalf("Record = %q, want the kept one", got)
	}
	after.SetTurn("working", true, false, at.Add(-time.Second))
	if after.Working()["working"] == 0 {
		t.Fatal("an older word must not overrule the kept one")
	}
	after.SetTurn("working", false, false, at.Add(time.Second))
	if after.Working()["working"] != 0 {
		t.Fatal("a hook written while the server was down closes the kept turn")
	}
	after.Answered("asking", at.Add(time.Second), false)
	if after.Working()["asking"] == 0 {
		t.Fatal("a kept ask is answered like a live one")
	}
}

// A session that leaves the running set leaves the file with it, and one a
// restart brought back but no watcher claims, its terminal ended while the
// server was down, leaves with the sweep.
func TestAGoneSessionLeavesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "working-marks.json")
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	before := persistedTracker(t, path, &at)
	before.SetTurn("stopped", true, false, at)
	before.SetTurn("ended", true, false, at)
	before.Forget("stopped")

	after := persistedTracker(t, path, &at)
	if _, ok := after.sessions["stopped"]; ok {
		t.Fatal("a forgotten session came back")
	}
	after.Configure("alive", Policy{}, at)
	after.SetTurn("alive", false, false, at)
	at = at.Add(sweepKeep + time.Second)
	after.sweep()
	if _, ok := after.sessions["ended"]; ok {
		t.Fatal("an unclaimed session outlives the sweep")
	}
	again := persistedTracker(t, path, &at)
	if _, ok := again.sessions["ended"]; ok {
		t.Fatal("the swept session is still in the file")
	}
	if _, ok := again.sessions["alive"]; !ok {
		t.Fatal("a claimed session left the file")
	}
}

// A file that does not parse is quarantined and the tracker starts empty.
func TestABrokenFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "working-marks.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	tracker := persistedTracker(t, path, &at)
	if len(tracker.sessions) != 0 {
		t.Fatalf("want no sessions, got %v", tracker.sessions)
	}
	if _, err := os.Stat(path + ".broken"); err != nil {
		t.Fatalf("the broken file was not kept: %v", err)
	}
}
