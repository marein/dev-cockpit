package forge

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// The poller hands a pipeline's end and a review note on before its record
// of them moves. These tests let a process die in the middle of that, over
// several projects, a working copy that changes its branch and a forge that
// fails or limits its requests, start the next one over the records it left,
// and hold that nothing the forge still tells is lost and nothing the records
// hold is handed on again.

// handover is what the processes of one test were handed and told, by name:
// "end app 12 failed" and "note app n2". A process dies while a hand over is
// taken, the one left counts down to, 0 for never, and nothing it does after
// that is heard.
type handover struct {
	f      *notesFixture
	mu     sync.Mutex
	handed []string
	told   []string
	left   int
	dead   bool
}

// process is the next process over the state directory, which dies while the
// hand over with the number die is taken, 0 for never. Whatever it is handed
// must not stand in a record yet.
func (h *handover) process(die int) {
	h.f.restart()
	h.mu.Lock()
	h.left, h.dead = die, false
	h.mu.Unlock()
	take := func(name string, held bool) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.dead {
			runtime.Goexit()
		}
		if held {
			h.f.t.Errorf("%q was handed on while the record held it", name)
		}
		h.handed = append(h.handed, name)
		if h.left > 0 {
			if h.left--; h.left == 0 {
				h.dead = true
				runtime.Goexit()
			}
		}
	}
	h.f.s.OnPipelineEnd(func(end PipelineEnd) {
		pl := end.Status.Pipeline
		take(fmt.Sprintf("end %s %s %s", end.Watch.Project, pl.ID, pl.Status), h.ended(end.Watch.Project, pl.ID, pl.Status))
	}, func(end PipelineEnd) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if !h.dead {
			h.told = append(h.told, fmt.Sprintf("end %s %s %s", end.Watch.Project, end.Status.Pipeline.ID, end.Status.Pipeline.Status))
		}
	})
	h.f.s.OnReviewNote(func(n ReviewNote) {
		take("note "+n.Watch.Project+" "+n.Note.ID, h.noted(n.Watch.Project, n.Change.Number, n.Note.ID))
	})
}

// ended reports whether the record on disk holds that final status of a
// project's pipeline as handed on.
func (h *handover) ended(project, pipeline string, status Status) bool {
	var file endsFile
	if err := statefile.Read(h.f.s.endsPath(), &file); err != nil {
		h.f.t.Error(err)
	}
	r := file.Projects[project]
	return r.Pipeline == "#"+pipeline && slices.Contains(r.Ended, status)
}

// noted reports whether the record on disk holds a note of a project's
// change request.
func (h *handover) noted(project string, change int, id string) bool {
	var file notesFile
	if err := statefile.Read(h.f.s.notesPath(), &file); err != nil {
		h.f.t.Error(err)
	}
	for _, r := range file.Projects[project] {
		if r.Change == change && slices.Contains(r.Notes, id) {
			return true
		}
	}
	return false
}

// heard answers what was handed on since the last call, sorted, since two
// projects are read side by side.
func (h *handover) heard() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := slices.Sorted(slices.Values(h.handed))
	h.handed = nil
	return out
}

func (h *handover) expect(want ...string) {
	h.f.t.Helper()
	if got := h.heard(); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		h.f.t.Fatalf("handed on %q, want %q", got, want)
	}
}

// stands has the branch at head stand in a change request with that pipeline
// in that status.
func (f *notesFixture) stands(head string, change int, pipeline string, status Status) {
	f.stub.mu.Lock()
	defer f.stub.mu.Unlock()
	f.stub.answers[head] = BranchStatus{Change: &Change{Number: change, State: Open}, Pipeline: &Pipeline{ID: pipeline, Status: status}}
}

// Two projects on branches of their own, each with a pipeline that ends and
// notes of somebody else and of the token's own account, and a process that
// dies at any one hand over: the next process hands on the one it died at
// and whatever the records do not hold, the one after that nothing, no note
// of the token's own account is ever handed on, and no end is told twice.
func TestEventsOfSeveralProjectsAreHandedOnWhereverAProcessDies(t *testing.T) {
	events := []string{"end app 12 failed", "end lib 30 success", "note app n2", "note app n4", "note lib m2", "note lib m3"}
	for die := 1; die <= len(events); die++ {
		t.Run(fmt.Sprintf("dies at hand over %d", die), func(t *testing.T) {
			f := newNotesFixture(t)
			h := &handover{f: f}
			app, lib := f.watch("app", "feature", "a1"), f.watch("lib", "main", "c1")
			f.stands("a1", 4, "12", Running)
			f.stands("c1", 9, "30", Running)
			f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1))}})
			f.stub.set(9, Review{General: []Thread{general("g1", note("m1", "alice", 1))}})
			h.process(0)
			f.round(app, lib)
			h.expect()

			f.stands("a1", 4, "12", Failed)
			f.stands("c1", 9, "30", Success)
			f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2), note("n3", "me", 3), note("n4", "carol", 4))}})
			f.stub.set(9, Review{General: []Thread{general("g1", note("m1", "alice", 1), note("m2", "bob", 2), note("m3", "carol", 3))}})
			h.process(die)
			f.round(app, lib)
			first := h.heard()
			if len(first) != die {
				t.Fatalf("the process was to die at hand over %d and was handed %q", die, first)
			}
			h.process(0)
			f.round(app, lib)
			second := h.heard()
			for _, name := range events {
				if !slices.Contains(first, name) && !slices.Contains(second, name) {
					t.Fatalf("%q was never handed on: %q, then %q", name, first, second)
				}
			}
			if all := slices.Compact(slices.Sorted(slices.Values(slices.Concat(first, second)))); !slices.Equal(all, events) {
				t.Fatalf("handed on %q, want nothing but %q", all, events)
			}
			again := 0
			for _, name := range second {
				if slices.Contains(first, name) {
					again++
				}
			}
			if again == 0 {
				t.Fatalf("the hand over the process died at was not handed on again: %q, then %q", first, second)
			}
			h.process(0)
			f.round(app, lib)
			h.expect()
			if !h.ended("app", "12", Failed) || !h.ended("lib", "30", Success) {
				t.Fatal("the records do not hold both ends")
			}
			if told := slices.Sorted(slices.Values(h.told)); len(slices.Compact(slices.Clone(told))) != len(told) || len(told) == 0 {
				t.Fatalf("an end was told twice, or none at all: %q", told)
			}
		})
	}
}

// A working copy that changes its branch and comes back: a pipeline's end
// that was handed on is not handed on again, the branch it comes back to is
// read like one never seen, and a note the process died at before the change
// is handed on again once the branch is back, with the notes written on its
// change request meanwhile.
func TestABranchSwitchHandsNothingTwiceAndLosesNoNote(t *testing.T) {
	f := newNotesFixture(t)
	h := &handover{f: f}
	feature, main := f.watch("app", "feature", "a1"), f.watch("app", "main", "b1")
	f.stands("a1", 4, "12", Running)
	f.stands("b1", 5, "7", Success)
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1))}})
	f.stub.set(5, Review{General: []Thread{general("g1", note("m1", "alice", 1))}})
	h.process(0)
	f.round(feature)
	f.stands("a1", 4, "12", Failed)
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2))}})
	h.process(2)
	f.round(feature)
	h.expect("end app 12 failed", "note app n2")

	h.process(0)
	f.round(main)
	h.expect()
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2), note("n3", "carol", 3))}})
	f.stub.set(5, Review{General: []Thread{general("g1", note("m1", "alice", 1), note("m2", "bob", 2))}})
	f.pass(pollSlow)
	f.round(main)
	h.expect("note app m2")

	f.round(feature)
	h.expect("note app n2", "note app n3")
	h.process(0)
	f.round(feature)
	f.round(main)
	f.round(feature)
	h.expect()
	if !slices.Equal(h.told, []string{"end app 12 failed"}) {
		t.Fatalf("the news heard %q", h.told)
	}
}

// A forge that fails or limits its requests after a process died in a hand
// over moves no record and loses nothing: the first read that goes through
// hands on what the process died at and what happened meanwhile, once.
func TestAFailingForgeLosesNothingAfterAProcessDied(t *testing.T) {
	for name, failure := range map[string]error{"fails": errors.New("The forge is down."), "limits its requests": &RateLimitError{Wait: 40 * time.Second}} {
		t.Run(name, func(t *testing.T) {
			f := newNotesFixture(t)
			h := &handover{f: f}
			w := f.watch("app", "feature", "a1")
			f.stands("a1", 4, "12", Running)
			f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1))}})
			h.process(0)
			f.round(w)
			f.stands("a1", 4, "12", Canceled)
			f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2))}})
			h.process(1)
			f.round(w)
			h.expect("end app 12 canceled")

			h.process(0)
			f.stub.failWith(failure)
			for range 3 {
				f.round(w)
				f.pass(pollSlow)
			}
			h.expect()
			if h.ended("app", "12", Canceled) || h.noted("app", 4, "n2") {
				t.Fatal("a read that failed moved a record")
			}
			f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2), note("n3", "carol", 3))}})
			f.stub.failWith(nil)
			f.round(w)
			h.expect("end app 12 canceled", "note app n2", "note app n3")
			for range 2 {
				f.pass(pollSlow)
				f.round(w)
			}
			h.process(0)
			f.round(w)
			h.expect()
		})
	}
}
