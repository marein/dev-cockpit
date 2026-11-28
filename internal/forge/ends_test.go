package forge

import (
	"context"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// endsOf has f take the pipeline ends its service hands on, and answers
// what it took so far, by status.
func (f *pollFixture) endsOf() func() []Status {
	var heard []Status
	f.s.OnPipelineEnd(func(end PipelineEnd) {
		f.mu.Lock()
		defer f.mu.Unlock()
		heard = append(heard, end.Status.Pipeline.Status)
	}, nil)
	return func() []Status {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := heard
		heard = nil
		return out
	}
}

// ended reads the final statuses the record on disk holds of a project's
// pipeline as handed on.
func (f *pollFixture) ended(project string) []Status {
	f.t.Helper()
	var file endsFile
	if err := statefile.Read(f.s.endsPath(), &file); err != nil {
		f.t.Fatal(err)
	}
	return file.Projects[project].Ended
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

// The one that takes a pipeline's end hears of it while the record does not
// hold it yet, so a process that dies there reads it again, and everybody
// else is told once the record holds it.
func TestAPipelineEndIsTakenBeforeItsRecordMovesAndToldAfter(t *testing.T) {
	f := newPollFixture(t)
	w := f.watch("app", "feature", "a1")
	f.pipeline("a1", "12", Running)
	f.round(w)
	var order []string
	f.s.OnPipelineEnd(func(end PipelineEnd) {
		if held := f.ended("app"); len(held) != 0 {
			t.Errorf("the record held %q before the end was taken", held)
		}
		order = append(order, "taken "+string(end.Status.Pipeline.Status))
	}, func(end PipelineEnd) {
		if held := f.ended("app"); !slices.Equal(held, []Status{Failed}) {
			t.Errorf("the record held %q when the end was told", held)
		}
		order = append(order, "told "+string(end.Status.Pipeline.Status))
	})
	f.pipeline("a1", "12", Failed)
	f.pass(pollFast)
	f.round(w)
	if want := []string{"taken failed", "told failed"}; !slices.Equal(order, want) {
		t.Fatalf("the end was heard as %q, want %q", order, want)
	}
}

// A process that dies while an end of any kind is taken leaves the record
// where it stood: the next one reads the end again and hands it again, tells
// it once its record holds it, and the one after that hears nothing. Nobody
// was told by the process that died.
func TestAPipelineEndIsHandedAgainUntilItsRecordMoved(t *testing.T) {
	for _, status := range []Status{Success, Failed, Canceled, Skipped} {
		t.Run(string(status), func(t *testing.T) {
			f := newPollFixture(t)
			w := f.watch("app", "feature", "a1")
			f.pipeline("a1", "12", Running)
			f.round(w)
			var taken, told []Status
			hear := func(dies bool) {
				f.s.OnPipelineEnd(func(end PipelineEnd) {
					taken = append(taken, end.Status.Pipeline.Status)
					if dies {
						runtime.Goexit()
					}
				}, func(end PipelineEnd) { told = append(told, end.Status.Pipeline.Status) })
			}
			hear(true)
			f.pipeline("a1", "12", status)
			f.pass(pollFast)
			f.round(w)
			if !slices.Equal(taken, []Status{status}) || len(told) != 0 || len(f.ended("app")) != 0 {
				t.Fatalf("the process that died took %q, told %q and left %q in the record", taken, told, f.ended("app"))
			}
			f.restart()
			hear(false)
			f.round(w)
			if !slices.Equal(taken, []Status{status, status}) || !slices.Equal(told, []Status{status}) || !slices.Equal(f.ended("app"), []Status{status}) {
				t.Fatalf("the next process took %q, told %q and left %q in the record", taken, told, f.ended("app"))
			}
			f.restart()
			hear(false)
			f.round(w)
			if len(taken) != 2 || len(told) != 1 {
				t.Fatalf("a process after the record moved took %q and told %q", taken, told)
			}
		})
	}
}

// An end whose record cannot be written is handed to the one that takes it
// with every read, which tells an end handed again by its name, and told to
// nobody else until the record holds it, then once.
func TestAPipelineEndWhoseRecordCannotMoveIsToldOnceItDoes(t *testing.T) {
	f := newPollFixture(t)
	w := f.watch("app", "feature", "a1")
	f.pipeline("a1", "12", Running)
	f.round(w)
	taken, told := 0, 0
	f.s.OnPipelineEnd(func(PipelineEnd) { taken++ }, func(PipelineEnd) { told++ })
	if err := os.Mkdir(f.s.endsPath()+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	f.pipeline("a1", "12", Failed)
	for range 2 {
		f.pass(pollSlow)
		f.round(w)
	}
	if taken != 2 || told != 0 || len(f.ended("app")) != 0 {
		t.Fatalf("with a record that cannot move the end was taken %d times and told %d times, the record holds %q", taken, told, f.ended("app"))
	}
	if err := os.Remove(f.s.endsPath() + ".tmp"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		f.pass(pollSlow)
		f.round(w)
	}
	if taken != 3 || told != 1 {
		t.Fatalf("once the record moved the end was taken %d times and told %d times in all, want 3 and 1", taken, told)
	}
}

// Two reads that see one end, the poller's and the one behind forge-show,
// hand it on once: the second waits until the record of the first moved.
func TestTwoReadsOfOneEndHandItOnOnce(t *testing.T) {
	f := newPollFixture(t)
	w := f.watch("app", "feature", "a1")
	f.pipeline("a1", "12", Running)
	f.round(w)
	taken, told := 0, 0
	second := make(chan struct{})
	f.s.OnPipelineEnd(func(PipelineEnd) {
		if taken++; taken == 1 {
			go func() {
				defer close(second)
				if _, err := f.s.Status(context.Background(), w, true); err != nil {
					t.Error(err)
				}
			}()
			for reads := f.reads(); f.reads() == reads; time.Sleep(time.Millisecond) {
			}
			time.Sleep(20 * time.Millisecond)
		}
	}, func(PipelineEnd) {
		f.mu.Lock()
		defer f.mu.Unlock()
		told++
	})
	f.pipeline("a1", "12", Failed)
	f.pass(pollFast)
	f.round(w)
	<-second
	f.mu.Lock()
	defer f.mu.Unlock()
	if taken != 1 || told != 1 {
		t.Fatalf("two reads of one end handed it on %d times and told it %d times", taken, told)
	}
}
