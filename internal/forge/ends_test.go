package forge

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"
)

// endsOf has f hear of the pipeline ends its service publishes, and answers
// what it heard so far, by status.
func (f *pollFixture) endsOf() func() []Status {
	var heard []Status
	f.s.OnPipelineEnd(func(end PipelineEnd) {
		f.mu.Lock()
		defer f.mu.Unlock()
		heard = append(heard, end.Status.Pipeline.Status)
	})
	return func() []Status {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := heard
		heard = nil
		return out
	}
}

// restart is a new process on the same state directory, which knows
// nothing of what the one before it polled.
func (f *pollFixture) restart() {
	f.s = New(f.dir, []Provider{f.stub}, f.host)
	f.s.poll.now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.now
	}
}

func (f *pollFixture) pipeline(head, id string, status Status) {
	f.stub.mu.Lock()
	defer f.stub.mu.Unlock()
	f.stub.answers[head] = BranchStatus{Change: &Change{Number: 4, State: Open}, Pipeline: &Pipeline{ID: id, Status: status}}
}

func (f *pollFixture) expect(heard func() []Status, want ...Status) {
	f.t.Helper()
	if got := heard(); !slices.Equal(got, want) {
		f.t.Fatalf("heard the ends %q, want %q", got, want)
	}
}

// A pipeline that runs and ends is published at its end, once per final
// status: a retry that ends the same way again is no news, one that ends
// another way is.
func TestAPipelineEndIsPublishedOncePerPipelineAndFinalStatus(t *testing.T) {
	f := newPollFixture(t)
	heard := f.endsOf()
	w := f.watch("app", "feature", "a1")
	f.pipeline("a1", "12", Running)
	f.round(w)
	f.expect(heard)
	f.pipeline("a1", "12", Failed)
	f.pass(pollFast)
	f.round(w)
	f.expect(heard, Failed)
	f.pass(pollSlow)
	f.round(w)
	f.expect(heard)
	if _, err := f.s.Status(context.Background(), w, true); err != nil {
		t.Fatal(err)
	}
	f.expect(heard)
	f.pipeline("a1", "12", Running)
	f.pass(pollSlow)
	f.round(w)
	f.pipeline("a1", "12", Success)
	f.pass(pollFast)
	f.round(w)
	f.expect(heard, Success)
	f.pipeline("a1", "12", Failed)
	f.pass(pollSlow)
	f.round(w)
	f.expect(heard)
}

// The read behind forge-show sees an end first where it reads before the
// poller does, and the poller's read after it publishes nothing more. A read
// of another branch than the one the poller keeps publishes nothing.
func TestTheStatusReadPublishesAnEndItSeesFirst(t *testing.T) {
	f := newPollFixture(t)
	heard := f.endsOf()
	w := f.watch("app", "feature", "a1")
	f.pipeline("a1", "12", Running)
	f.round(w)
	other := f.watch("app", "main", "b1")
	f.pipeline("b1", "7", Running)
	if _, err := f.s.Status(context.Background(), other, true); err != nil {
		t.Fatal(err)
	}
	f.pipeline("b1", "7", Failed)
	if _, err := f.s.Status(context.Background(), other, true); err != nil {
		t.Fatal(err)
	}
	f.expect(heard)
	f.pipeline("a1", "12", Canceled)
	if _, err := f.s.Status(context.Background(), w, true); err != nil {
		t.Fatal(err)
	}
	f.expect(heard, Canceled)
	f.pass(pollFast)
	f.round(w)
	f.expect(heard)
}

// What ended before the service ever read the branch is no end: a host that
// got its token, a project bound or a branch checked out finds the last
// pipeline finished long ago. The next pipeline of that branch is news,
// whether it is read running or only once it ended.
func TestAPipelineSeenFirstAtItsEndIsNoEnd(t *testing.T) {
	f := newPollFixture(t)
	heard := f.endsOf()
	w := f.watch("app", "feature", "a1")
	f.pipeline("a1", "12", Failed)
	f.round(w)
	f.expect(heard)
	pushed := f.watch("app", "feature", "a2")
	f.pipeline("a2", "13", Success)
	f.round(pushed)
	f.expect(heard, Success)
	main := f.watch("app", "main", "b1")
	f.pipeline("b1", "7", Failed)
	f.round(main)
	f.expect(heard)
	f.pipeline("a2", "13", Failed)
	f.round(pushed)
	f.expect(heard)
}

// A pipeline the service saw running and that ended while the server was
// down is published by the next process, once, and the process after that
// publishes it no more.
func TestAPipelineEndSurvivesARestart(t *testing.T) {
	f := newPollFixture(t)
	heard := f.endsOf()
	w := f.watch("app", "feature", "a1")
	f.pipeline("a1", "12", Running)
	f.round(w)
	f.restart()
	heard = f.endsOf()
	f.pipeline("a1", "12", Failed)
	f.round(w)
	f.expect(heard, Failed)
	f.restart()
	heard = f.endsOf()
	f.round(w)
	f.expect(heard)
	f.pipeline("a2", "13", Success)
	f.restart()
	heard = f.endsOf()
	f.round(f.watch("app", "feature", "a2"))
	f.expect(heard, Success)
}

// A project that is gone takes its record along: one made again under its
// name is read for the first time, its last end is no news.
func TestAForgottenProjectIsReadForTheFirstTime(t *testing.T) {
	f := newPollFixture(t)
	heard := f.endsOf()
	w := f.watch("app", "feature", "a1")
	f.pipeline("a1", "12", Running)
	f.round(w)
	f.s.Forget("app")
	f.round()
	f.pipeline("a1", "12", Failed)
	f.round(w)
	f.expect(heard)
}

// GitHub numbers no pipeline, the checks of a commit are one: a push is a
// new one, the same commit read again the same one.
func TestAPipelineWithoutIDIsTheOneOfItsCommit(t *testing.T) {
	f := newPollFixture(t)
	heard := f.endsOf()
	f.pipeline("a1", "", Running)
	f.round(f.watch("app", "feature", "a1"))
	f.pipeline("a1", "", Failed)
	f.pass(pollFast)
	f.round(f.watch("app", "feature", "a1"))
	f.expect(heard, Failed)
	f.pipeline("a2", "", Failed)
	f.round(f.watch("app", "feature", "a2"))
	f.expect(heard, Failed)
}

// A record it cannot read is no record of nothing: nothing is published
// and nothing written over it, a pipeline waiting for a person has not
// ended, and a record nothing moved for longer than endsKept is forgotten.
func TestPipelineEndsPublishNothingFromAFileTheyCannotRead(t *testing.T) {
	f := newPollFixture(t)
	heard := f.endsOf()
	w := f.watch("app", "feature", "a1")
	f.pipeline("a1", "12", Running)
	f.round(w)
	f.pipeline("a1", "12", Manual)
	f.pass(pollFast)
	f.round(w)
	f.expect(heard)
	saved, err := os.ReadFile(f.s.endsPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.s.endsPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.s.endsPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	f.pipeline("a1", "12", Failed)
	f.pass(pollSlow)
	f.round(w)
	f.expect(heard)
	if err := os.Remove(f.s.endsPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.s.endsPath(), saved, 0o600); err != nil {
		t.Fatal(err)
	}
	f.pass(pollSlow)
	f.round(w)
	f.expect(heard, Failed)
	f.pass(endsKept + time.Hour)
	f.pipeline("c1", "3", Running)
	f.round(f.watch("lib", "main", "c1"))
	f.pipeline("a1", "12", Success)
	f.round(w)
	f.expect(heard)
}
