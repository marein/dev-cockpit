package forge

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"
)

// reviewStub reads status like pollStub and review threads as review says,
// counting the change requests it was asked for.
type reviewStub struct {
	*pollStub
	mu     sync.Mutex
	review Review
	fail   error
	asked  []int
}

func (r *reviewStub) Threads(_ context.Context, _ *Client, _ Binding, _ string, number int) (Review, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, number)
	return r.review, r.fail
}

func (r *reviewStub) set(review Review, fail error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.review, r.fail = review, fail
}

func (r *reviewStub) threadReads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.asked)
}

type reviewFixture struct {
	*pollFixture
	stub *reviewStub
	mu   sync.Mutex
	got  []ReviewRead
	kept []map[string]bool
	hold chan struct{}
}

func newReviewFixture(t *testing.T, hears bool) *reviewFixture {
	stub := &reviewStub{pollStub: &pollStub{answers: map[string]BranchStatus{}}}
	f := &reviewFixture{pollFixture: newPollFixtureOf(t, stub, stub.pollStub), stub: stub}
	if hears {
		f.s.OnReview(func(r ReviewRead) {
			f.mu.Lock()
			f.got = append(f.got, r)
			hold := f.hold
			f.mu.Unlock()
			if hold != nil {
				<-hold
			}
		}, func(projects map[string]bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.kept = append(f.kept, projects)
		})
	}
	return f
}

func (f *reviewFixture) heard() []ReviewRead {
	f.mu.Lock()
	defer f.mu.Unlock()
	got := f.got
	f.got = nil
	return got
}

// keeps answers the projects each telling of what the poller keeps named,
// sorted, since the last call.
func (f *reviewFixture) keeps() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var got [][]string
	for _, projects := range f.kept {
		got = append(got, slices.Sorted(maps.Keys(projects)))
	}
	f.kept = nil
	return got
}

func noted(body string) Review {
	return Review{Diff: []Thread{{ID: "t1", Path: "a.go", Line: 3, Side: NewSide, Commit: "c1", Notes: []Note{{ID: "n1", Author: "root", Body: body}}}}}
}

// Nobody hearing of review threads, the poller reads none. Once somebody
// does, a read of a branch with a change request reads its threads too and
// tells them on the first read; within pollReview it asks no more, after it
// asks again and tells only what changed; a moved head reads them at once
// and tells them again, and a change request gone tells none, once.
func TestThePollerReadsTheReviewThreadsOfABranchsChangeRequest(t *testing.T) {
	deaf := newReviewFixture(t, false)
	deaf.stub.answer("a1", Running)
	deaf.round(deaf.watch("app", "feature", "a1"))
	if deaf.stub.threadReads() != 0 {
		t.Fatal("the threads were read while nobody hears of them")
	}

	f := newReviewFixture(t, true)
	f.stub.answer("a1", Running)
	f.stub.set(noted("New name?"), nil)
	w := f.watch("app", "feature", "a1")
	f.round(w)
	got := f.heard()
	if len(got) != 1 || !got[0].Watch.same(w) || got[0].Change.Number != 4 || got[0].Review.Diff[0].Notes[0].Body != "New name?" {
		t.Fatalf("the first read told %+v", got)
	}
	if !slices.Equal(f.stub.asked, []int{4}) {
		t.Fatalf("the threads of %v were asked for", f.stub.asked)
	}
	f.pass(pollFast)
	f.round(w)
	if f.reads() != 2 || f.stub.threadReads() != 1 || len(f.heard()) != 0 {
		t.Fatalf("within pollReview: %d status reads, %d thread reads", f.reads(), f.stub.threadReads())
	}
	f.pass(pollReview)
	f.round(w)
	if f.stub.threadReads() != 2 || len(f.heard()) != 0 {
		t.Fatalf("after pollReview the same threads: %d reads, told again", f.stub.threadReads())
	}
	f.stub.set(noted("New name, please"), nil)
	f.pass(pollReview)
	f.round(w)
	if got := f.heard(); len(got) != 1 || got[0].Review.Diff[0].Notes[0].Body != "New name, please" {
		t.Fatalf("a changed note told %+v", got)
	}
	f.stub.answer("a2", Running)
	moved := f.watch("app", "feature", "a2")
	f.round(moved)
	if got := f.heard(); f.stub.threadReads() != 4 || len(got) != 1 || !got[0].Watch.same(moved) {
		t.Fatalf("a moved head: %d thread reads, told %+v", f.stub.threadReads(), got)
	}
	f.stub.mu.Lock()
	f.stub.answers["a2"] = BranchStatus{Pipeline: &Pipeline{ID: "12", Status: Running}}
	f.stub.mu.Unlock()
	f.pass(pollFast)
	f.round(moved)
	if got := f.heard(); len(got) != 1 || got[0].Change != nil || len(got[0].Review.Diff) != 0 {
		t.Fatalf("a change request gone told %+v", got)
	}
	f.pass(pollFast)
	f.round(moved)
	if got := f.heard(); len(got) != 0 || f.stub.threadReads() != 4 {
		t.Fatalf("still without a change request: told %+v, %d thread reads", got, f.stub.threadReads())
	}
}

// A failed read of the threads tells nothing and is asked again after
// pollReview, not with every read of the status; a read its host's change
// cut off tells nothing either.
func TestAFailedReadOfTheThreadsTellsNothing(t *testing.T) {
	f := newReviewFixture(t, true)
	f.stub.answer("a1", Running)
	f.stub.set(Review{}, errors.New("The token may not read the review threads."))
	w := f.watch("app", "feature", "a1")
	f.round(w)
	f.pass(pollFast)
	f.round(w)
	if got := f.heard(); len(got) != 0 || f.stub.threadReads() != 1 {
		t.Fatalf("a failed read told %+v, %d thread reads", got, f.stub.threadReads())
	}
	f.stub.set(noted("Now readable"), nil)
	f.pass(pollReview)
	f.round(w)
	if got := f.heard(); len(got) != 1 || f.stub.threadReads() != 2 {
		t.Fatalf("after pollReview: told %+v, %d thread reads", got, f.stub.threadReads())
	}

	g := newReviewFixture(t, true)
	g.stub.answer("a1", Running)
	g.stub.set(noted("Cut off"), nil)
	g.stub.gate = make(chan struct{})
	g.start(g.watch("app", "feature", "a1"))
	for g.reads() == 0 {
		time.Sleep(time.Millisecond)
	}
	g.s.poll.hostChanged(g.host.ID)
	close(g.stub.gate)
	g.s.poll.reads.Wait()
	if got := g.heard(); len(got) != 0 {
		t.Fatalf("a read of a host that changed meanwhile told %+v", got)
	}
}

// Whoever hears of review threads hears which projects the poller keeps after
// its first round, and again only once they changed: a project whose
// watches went, whose host lost its token or that no host with a token is
// left for. A project whose branch could not be told stays kept unread, and
// a failed list of watches tells nothing.
func TestThePollerTellsWhichProjectsItKeeps(t *testing.T) {
	deaf := newReviewFixture(t, false)
	deaf.round(deaf.watch("app", "feature", "a1"))
	if deaf.s.poll.kept != nil {
		t.Fatal("the poller kept a list while nobody hears of review threads")
	}

	f := newReviewFixture(t, true)
	f.stub.answer("a1", Running)
	app, shop := f.watch("app", "feature", "a1"), f.watch("shop", "main", "a1")
	f.round(app, shop)
	if got := f.keeps(); len(got) != 1 || !slices.Equal(got[0], []string{"app", "shop"}) {
		t.Fatalf("the first round told %v", got)
	}
	f.pass(pollFast)
	f.round(app, shop)
	if got := f.keeps(); len(got) != 0 {
		t.Fatalf("the same projects were told again: %v", got)
	}
	unknown := Watch{Project: "shop", Binding: shop.Binding}
	f.pass(pollFast)
	reads := f.reads()
	f.round(app, unknown)
	if got := f.keeps(); len(got) != 0 || f.reads() != reads+1 {
		t.Fatalf("a branch that could not be told: told %v, %d reads", got, f.reads()-reads)
	}
	f.s.pollRound(context.Background(), func(context.Context, func(Binding) bool) ([]Watch, error) {
		return nil, errors.New("the bindings could not be read")
	})
	f.s.poll.reads.Wait()
	if got := f.keeps(); len(got) != 0 {
		t.Fatalf("a failed list of watches told %v", got)
	}
	f.round(app)
	if got := f.keeps(); len(got) != 1 || !slices.Equal(got[0], []string{"app"}) {
		t.Fatalf("a project whose watch went: told %v", got)
	}
	if err := f.s.ChangeToken(f.host.ID, "", true); err != nil {
		t.Fatal(err)
	}
	f.round(app)
	if got := f.keeps(); len(got) != 1 || len(got[0]) != 0 {
		t.Fatalf("without any token: told %v", got)
	}
}

// A read of review threads told while an earlier one is still being heard,
// whose project then leaves the poller, is not heard after it left, nor is a
// word of what the poller keeps that a later one replaced: the last word
// heard is what the poller holds.
func TestAWordTheProjectLeftMeanwhileIsNotHeard(t *testing.T) {
	f := newReviewFixture(t, true)
	f.stub.answer("a1", Running)
	f.stub.set(noted("New name?"), nil)
	f.mu.Lock()
	hold := make(chan struct{})
	f.hold = hold
	f.mu.Unlock()
	app := f.watch("app", "feature", "a1")
	f.start(app)
	waitUntil(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.got) == 1
	})
	f.stub.set(noted("New name, please"), nil)
	f.pass(pollReview)
	f.start(app)
	waitUntil(t, func() bool {
		f.s.poll.mu.Lock()
		defer f.s.poll.mu.Unlock()
		k := f.s.poll.watched["app"]
		return k != nil && k.review.published != nil && k.review.published.Review.Diff[0].Notes[0].Body == "New name, please"
	})
	f.start()
	close(hold)
	f.s.poll.reads.Wait()
	if got := f.heard(); len(got) != 1 || got[0].Review.Diff[0].Notes[0].Body != "New name?" {
		t.Fatalf("heard %+v", got)
	}
	if got := f.keeps(); len(got) == 0 || len(got[len(got)-1]) != 0 {
		t.Fatalf("the poller told it keeps %v", got)
	}
}

func waitUntil(t *testing.T, done func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !done(); time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("timed out")
		}
	}
}
