package forge

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"testing"
	"time"
)

// noteStub reads status like pollStub, the review threads of each change
// request as reviews holds them, and answers who the token belongs to as
// account says, counting how often it was asked.
type noteStub struct {
	*pollStub
	nmu     sync.Mutex
	reviews map[int]Review
	account string
	failed  error
	tests   int
}

func (n *noteStub) Threads(_ context.Context, _ *Client, _ Binding, _ string, number int) (Review, error) {
	n.nmu.Lock()
	defer n.nmu.Unlock()
	return n.reviews[number], nil
}

func (n *noteStub) Test(context.Context, *Client, Host, string) (Connection, error) {
	n.nmu.Lock()
	defer n.nmu.Unlock()
	n.tests++
	if n.failed != nil {
		return Connection{}, n.failed
	}
	return Connection{Account: n.account}, nil
}

func (n *noteStub) set(change int, review Review) {
	n.nmu.Lock()
	defer n.nmu.Unlock()
	n.reviews[change] = review
}

func (n *noteStub) asks() int {
	n.nmu.Lock()
	defer n.nmu.Unlock()
	return n.tests
}

type notesFixture struct {
	*pollFixture
	stub  *noteStub
	nmu   sync.Mutex
	notes []ReviewNote
}

func newNotesFixture(t *testing.T) *notesFixture {
	stub := &noteStub{pollStub: &pollStub{answers: map[string]BranchStatus{}}, reviews: map[int]Review{}, account: "Me"}
	f := &notesFixture{pollFixture: newPollFixtureOf(t, stub, stub.pollStub), stub: stub}
	f.hear()
	return f
}

func (f *notesFixture) hear() {
	f.s.OnReviewNote(func(n ReviewNote) {
		f.nmu.Lock()
		defer f.nmu.Unlock()
		f.notes = append(f.notes, n)
	})
}

// restart is a new process on the same state directory, which knows nothing
// of what the one before it polled nor whose its token is.
func (f *notesFixture) restart() {
	f.s = New(f.dir, []Provider{f.stub}, f.host)
	f.s.poll.now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.now
	}
	f.hear()
}

// change has the branch at head stand in the change request with that
// number.
func (f *notesFixture) change(head string, number int) {
	f.stub.mu.Lock()
	defer f.stub.mu.Unlock()
	f.stub.answers[head] = BranchStatus{Change: &Change{Number: number, State: Open}, Pipeline: &Pipeline{ID: "12", Status: Running}}
}

// heard answers the ids of the notes heard of since the last call.
func (f *notesFixture) heard() []string {
	f.nmu.Lock()
	defer f.nmu.Unlock()
	var ids []string
	for _, n := range f.notes {
		ids = append(ids, n.Note.ID)
	}
	f.notes = nil
	return ids
}

func (f *notesFixture) expect(want ...string) {
	f.t.Helper()
	if got := f.heard(); !slices.Equal(got, want) {
		f.t.Fatalf("heard of the notes %q, want %q", got, want)
	}
}

// again lets the threads of the branches fall due and reads ws.
func (f *notesFixture) again(ws ...Watch) {
	f.pass(pollReview)
	f.round(ws...)
}

var noteTime = time.Unix(1_790_000_000, 0)

func note(id, author string, minute int) Note {
	return Note{ID: id, Author: author, Body: "on " + id, Created: noteTime.Add(time.Duration(minute) * time.Minute)}
}

func onLine(id string, notes ...Note) Thread {
	return Thread{ID: id, Path: "a.go", Line: 3, Side: NewSide, Resolvable: true, Notes: notes}
}

func general(id string, notes ...Note) Thread {
	return Thread{ID: id, Notes: notes}
}

// The notes that stand when the change request's threads are first read
// are nobody's news. A note written after is heard of once, in the order
// the notes were written, with its thread, its change request and its
// branch, and one of the token's own account is not heard of at all, in
// whatever case the forge writes the name.
func TestANewNoteIsHeardOfOnce(t *testing.T) {
	f := newNotesFixture(t)
	w := f.watch("app", "feature", "a1")
	f.change("a1", 4)
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1))}})
	f.round(w)
	f.expect()
	f.stub.set(4, Review{
		Diff:    []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 5), note("n3", "me", 6))},
		General: []Thread{general("g1", note("n4", "carol", 4))},
	})
	f.again(w)
	f.nmu.Lock()
	got := slices.Clone(f.notes)
	f.nmu.Unlock()
	f.expect("n4", "n2")
	if n := got[1]; n.Watch.Project != "app" || n.Watch.Branch != "feature" || n.Change.Number != 4 || n.Thread.ID != "t1" || len(n.Thread.Notes) != 3 || n.Note.Author != "bob" {
		t.Fatalf("the reply was heard of as %+v", n)
	}
	f.again(w)
	f.expect()
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n3", "me", 6))}})
	f.again(w)
	f.expect()
}

// A note written while the server was down is heard of by the next process,
// once, and the process after that hears of it no more.
func TestANewNoteSurvivesARestart(t *testing.T) {
	f := newNotesFixture(t)
	w := f.watch("app", "feature", "a1")
	f.change("a1", 4)
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1))}})
	f.round(w)
	f.restart()
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2))}})
	f.round(w)
	f.expect("n2")
	f.restart()
	f.round(w)
	f.expect()
}

// Each change request keeps its own notes: one read again after another
// branch's was hears of the notes written on it meanwhile, and the other
// one's first read hears of none.
func TestAChangeRequestKeepsItsNotesWhileAnotherIsRead(t *testing.T) {
	f := newNotesFixture(t)
	feature, other := f.watch("app", "feature", "a1"), f.watch("app", "other", "b1")
	f.change("a1", 4)
	f.change("b1", 5)
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1))}})
	f.stub.set(5, Review{General: []Thread{general("g1", note("m1", "alice", 1))}})
	f.round(feature)
	f.round(other)
	f.expect()
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2))}})
	f.round(feature)
	f.expect("n2")
}

// A read that holds the first of more notes than it takes forgets none it
// did not reach: they are not heard of again when a later read reaches them.
func TestAPartialReadForgetsNoNote(t *testing.T) {
	f := newNotesFixture(t)
	w := f.watch("app", "feature", "a1")
	f.change("a1", 4)
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1)), onLine("t2", note("n2", "bob", 2))}})
	f.round(w)
	f.stub.set(4, Review{Diff: []Thread{onLine("t2", note("n2", "bob", 2))}, Partial: true})
	f.again(w)
	f.expect()
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1)), onLine("t2", note("n2", "bob", 2), note("n3", "carol", 3))}})
	f.again(w)
	f.expect("n3")
}

// Who the token belongs to is asked once per turn of its host, and the
// notes wait while it cannot be told: nothing is kept of a read that could
// not tell, so the first read that can is the first one. A new token is a
// new turn and asks again.
func TestNewNotesWaitForWhoTheTokenBelongsTo(t *testing.T) {
	f := newNotesFixture(t)
	w := f.watch("app", "feature", "a1")
	f.change("a1", 4)
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1))}})
	f.stub.failed = errors.New("The token was refused.")
	f.round(w)
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2))}})
	f.stub.nmu.Lock()
	f.stub.failed = nil
	f.stub.nmu.Unlock()
	f.again(w)
	f.expect()
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2), note("n3", "me", 3), note("n4", "carol", 4))}})
	f.again(w)
	f.expect("n4")
	if asks := f.stub.asks(); asks != 2 {
		t.Fatalf("who the token belongs to was asked %d times, want 2", asks)
	}
	if err := f.s.ChangeToken(f.host.ID, pollToken+"x", false); err != nil {
		t.Fatal(err)
	}
	f.again(w)
	if asks := f.stub.asks(); asks != 3 {
		t.Fatalf("a new token asked who it belongs to %d times in all, want 3", asks)
	}
}

// A project that is gone takes its notes along: one made again under its
// name reads its change requests for the first time.
func TestAForgottenProjectReadsItsChangeRequestsAfresh(t *testing.T) {
	f := newNotesFixture(t)
	w := f.watch("app", "feature", "a1")
	f.change("a1", 4)
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1))}})
	f.round(w)
	f.s.Forget("app")
	f.round()
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2))}})
	f.round(w)
	f.expect()
}

// A record it cannot read is no record of nothing: nothing is heard of and
// nothing written over it. A change request still read keeps its record
// past notesKept, one nobody read for that long is forgotten.
func TestReviewNotesPublishNothingFromAFileTheyCannotRead(t *testing.T) {
	f := newNotesFixture(t)
	w := f.watch("app", "feature", "a1")
	f.change("a1", 4)
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1))}})
	f.round(w)
	saved, err := os.ReadFile(f.s.notesPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.s.notesPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.s.notesPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2))}})
	f.again(w)
	f.expect()
	if err := os.Remove(f.s.notesPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.s.notesPath(), saved, 0o600); err != nil {
		t.Fatal(err)
	}
	f.again(w)
	f.expect("n2")

	f.pass(notesKept/2 + time.Hour)
	f.round(w)
	f.pass(notesKept/2 + time.Hour)
	lib := f.watch("lib", "main", "c1")
	f.change("c1", 9)
	f.round(lib)
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2), note("n3", "carol", 3))}})
	f.round(w)
	f.expect("n3")

	f.pass(notesKept + time.Hour)
	f.round(lib)
	f.stub.set(4, Review{Diff: []Thread{onLine("t1", note("n1", "alice", 1), note("n2", "bob", 2), note("n3", "carol", 3), note("n4", "dave", 4))}})
	f.round(w)
	f.expect()
}
