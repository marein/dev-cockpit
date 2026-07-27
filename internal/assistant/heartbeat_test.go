package assistant

import (
	"strings"
	"testing"
	"time"
)

// A coder that stopped sends nothing, and nothing used to happen: the whole
// feature hung on a signal. This is the case that stood still for half an hour.
func TestAStandingJobIsCheckedWithoutAnySignal(t *testing.T) {
	f := newJobFixture(t, "BLOCKED: it needs a decision")
	f.steered(t)
	f.sessions.activity = Activity{Text: "the coder said its piece and stopped", Finished: true}
	f.watcher.quiet = 0

	f.watcher.Heartbeat()

	job := f.waitWakes(t, "term-1", 1)
	if job.Wakes != 1 {
		t.Fatalf("want exactly one check, got %d", job.Wakes)
	}
	if len(f.runner.turns()) != 1 {
		t.Fatalf("want one turn spent, got %d", len(f.runner.turns()))
	}
}

// A job whose time is up has no signal left to give, so its last word has to
// come from the pass itself. It used to be written only when a coder happened to
// report, which is exactly what a finished job never does.
func TestAnExpiredJobReportsFromTheHeartbeat(t *testing.T) {
	f := newJobFixture(t, "DONE: x")
	c, job := f.steered(t)
	job.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	f.jobs.Save(job)

	f.watcher.Heartbeat()

	fresh, _ := f.jobs.Get("term-1")
	if fresh.State != JobExpired {
		t.Fatalf("want the job expired, got %q", fresh.State)
	}
	if len(f.runner.turns()) != 0 {
		t.Fatal("want no turn spent on a job that ran out")
	}
	conversation, err := f.svc.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var reports []Message
	for _, m := range conversation.Messages {
		if m.Wake != nil {
			reports = append(reports, m)
		}
	}
	if len(reports) != 1 {
		t.Fatalf("want the expiry report written, got %d messages", len(reports))
	}
	if !strings.Contains(reports[0].Content, "stopped steering") {
		t.Fatalf("want the report to say nobody is steering, got %q", reports[0].Content)
	}
	select {
	case <-f.news:
	default:
		t.Fatal("want the expiry to reach the user")
	}
}

// The pass is free until it decides otherwise. A coder that is visibly working
// is read and left alone, however often the heartbeat comes around.
func TestAWorkingCoderCostsNoCheck(t *testing.T) {
	f := newJobFixture(t, "DONE: x")
	f.steered(t)
	f.sessions.activity = Activity{Text: "still writing the file", Finished: false}
	f.watcher.quiet = 0

	f.watcher.Heartbeat()
	f.watcher.Heartbeat()
	f.watcher.Heartbeat()

	if turns := f.runner.turns(); len(turns) != 0 {
		t.Fatalf("want a working coder to cost nothing, got %d turn(s)", len(turns))
	}
	job, _ := f.jobs.Get("term-1")
	if job.Wakes != 0 {
		t.Fatalf("want no check counted, got %d", job.Wakes)
	}
}

// A coder that looks busy but has not moved for a long time is not working, it
// is stuck in a way it cannot report. That one is worth a turn: the check reads
// the screen and gets it going again.
func TestACoderThatStoppedMovingIsCheckedAnyway(t *testing.T) {
	f := newJobFixture(t, "BLOCKED: it is stuck")
	f.steered(t)
	f.sessions.activity = Activity{Text: "the same picture as before", Finished: false}
	f.watcher.quiet = 0

	f.watcher.Heartbeat()
	if len(f.runner.turns()) != 0 {
		t.Fatal("want the first sighting to cost nothing")
	}
	// The picture has not changed since, and by now it has stood long enough.
	f.watcher.stall = 0
	f.watcher.Heartbeat()

	f.waitWakes(t, "term-1", 1)
}

// A terminal that is gone can never meet its criterion, and the signal that
// would have said so died with the session. The job that was left standing after
// a lost tmux server is exactly this case: it has to end, and the user has to
// hear it.
func TestAJobWhoseTerminalIsGoneIsEndedAndReported(t *testing.T) {
	f := newJobFixture(t, "DONE: x")
	c, job := f.steered(t)
	// Old enough to be past the grace a starting coder gets.
	job.CreatedAt = time.Now().UTC().Add(-time.Hour)
	f.jobs.Save(job)
	f.sessions.gone = map[string]bool{"term-1": true}

	f.watcher.Heartbeat()

	fresh, _ := f.jobs.Get("term-1")
	if fresh.State != JobExpired {
		t.Fatalf("want the job ended, got %q", fresh.State)
	}
	if len(f.runner.turns()) != 0 {
		t.Fatal("want no turn spent asking a coder that is not there")
	}
	conversation, _ := f.svc.Get(c.ID)
	last, ok := conversation.Last()
	if !ok || last.Wake == nil {
		t.Fatal("want the end of the job reported")
	}
	if !strings.Contains(last.Content, "is gone") {
		t.Fatalf("want the report to say the coder is gone, got %q", last.Content)
	}
	select {
	case <-f.news:
	default:
		t.Fatal("want the user to hear about it")
	}
}

// A coder that was started a moment ago is not in the session list yet, and a
// job that ended for that would be a promise broken in the quietest way.
func TestAFreshJobIsNotEndedForATerminalThatIsStillComingUp(t *testing.T) {
	f := newJobFixture(t, "DONE: x")
	f.steered(t)
	f.sessions.gone = map[string]bool{"term-1": true}

	f.watcher.Heartbeat()

	fresh, _ := f.jobs.Get("term-1")
	if fresh.State != JobSteering {
		t.Fatalf("want a fresh job left alone, got %q", fresh.State)
	}
}
