package forge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

// pollStub reads a branch the way a provider would, answering by head, and
// counts what it was asked and how many reads ran at once. A gate holds
// every read until it closes or the read ends.
type pollStub struct {
	mu      sync.Mutex
	answers map[string]BranchStatus
	fail    error
	reads   []string
	running int
	most    int
	gate    chan struct{}
}

func (p *pollStub) Kind() Kind {
	return Kind{ID: "stub", Label: "Stub", ChangeNoun: "change", ChangeMark: "!"}
}

func (p *pollStub) Test(context.Context, *Client, Host, string) (Connection, error) {
	return Connection{}, nil
}

func (p *pollStub) BranchStatus(ctx context.Context, _ *Client, b Binding, token, branch, head string) (BranchStatus, error) {
	p.mu.Lock()
	p.reads = append(p.reads, fmt.Sprintf("%s %s@%s %s", b.Path, branch, head, token))
	p.running++
	p.most = max(p.most, p.running)
	gate, fail, answer := p.gate, p.fail, p.answers[head]
	p.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			fail = ctx.Err()
		}
	}
	p.mu.Lock()
	p.running--
	p.mu.Unlock()
	return answer, fail
}

func (p *pollStub) asked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.reads)
}

func (p *pollStub) answer(head string, status Status) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answers[head] = BranchStatus{Change: &Change{Number: 4, State: Open}, Pipeline: &Pipeline{ID: "12", Status: status}}
}

func (p *pollStub) failWith(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail = err
}

type pollFixture struct {
	t       *testing.T
	dir     string
	s       *Service
	stub    *pollStub
	host    Host
	now     time.Time
	changed []string
	mu      sync.Mutex
}

const pollToken = "glpat-poll-0123456789abcdef"

func newPollFixture(t *testing.T) *pollFixture {
	stub := &pollStub{answers: map[string]BranchStatus{}}
	return newPollFixtureOf(t, stub, stub)
}

// newPollFixtureOf is the fixture of a provider that reads status with stub.
func newPollFixtureOf(t *testing.T, provider Provider, stub *pollStub) *pollFixture {
	host := BuiltIn(stub.Kind(), "https://forge.example.com")
	f := &pollFixture{t: t, dir: t.TempDir(), stub: stub, host: host, now: time.Unix(1_800_000_000, 0)}
	f.s = New(f.dir, []Provider{provider}, host)
	f.s.poll.now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.now
	}
	f.s.poll.changed = func(project string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.changed = append(f.changed, project)
	}
	if err := f.s.ChangeToken(host.ID, pollToken, false); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *pollFixture) watch(project, branch, head string) Watch {
	return Watch{Project: project, Binding: Binding{Host: f.host, Path: "group/" + project}, Branch: branch, Remote: branch, Head: head}
}

func (f *pollFixture) pass(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// round looks at the watches once and waits for the reads it started.
func (f *pollFixture) round(ws ...Watch) {
	f.start(ws...)
	f.s.poll.reads.Wait()
}

func (f *pollFixture) start(ws ...Watch) {
	f.s.pollRound(context.Background(), func(_ context.Context, polled func(Binding) bool) ([]Watch, error) {
		return ws, nil
	})
}

func (f *pollFixture) reads() int {
	return len(f.stub.asked())
}

func (f *pollFixture) moves() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	moves := f.changed
	f.changed = nil
	return moves
}

// A branch is read when first seen, then again after pollFast while its
// pipeline runs or waits and after pollSlow once it ended; the project is
// told only when what shows moved.
func TestThePollerReadsARunningPipelineFastAndAnEndedOneSlow(t *testing.T) {
	f := newPollFixture(t)
	w := f.watch("app", "feature", "a1")
	f.stub.answer("a1", Failed)
	f.round(w)
	if got := f.stub.asked(); !slices.Equal(got, []string{"group/app feature@a1 " + pollToken}) {
		t.Fatalf("first seen, the forge was asked %q", got)
	}
	if got := f.moves(); !slices.Equal(got, []string{"app"}) {
		t.Fatalf("the first read told %q", got)
	}
	f.round(w)
	f.pass(pollSlow - time.Second)
	f.round(w)
	if f.reads() != 1 {
		t.Fatalf("an ended pipeline was read again before pollSlow: %d reads", f.reads())
	}
	f.pass(time.Second)
	f.round(w)
	if f.reads() != 2 || len(f.moves()) != 0 {
		t.Fatalf("after pollSlow: %d reads, the same answer told %q", f.reads(), f.moves())
	}
	f.stub.answer("a1", Running)
	f.pass(pollSlow)
	f.round(w)
	if f.reads() != 3 || !slices.Equal(f.moves(), []string{"app"}) {
		t.Fatalf("a retried pipeline: %d reads", f.reads())
	}
	f.pass(pollFast - time.Second)
	f.round(w)
	f.pass(time.Second)
	f.round(w)
	if f.reads() != 4 {
		t.Fatalf("a running pipeline was not read after pollFast: %d reads", f.reads())
	}
	polled, ok := f.s.Polled("app", w.Binding, "feature")
	if !ok || polled.Status.Pipeline.Status != Running || polled.Err != nil || !polled.Read.Equal(f.now) {
		t.Fatalf("the cache holds %+v, %v", polled, ok)
	}
	if _, ok := f.s.Polled("app", w.Binding, "main"); ok {
		t.Fatal("the cache answered for another branch")
	}
	if _, ok := f.s.Polled("app", f.watch("other", "feature", "a1").Binding, "feature"); ok {
		t.Fatal("the cache answered for another binding")
	}
}

// A push or a fetch that moves the head is read at once and fast for
// pollMoved, a checkout of another branch at once but at its own pace.
func TestThePollerReadsAMovedHeadAtOnceAndFastForAWhile(t *testing.T) {
	f := newPollFixture(t)
	f.stub.answer("a1", Success)
	f.stub.answer("a2", Success)
	f.stub.answer("b1", Success)
	f.round(f.watch("app", "feature", "a1"))
	moved := f.watch("app", "feature", "a2")
	f.round(moved)
	if f.reads() != 2 {
		t.Fatalf("a moved head was not read at once: %d reads", f.reads())
	}
	for i := 0; i < int(pollMoved/pollFast); i++ {
		f.pass(pollFast)
		f.round(moved)
	}
	if want := 2 + int(pollMoved/pollFast); f.reads() != want {
		t.Fatalf("after the move: %d reads, want %d", f.reads(), want)
	}
	f.pass(pollFast)
	f.round(moved)
	if want := 2 + int(pollMoved/pollFast); f.reads() != want {
		t.Fatalf("past pollMoved the branch is still read fast: %d reads", f.reads())
	}
	other := f.watch("app", "other", "b1")
	f.round(other)
	reads := f.reads()
	f.pass(pollFast)
	f.round(other)
	if f.reads() != reads || f.stub.asked()[reads-1] != "group/app other@b1 "+pollToken {
		t.Fatalf("a checkout: %q", f.stub.asked())
	}
	f.round(f.watch("app", "other", ""))
	if f.reads() != reads+1 {
		t.Fatal("a branch whose tracking ref went was not read again")
	}
}

// Only a host with a token is polled, without a token on any host the
// watches are not even asked. A branch the watches no longer name, or whose
// host lost its token, goes and its project is told; a failed list of
// watches takes nothing away.
func TestThePollerStopsForAHostWithoutToken(t *testing.T) {
	f := newPollFixture(t)
	f.stub.answer("a1", Running)
	w := f.watch("app", "feature", "a1")
	f.round(w)
	f.moves()
	f.s.pollRound(context.Background(), func(context.Context, func(Binding) bool) ([]Watch, error) {
		return nil, errors.New("the bindings could not be read")
	})
	if _, ok := f.s.Polled("app", w.Binding, "feature"); !ok {
		t.Fatal("a failed list of watches took the status away")
	}
	if err := f.s.ChangeToken(f.host.ID, "", true); err != nil {
		t.Fatal(err)
	}
	other, err := f.s.Add(Input{Kind: "stub", WebURL: "https://other.example.com"})
	if err == nil {
		err = f.s.ChangeToken(other.ID, pollToken, false)
	}
	if err != nil {
		t.Fatal(err)
	}
	var asked []bool
	ask := func(_ context.Context, polled func(Binding) bool) ([]Watch, error) {
		asked = append(asked, polled(w.Binding), polled(Binding{Host: other, Path: "group/app"}))
		return []Watch{w}, nil
	}
	f.s.pollRound(context.Background(), ask)
	f.s.poll.reads.Wait()
	if f.reads() != 1 || !slices.Equal(asked, []bool{false, true}) || !slices.Equal(f.moves(), []string{"app"}) {
		t.Fatalf("without a token: %d reads, polled %v", f.reads(), asked)
	}
	if err := f.s.Delete(other.ID); err != nil {
		t.Fatal(err)
	}
	f.s.pollRound(context.Background(), ask)
	if len(asked) != 2 {
		t.Fatal("without any token the watches were asked")
	}
	if _, ok := f.s.Polled("app", w.Binding, "feature"); ok {
		t.Fatal("the status of a host without token stayed")
	}
	f.pass(pollFast)
	f.round(w)
	if f.reads() != 1 {
		t.Fatalf("a host without token was read: %d reads", f.reads())
	}
	if err := f.s.ChangeToken(f.host.ID, pollToken, false); err != nil {
		t.Fatal(err)
	}
	f.round(w)
	f.round()
	if f.reads() != 2 || !slices.Equal(f.moves(), []string{"app", "app"}) {
		t.Fatalf("a branch no longer named: %d reads", f.reads())
	}
}

// A failed read keeps the answer the branch had and waits twice as long
// each time, up to pollSlow; a limit the server set holds until it lifts.
// A new token reads every branch of its host at once.
func TestThePollerBacksOffAFailingRead(t *testing.T) {
	f := newPollFixture(t)
	f.stub.answer("a1", Running)
	w := f.watch("app", "feature", "a1")
	f.round(w)
	f.moves()
	f.stub.failWith(errors.New("The server did not answer in time."))
	reads := f.reads()
	for _, wait := range []time.Duration{pollFast, pollFast, 2 * pollFast, 4 * pollFast, 8 * pollFast, 16 * pollFast, pollSlow, pollSlow} {
		f.pass(wait - time.Second)
		f.round(w)
		if f.reads() != reads {
			t.Fatalf("read again %v early", time.Second)
		}
		f.pass(time.Second)
		f.round(w)
		if reads++; f.reads() != reads {
			t.Fatalf("not read again after %v", wait)
		}
	}
	polled, ok := f.s.Polled("app", w.Binding, "feature")
	if !ok || polled.Err == nil || polled.Status.Pipeline == nil || polled.Status.Pipeline.Status != Running || !slices.Equal(f.moves(), []string{"app"}) {
		t.Fatalf("the failure shows %+v, %v", polled, ok)
	}
	f.stub.failWith(&RateLimitError{Wait: 20 * time.Minute})
	f.pass(pollSlow)
	f.round(w)
	reads = f.reads()
	f.pass(20*time.Minute - time.Second)
	f.round(w)
	if f.reads() != reads {
		t.Fatal("a limited server was asked before the limit lifted")
	}
	f.pass(time.Second)
	f.round(w)
	if f.reads() != reads+1 {
		t.Fatal("a limited server was not asked after the limit lifted")
	}
	f.stub.failWith(nil)
	if err := f.s.ChangeToken(f.host.ID, pollToken+"2", false); err != nil {
		t.Fatal(err)
	}
	f.round(w)
	if got := f.stub.asked(); got[len(got)-1] != "group/app feature@a1 "+pollToken+"2" {
		t.Fatalf("a new token: %q", got[len(got)-1])
	}
	if polled, _ := f.s.Polled("app", w.Binding, "feature"); polled.Err != nil {
		t.Fatalf("a read that went through kept the failure: %v", polled.Err)
	}
}

// A token changed while a round looks at the branches or while a read is in
// flight is the one the next read takes, at once: the read in flight ends
// and its answer is dropped, the poll before the change answers no status
// read. A token taken away is not sent again.
func TestThePollerReadsWithTheTokenTheHostHasNow(t *testing.T) {
	f := newPollFixture(t)
	f.stub.answer("a1", Success)
	w := f.watch("app", "feature", "a1")
	f.round(w)
	f.moves()
	token := func(token string, remove bool) {
		if err := f.s.ChangeToken(f.host.ID, token, remove); err != nil {
			t.Fatal(err)
		}
	}
	during := func(change func()) {
		f.s.pollRound(context.Background(), func(context.Context, func(Binding) bool) ([]Watch, error) {
			change()
			return []Watch{w}, nil
		})
		f.s.poll.reads.Wait()
	}
	during(func() { token(pollToken+"2", false) })
	if got := f.stub.asked(); len(got) != 2 || got[1] != "group/app feature@a1 "+pollToken+"2" {
		t.Fatalf("a token changed while the round looked: %q", got)
	}
	token(pollToken+"3", false)
	if _, err := f.s.Status(context.Background(), w, false); err != nil || f.reads() != 3 || f.stub.asked()[2] != "group/app feature@a1 "+pollToken+"3" {
		t.Fatalf("the status read after a change of the token answered from the poll: %v, %q", err, f.stub.asked())
	}
	f.stub.gate = make(chan struct{})
	f.pass(pollSlow)
	f.start(w)
	deadline := time.Now().Add(5 * time.Second)
	for f.reads() < 4 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	token(pollToken+"4", false)
	f.s.poll.reads.Wait()
	if polled, ok := f.s.Polled("app", w.Binding, "feature"); !ok || polled.Err != nil || len(f.moves()) != 0 {
		t.Fatalf("the read the change ended shows %+v, %v", polled, ok)
	}
	f.stub.gate = nil
	f.round(w)
	if got := f.stub.asked(); len(got) != 5 || got[3] != "group/app feature@a1 "+pollToken+"3" || got[4] != "group/app feature@a1 "+pollToken+"4" {
		t.Fatalf("a token changed while a read was in flight: %q", got)
	}
	during(func() { token("", true) })
	f.round(w)
	if f.reads() != 5 {
		t.Fatalf("a token taken away was sent: %q", f.stub.asked())
	}
	if _, ok := f.s.Polled("app", w.Binding, "feature"); ok || !slices.Equal(f.moves(), []string{"app"}) {
		t.Fatal("the status of a host whose token went stayed")
	}
}

// At most pollParallel reads run at once, and a read whose branch moved
// meanwhile is dropped, the moved branch read in the next round.
func TestThePollerBoundsItsReads(t *testing.T) {
	f := newPollFixture(t)
	f.stub.gate = make(chan struct{})
	var ws []Watch
	for i := range 10 {
		ws = append(ws, f.watch(fmt.Sprintf("app%d", i), "feature", "a1"))
	}
	f.start(ws...)
	deadline := time.Now().Add(5 * time.Second)
	for f.reads() < pollParallel && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if f.reads() != pollParallel {
		t.Fatalf("%d reads started at once", f.reads())
	}
	moved := slices.Clone(ws)
	moved[0].Head = "a2"
	f.start(moved...)
	close(f.stub.gate)
	f.s.poll.reads.Wait()
	if f.reads() != 10 || f.stub.most > pollParallel {
		t.Fatalf("%d reads, at most %d at once", f.reads(), f.stub.most)
	}
	if _, ok := f.s.Polled("app0", ws[0].Binding, "feature"); ok {
		t.Fatal("the read of a branch that moved meanwhile was kept")
	}
	f.round(moved...)
	if got := f.stub.asked(); f.reads() != 11 || got[10] != "group/app0 feature@a2 "+pollToken {
		t.Fatalf("the moved branch: %q", got)
	}
}

// What the poller read no longer than CacheFresh ago answers in place of
// the forge, an older read, a fresh read asked for and a branch it does not
// keep ask the forge; what the forge answers for the branch it keeps goes
// into the cache.
func TestStatusReadsTheCacheWhereItIsFresh(t *testing.T) {
	f := newPollFixture(t)
	f.stub.answer("a1", Running)
	w := f.watch("app", "feature", "a1")
	f.round(w)
	f.moves()
	status, err := f.s.Status(context.Background(), w, false)
	if err != nil || status.Pipeline.Status != Running || f.reads() != 1 {
		t.Fatalf("a fresh read: %+v, %v, %d reads", status, err, f.reads())
	}
	if _, err := f.s.Status(context.Background(), w, true); err != nil || f.reads() != 2 {
		t.Fatalf("asked for fresh: %v, %d reads", err, f.reads())
	}
	f.stub.answer("a1", Failed)
	f.pass(CacheFresh + time.Second)
	status, err = f.s.Status(context.Background(), w, false)
	if err != nil || status.Pipeline.Status != Failed || f.reads() != 3 || !slices.Equal(f.moves(), []string{"app"}) {
		t.Fatalf("an old read: %+v, %v, %d reads", status, err, f.reads())
	}
	if polled, _ := f.s.Polled("app", w.Binding, "feature"); polled.Status.Pipeline.Status != Failed {
		t.Fatalf("the cache kept %s", polled.Status.Pipeline.Status)
	}
	f.round(w)
	if f.reads() != 3 {
		t.Fatal("the read of Status did not count for the poller")
	}
	f.stub.answer("b1", Success)
	other := f.watch("app", "main", "b1")
	if status, err := f.s.Status(context.Background(), other, false); err != nil || status.Pipeline.Status != Success || f.reads() != 4 {
		t.Fatalf("another branch: %+v, %v", status, err)
	}
	if polled, _ := f.s.Polled("app", w.Binding, "feature"); polled.Status.Pipeline.Status != Failed {
		t.Fatal("the read of another branch went into the cache")
	}
	if err := f.s.ChangeToken(f.host.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Status(context.Background(), w, true); !errors.Is(err, ErrNoToken) {
		t.Fatalf("without a token: %v", err)
	}
}
