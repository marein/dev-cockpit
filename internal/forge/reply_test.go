package forge

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// threadStub reads status like pollStub and review threads like reviewStub,
// and answers and resolves threads, refusing a write it is told to fail.
type threadStub struct {
	*reviewStub
	mu          sync.Mutex
	wrote       []string
	failReply   error
	failResolve error
}

func (s *threadStub) Reply(_ context.Context, _ *Client, b Binding, token string, ch Change, t Thread, body string) (Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failReply != nil {
		return Note{}, s.failReply
	}
	s.wrote = append(s.wrote, "reply "+b.Path+" !"+strconv.Itoa(ch.Number)+" "+t.ID+" "+token+": "+body)
	return Note{ID: "n9", Author: "root", Body: body, URL: ch.URL + "#note_n9"}, nil
}

func (s *threadStub) Resolve(_ context.Context, _ *Client, b Binding, token string, ch Change, t Thread) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failResolve != nil {
		return s.failResolve
	}
	s.wrote = append(s.wrote, "resolve "+b.Path+" !"+strconv.Itoa(ch.Number)+" "+t.ID+" "+token)
	return nil
}

func (s *threadStub) written() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.wrote)
}

type threadFixture struct {
	*reviewFixture
	stub *threadStub
}

func newThreadFixture(t *testing.T) *threadFixture {
	review := &reviewStub{pollStub: &pollStub{answers: map[string]BranchStatus{}}}
	stub := &threadStub{reviewStub: review}
	f := &threadFixture{reviewFixture: &reviewFixture{pollFixture: newPollFixtureOf(t, stub, review.pollStub), stub: review}, stub: stub}
	f.s.OnReview(func(r ReviewRead) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.got = append(f.got, r)
	}, func(map[string]bool) {})
	review.answers["a1"] = BranchStatus{Change: &Change{Number: 4, State: Open, URL: "https://forge.example.com/group/app/-/merge_requests/4", Head: "a1"}}
	review.answers["m1"] = BranchStatus{Change: &Change{Number: 3, State: Merged, URL: "https://forge.example.com/group/app/-/merge_requests/3", Head: "m1"}}
	review.set(Review{
		Diff: []Thread{
			{ID: "t1", Path: "src/login.go", Line: 4, Resolvable: true, Notes: []Note{{ID: "1", Author: "alice", Body: "New name?"}}},
			{ID: "t2", Path: "src/login.go", Line: 9, Resolvable: true, Resolved: true, Notes: []Note{{ID: "2"}}},
			{ID: "t3", Path: "src/login.go", Line: 12, Notes: []Note{{ID: "3"}}},
		},
		General: []Thread{{ID: "g1", Notes: []Note{{ID: "4"}}}},
	}, nil)
	return f
}

// A write reads the branch and its review threads afresh, finds the thread
// on the diff by its id, answers it and then resolves it, and answers the
// thread resolved with the note it posted; the change request may be merged.
func TestAWriteOnAThreadAnswersAndResolvesItAfterAFreshRead(t *testing.T) {
	f := newThreadFixture(t)
	w := f.watch("app", "feature", "a1")
	write := ThreadWrite{Thread: "t1", Body: "fixed in abc123", Resolve: true}
	target, err := f.s.ThreadTarget(context.Background(), w, write)
	if err != nil || target.Change.Number != 4 || target.Thread.ID != "t1" || len(f.stub.written()) != 0 {
		t.Fatalf("the target reads %+v, %v, wrote %q", target, err, f.stub.written())
	}
	reads, threadReads := f.reads(), f.stub.threadReads()
	done, note, err := f.s.WriteThread(context.Background(), w, write)
	if err != nil || note == nil || note.URL != "https://forge.example.com/group/app/-/merge_requests/4#note_n9" || !done.Thread.Resolved || done.Thread.Path != "src/login.go" {
		t.Fatalf("the write answered %+v, %+v, %v", done, note, err)
	}
	want := []string{"reply group/app !4 t1 " + pollToken + ": fixed in abc123", "resolve group/app !4 t1 " + pollToken}
	if !slices.Equal(f.stub.written(), want) || f.reads() != reads+1 || f.stub.threadReads() != threadReads+1 {
		t.Fatalf("the forge was asked %q after %d reads and %d thread reads", f.stub.written(), f.reads()-reads, f.stub.threadReads()-threadReads)
	}
	merged := f.watch("app", "feature", "m1")
	if _, note, err := f.s.WriteThread(context.Background(), merged, ThreadWrite{Thread: "t1", Resolve: true}); err != nil || note != nil {
		t.Fatalf("a resolve on a merged change request answered %+v, %v", note, err)
	}
}

// Nothing is written where the branch has no change request, the thread is
// not on its diff, is a general one, cannot be resolved, or stands resolved
// with nothing to answer; a reply on a resolved thread is posted and asks no
// resolve.
func TestAWriteOnAThreadIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	f := newThreadFixture(t)
	w := f.watch("app", "feature", "a1")
	for _, c := range []struct {
		head  string
		write ThreadWrite
		want  string
	}{
		{"c1", ThreadWrite{Thread: "t1", Body: "x"}, `"feature" has no change.`},
		{"a1", ThreadWrite{Thread: "t9", Body: "x"}, `The change !4 has no such review thread on its diff.`},
		{"a1", ThreadWrite{Thread: "g1", Body: "x"}, `The change !4 holds the thread as a general one, only a thread on its diff is answered or resolved here.`},
		{"a1", ThreadWrite{Thread: "t3", Body: "x", Resolve: true}, `Stub does not resolve this thread.`},
		{"a1", ThreadWrite{Thread: "t2", Resolve: true}, `The thread is resolved already.`},
	} {
		_, note, err := f.s.WriteThread(context.Background(), f.watch("app", "feature", c.head), c.write)
		var refused *ThreadRefusal
		if !errors.As(err, &refused) || err.Error() != c.want || note != nil {
			t.Errorf("at %q %+v answered %+v, %v", c.head, c.write, note, err)
		}
	}
	if len(f.stub.written()) != 0 {
		t.Fatalf("a refused write asked %q", f.stub.written())
	}
	f.stub.set(Review{Diff: []Thread{{ID: "t1"}}, Partial: true}, nil)
	if _, _, err := f.s.WriteThread(context.Background(), w, ThreadWrite{Thread: "t9", Body: "x"}); err == nil || err.Error() != `The change !4 has no such review thread among the first ones Stub lists of it, one read takes no more.` {
		t.Fatalf("a thread past a partial read answered %v", err)
	}
	f.stub.set(Review{Diff: []Thread{{ID: "t2", Resolvable: true, Resolved: true}}}, nil)
	done, note, err := f.s.WriteThread(context.Background(), w, ThreadWrite{Thread: "t2", Body: "late", Resolve: true})
	if err != nil || note == nil || !done.Thread.Resolved || !slices.Equal(f.stub.written(), []string{"reply group/app !4 t2 " + pollToken + ": late"}) {
		t.Fatalf("a reply on a resolved thread answered %+v, %v, asked %q", note, err, f.stub.written())
	}
}

// A resolve the forge refuses after the reply went says the reply stands,
// and a refused reply resolves nothing.
func TestAResolveRefusedAfterTheReplySaysTheReplyWent(t *testing.T) {
	f := newThreadFixture(t)
	w := f.watch("app", "feature", "a1")
	f.stub.failResolve = errors.New("The forge refused it.")
	_, note, err := f.s.WriteThread(context.Background(), w, ThreadWrite{Thread: "t1", Body: "fixed", Resolve: true})
	if err == nil || err.Error() != "The reply is posted, the thread is not resolved. The forge refused it." || note == nil || note.Body != "fixed" {
		t.Fatalf("a refused resolve answered %+v, %v", note, err)
	}
	f.stub.failReply, f.stub.failResolve = errors.New("No reply."), nil
	before := len(f.stub.written())
	if _, note, err := f.s.WriteThread(context.Background(), w, ThreadWrite{Thread: "t1", Body: "again", Resolve: true}); err == nil || err.Error() != "No reply." || note != nil || len(f.stub.written()) != before {
		t.Fatalf("a refused reply answered %+v, %v, asked %q", note, err, f.stub.written())
	}
}

// A write has the poller read the branch and its review threads at once, so
// the line comments follow it, though the threads were read a moment ago.
func TestAWriteOnAThreadHasTheThreadsReadAgain(t *testing.T) {
	f := newThreadFixture(t)
	w := f.watch("app", "feature", "a1")
	f.round(w)
	if got := f.heard(); len(got) != 1 {
		t.Fatalf("the first read told %+v", got)
	}
	if _, _, err := f.s.WriteThread(context.Background(), w, ThreadWrite{Thread: "t1", Resolve: true}); err != nil {
		t.Fatal(err)
	}
	f.stub.set(Review{Diff: []Thread{{ID: "t1", Path: "src/login.go", Line: 4, Resolvable: true, Resolved: true, Notes: []Note{{ID: "1"}}}}}, nil)
	threadReads := f.stub.threadReads()
	f.round(w)
	got := f.heard()
	if f.stub.threadReads() != threadReads+1 || len(got) != 1 || !got[0].Review.Diff[0].Resolved {
		t.Fatalf("the read after the write told %+v after %d thread reads", got, f.stub.threadReads()-threadReads)
	}
}

// A write on a thread the forge no longer has on the diff, deleted there
// since the poller read it, has the poller read the threads at once, so the
// line comments let it go.
func TestAWriteOnAThreadGoneHasTheThreadsReadAgain(t *testing.T) {
	f := newThreadFixture(t)
	w := f.watch("app", "feature", "a1")
	f.round(w)
	f.heard()
	f.stub.set(Review{}, nil)
	var refused *ThreadRefusal
	if _, _, err := f.s.WriteThread(context.Background(), w, ThreadWrite{Thread: "t1", Body: "x"}); !errors.As(err, &refused) {
		t.Fatalf("a write on a thread gone answered %v", err)
	}
	threadReads := f.stub.threadReads()
	f.round(w)
	if got := f.heard(); f.stub.threadReads() != threadReads+1 || len(got) != 1 || len(got[0].Review.Diff) != 0 {
		t.Fatalf("the read after the refusal told %+v after %d thread reads", got, f.stub.threadReads()-threadReads)
	}
}

// A provider that cannot answer or resolve says so, a host without a token
// too, the id of a thread is gated, and none of them reads the forge.
func TestAWriteOnAThreadNeedsAProviderThatWritesAndAToken(t *testing.T) {
	f := newPollFixture(t)
	w := f.watch("app", "feature", "a1")
	if replies, resolves := f.s.AnswersThreads(w.Binding); replies || resolves {
		t.Fatal("a provider without the capabilities is said to write threads")
	}
	if _, err := f.s.ThreadTarget(context.Background(), w, ThreadWrite{Thread: "t1", Body: "x"}); !errors.Is(err, ErrNoReply) {
		t.Fatalf("a provider that cannot answer answered %v", err)
	}
	if _, err := f.s.ThreadTarget(context.Background(), w, ThreadWrite{Thread: "t1", Resolve: true}); !errors.Is(err, ErrNoResolve) {
		t.Fatalf("a provider that cannot resolve answered %v", err)
	}
	p := newThreadFixture(t)
	if replies, resolves := p.s.AnswersThreads(w.Binding); !replies || !resolves {
		t.Fatal("a provider that writes threads is said not to")
	}
	if err := p.s.ChangeToken(p.host.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.s.WriteThread(context.Background(), p.watch("app", "feature", "a1"), ThreadWrite{Thread: "t1", Body: "x"}); !errors.Is(err, ErrNoToken) || p.reads() != 0 {
		t.Fatalf("a host without a token answered %v after %d reads", err, p.reads())
	}
	for id, want := range map[string]string{
		"":                               "Name the review thread by its id.",
		strings.Repeat("a", 256):         "The id of a review thread is text of at most 255 characters.",
		"\xff":                           "The id of a review thread is text of at most 255 characters.",
		"PRRT_a b":                       "The id of a review thread holds no spaces, no control characters and no invisible format characters.",
		"PRRT_a\u202e":                   "The id of a review thread holds no spaces, no control characters and no invisible format characters.",
		"d1\u0085":                       "The id of a review thread holds no spaces, no control characters and no invisible format characters.",
		"f7a73e78f83c1f1a6906214815a12e": "",
		"PRRT_kwDOAbc1-_=":               "",
	} {
		if got := ThreadIDRefusal(id); got != want {
			t.Errorf("%q is refused with %q, want %q", id, got, want)
		}
	}
}
