package assistant

import (
	"maps"
	"testing"
)

// A running turn names the process the cockpit started for it with its
// assistant, and once the turn ended nothing is named any more.
func TestATurnNamesItsProcessWhileItRuns(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{})}
	svc, _, _, runs := newTestServiceIn(t, t.TempDir(), runner)
	created, _ := svc.create("claude")
	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, "the registered turn", func() bool { return len(runs.List()) == 1 })
	rec := runs.List()[0]
	waitFor(t, "the launched turn", func() bool { return len(svc.TurnProcesses()) == 1 })
	if got, want := svc.TurnProcesses(), map[int]string{rec.PID: created.ID}; rec.PID <= 0 || !maps.Equal(got, want) {
		t.Fatalf("the turns read %v, want %v", got, want)
	}
	close(runner.block)
	waitIdle(t, svc, created.ID)
	if got := svc.TurnProcesses(); len(got) != 0 {
		t.Fatalf("an ended turn still reads %v", got)
	}
}
