package coder

import (
	"testing"
	"time"
)

// A pass is final: the check never runs again, no matter how much time passes.
func TestCapabilityProbeCachesAPassForever(t *testing.T) {
	runs := 0
	p := NewCapabilityProbe(func() bool {
		runs++
		return true
	}, 0)
	for i := 0; i < 3; i++ {
		if !p.Passed() {
			t.Fatal("a passed probe must stay passed")
		}
	}
	if runs != 1 {
		t.Fatalf("want the check run once, got %d", runs)
	}
}

// A miss holds only for the pause, then the check runs again and a recovered
// CLI turns the probe around. This is the case the probe exists for: a check
// that failed because of the moment must not disable the capability until the
// next server restart.
func TestCapabilityProbeRetriesAMissAfterThePause(t *testing.T) {
	runs := 0
	p := NewCapabilityProbe(func() bool {
		runs++
		return runs > 1
	}, 50*time.Millisecond)
	if p.Passed() {
		t.Fatal("the first check fails, the probe must report a miss")
	}
	if p.Passed() {
		t.Fatal("inside the pause the miss is trusted, the check must not run")
	}
	if runs != 1 {
		t.Fatalf("want the check run once inside the pause, got %d runs", runs)
	}
	time.Sleep(60 * time.Millisecond)
	if !p.Passed() {
		t.Fatal("after the pause the recovered check must turn the probe around")
	}
	if !p.Passed() {
		t.Fatal("the pass must stick")
	}
	if runs != 2 {
		t.Fatalf("want the check run twice in total, got %d", runs)
	}
}
