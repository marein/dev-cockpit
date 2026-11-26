package assistant

import (
	"slices"
	"testing"
)

// A codex coder steered under the key it was started with takes codex's own
// id with its first message, and its signals carry that id from then on. The
// job moves with it, so the signal under the new id buys the check and the
// job closes the way it does on a coder that had its id from the start.
func TestAJobFollowsItsTerminalToTheIdItTook(t *testing.T) {
	f := newJobFixture(t, "DONE: the README is written and mentions the project")
	f.steered(t)

	f.watcher.TerminalMoved("term-1", "record-1")
	if _, ok := f.jobs.Get("term-1"); ok {
		t.Fatal("the job still stands under the key the coder left")
	}
	moved, ok := f.jobs.Get("record-1")
	if !ok || moved.State != JobSteering || moved.Name != "readme-task" {
		t.Fatalf("the job under the new id is %+v, want the steered job", moved)
	}

	f.watcher.Handle("record-1")
	if done := f.waitJobState(t, "record-1", JobDone); done.Wakes != 1 {
		t.Fatalf("the check on the new id counted %d wakes, want 1", done.Wakes)
	}

	if err := f.watcher.Release("record-1", ""); err == nil {
		t.Fatal("a done job was released")
	}
}

// A job that already stands under the new id keeps it, and a move nobody
// steered changes nothing.
func TestAMoveKeepsAJobThatStandsUnderTheNewId(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	f.steered(t)
	if _, err := f.watcher.Steer(Job{Owner: f.owner.ID, Terminal: "record-1", Name: "own", DoneWhen: "it is done"}); err != nil {
		t.Fatalf("steer: %v", err)
	}

	f.watcher.TerminalMoved("term-1", "record-1")
	f.watcher.TerminalMoved("nobody", "record-2")
	if kept, _ := f.jobs.Get("record-1"); kept.Name != "own" {
		t.Fatalf("the job under the new id is %+v, want the one steered there", kept)
	}
	if _, ok := f.jobs.Get("term-1"); !ok {
		t.Fatal("the job under the key was dropped")
	}
	if _, ok := f.jobs.Get("record-2"); ok {
		t.Fatal("a move nobody steered made a job")
	}
}

// A trigger waiting for the job, or for the coder's news, waits for the
// terminal under the id it took, so the job's end still fires it.
func TestATriggerFollowsItsTerminalToTheIdItTook(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.steerJob(t, "late")
	onJob := f.trigger(t, TriggerSpec{Event: "job-closed", Task: "MAGIC next", Targets: targets("term-late"), BatchSet: true})
	onNews := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "MAGIC answer", Targets: targets("term-late", "term-other"), BatchSet: true})

	NewWatcher(f.svc, NewJobs(f.store), nil, nil).TerminalMoved("term-late", "record-late")
	for _, id := range []string{onJob.ID, onNews.ID} {
		got, _ := f.reactor.Get(id)
		if terminals := got.Terminals(); slices.Contains(terminals, "term-late") || !slices.Contains(terminals, "record-late") {
			t.Fatalf("trigger %s waits for %v, want the new id instead of the key", id, terminals)
		}
	}
	if got, _ := f.reactor.Get(onNews.ID); !slices.Contains(got.Terminals(), "term-other") {
		t.Fatalf("the other target of %s is gone: %v", onNews.ID, got.Terminals())
	}

	ev := f.jobDone("late")
	ev.Target = "record-late"
	f.reactor.Publish(ev)
	f.waitPushed(t, 1)
}
