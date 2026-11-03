package assistant

import (
	"strings"
	"testing"
	"time"
)

// A coder that ended its turn to wait for its own background run is idle for the
// cockpit, and a check that saw the run going answered WORKING without sending
// anything, which the standstill rule closes as blocked. That closed a reviewer
// waiting on its own e2e runners. The prompt names what the check sees and sends
// it to NOTHING, next to the standstill rule, which stays.
func TestTheWakePromptLeavesABackgroundRunToTheCoder(t *testing.T) {
	prompt := wakePrompt(Job{Terminal: "term-1", DoneWhen: "e2e PASS"}, Activity{Text: "coder: waiting for the runner", Finished: true})
	for _, want := range []string{
		"A coder that ended its turn to wait for a run it started in the background is not stuck",
		"its screen counts that run as a shell or task still running",
		"Send it nothing then and answer NOTHING: the job stays open",
		"a WORKING without that is turned into BLOCKED anyway",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt misses %q:\n%s", want, prompt)
		}
	}
}

// NOTHING is what that check answers, and on an idle coder it has to leave the
// job open and quiet: the standstill rule reads WORKING alone.
func TestNothingOnAnIdleCoderKeepsTheJobOpen(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	f.sessions.set(Activity{Text: "coder: waiting for the runner", Finished: true})
	c, _ := f.steered(t)

	f.watcher.Handle("term-1")
	f.waitWakes(t, "term-1", 1)
	quiesce(t, f.svc, f.watcher)

	job, _ := f.jobs.Get("term-1")
	if job.State != JobSteering {
		t.Fatalf("want the job still steering, got %q", job.State)
	}
	fresh, _ := f.svc.Get(c.ID)
	if len(fresh.Messages) != 0 {
		t.Fatalf("want no report for a NOTHING, got %d messages", len(fresh.Messages))
	}
	select {
	case <-f.news:
		t.Fatal("want no news for a NOTHING")
	case <-time.After(100 * time.Millisecond):
	}
}
