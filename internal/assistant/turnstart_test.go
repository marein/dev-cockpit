package assistant

import (
	"sync/atomic"
	"testing"
	"time"
)

// The moment a turn started is the one every working mark of the assistant is
// pinned to. It is taken once, when the turn is registered, and the answer to
// the send, the start frame, a page connecting mid answer and the register all
// carry that same moment, however long the turn runs.
func TestATurnKeepsTheMomentItStarted(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{})}
	svc, _, _, runs := newTestServiceIn(t, t.TempDir(), runner)
	base := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	var ticks atomic.Int64
	svc.now = func() time.Time { return base.Add(time.Duration(ticks.Add(1)) * time.Second) }
	created, err := svc.create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !svc.RunningSince(created.ID).IsZero() {
		t.Fatal("an assistant that runs nothing has no start")
	}

	frames := collectFrames(svc, created.ID)
	run, err := svc.Send(created.ID, "hello", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if run.StartedAt.IsZero() {
		t.Fatal("the answer to the send carries no start")
	}
	waitFor(t, "the registered turn", func() bool { return len(runs.List()) == 1 })
	if got := runs.List()[0].StartedAt; !got.Equal(run.StartedAt) {
		t.Fatalf("the register holds %v, the send answered %v", got, run.StartedAt)
	}
	if got := svc.RunningSince(created.ID); !got.Equal(run.StartedAt) {
		t.Fatalf("the running turn started at %v, the send answered %v", got, run.StartedAt)
	}
	snapshot, running, _, cancel := svc.Subscribe(created.ID)
	cancel()
	if !running || snapshot.Kind != FrameStart || snapshot.Since != run.StartedAt.UnixMilli() {
		t.Fatalf("a page connecting mid answer gets %+v, want the start at %d", snapshot, run.StartedAt.UnixMilli())
	}

	close(runner.block)
	waitIdle(t, svc, created.ID)
	if !svc.RunningSince(created.ID).IsZero() {
		t.Fatal("a turn that ended still has a start")
	}
	starts := 0
	for _, f := range frames.stop() {
		if f.Kind != FrameStart {
			continue
		}
		starts++
		if f.Since != run.StartedAt.UnixMilli() {
			t.Fatalf("the start frame carries %d, want %d", f.Since, run.StartedAt.UnixMilli())
		}
	}
	if starts != 1 {
		t.Fatalf("want one start frame, got %d", starts)
	}
}

// A restart in the middle of an answer keeps the moment it started: the
// register holds it, so the mark of the adopted turn stands where it stood.
func TestAnAdoptedTurnKeepsTheMomentItStarted(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{block: make(chan struct{})}
	svc, _, _, _ := newTestServiceIn(t, dir, runner)
	created, _ := svc.create("claude")
	rec := orphanTurn(t, svc, created.ID, runner)

	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	frames := collectFrames(restarted, created.ID)
	restarted.Recover()
	waitFor(t, "the restarted server to pick the turn up", func() bool { return !restarted.RunningSince(created.ID).IsZero() })
	if got := restarted.RunningSince(created.ID); !got.Equal(rec.StartedAt) {
		t.Fatalf("the adopted turn started at %v, want %v", got, rec.StartedAt)
	}

	close(runner.block)
	waitIdle(t, restarted, created.ID)
	for _, f := range frames.stop() {
		if f.Kind == FrameStart && f.Since != rec.StartedAt.UnixMilli() {
			t.Fatalf("the adopted start frame carries %d, want %d", f.Since, rec.StartedAt.UnixMilli())
		}
	}
}
