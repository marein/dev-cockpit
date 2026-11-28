package approval

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/askpass"
)

// dying is a process over the fixture's state whose owner hook writes the end
// down for the test and dies there: the goroutine that reports ends in the
// hook, which is what a kill between an end and its owner hearing it leaves
// behind. A restart is a service of its own over the same file.
func (f *fixture) dying() *Service {
	s := f.service()
	hook := s.cfg.Ended
	s.cfg.Ended = func(a Approval, o Outcome) {
		hook(a, o)
		runtime.Goexit()
	}
	s.SetBroker(f.broker)
	return s
}

// through runs fn to its end, also where the process dies in it.
func through(t *testing.T, fn func()) {
	t.Helper()
	over := make(chan struct{})
	go func() {
		defer close(over)
		fn()
	}()
	select {
	case <-over:
	case <-time.After(5 * time.Second):
		t.Fatal("it never came back")
	}
}

// An approval stands in the register with its end until its owner heard it,
// and is gone after: the end is on disk before anybody is told.
func TestAnApprovalStandsWithItsEndUntilItsOwnerHeard(t *testing.T) {
	f := newFixture(t)
	var during map[string]Approval
	hook := f.s.cfg.Ended
	f.s.cfg.Ended = func(a Approval, o Outcome) {
		during = f.s.load()
		hook(a, o)
	}
	f.s.Ask(deleting("bot", "shop", counting(new(int), errors.New("the directory is busy"))))
	f.decide(t, askpass.Decision{Approved: true})
	e := f.ending(t)
	stood, ok := during[e.approval.ID]
	if !ok || stood.Outcome == nil || *stood.Outcome != e.outcome || e.outcome != (Outcome{Verdict: Failed, Text: "the directory is busy"}) {
		t.Fatalf("while its owner heard %+v the register held %+v", e.outcome, during)
	}
	f.gone(t)
	raw, _ := os.ReadFile(f.path)
	if strings.TrimSpace(string(raw)) != "{}" {
		t.Fatalf("the register reads %s", raw)
	}
}

// A process that dies between the end of an approval and its owner hearing it
// leaves the end in the register, and the next one tells it again: the same
// approval, the same end in the same words, whichever way it ended. The run
// never runs a second time, and the start after that tells nothing.
func TestAnEndTheLastProcessStillOwedIsToldAgain(t *testing.T) {
	for _, c := range []struct {
		name string
		end  func(t *testing.T, f *fixture, s *Service)
		want Outcome
		ran  int
	}{
		{name: "declined by the user", want: Outcome{Verdict: Declined, Text: "declined by the user", Quiet: true},
			end: func(t *testing.T, f *fixture, s *Service) { f.decide(t, askpass.Decision{}) }},
		{name: "expired", want: Outcome{Verdict: Declined, Text: "the approval expired unanswered"},
			end: func(t *testing.T, f *fixture, s *Service) { f.broker.Find(f.question(t).Key).End() }},
		{name: "approved and failed", want: Outcome{Verdict: Failed, Text: "the directory is busy"}, ran: 1,
			end: func(t *testing.T, f *fixture, s *Service) { f.decide(t, askpass.Decision{Approved: true}) }},
		{name: "approved and done", want: Outcome{Verdict: Done, Text: "done it", Quiet: true}, ran: 1,
			end: func(t *testing.T, f *fixture, s *Service) { f.decide(t, askpass.Decision{Approved: true}) }},
		{name: "its project deleted", want: Outcome{Verdict: Declined, Text: "the project was deleted"},
			end: func(t *testing.T, f *fixture, s *Service) {
				f.question(t)
				through(t, func() { s.DeclineProject("shop") })
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			ran := 0
			var fails error
			if c.want.Verdict == Failed {
				fails = errors.New("the directory is busy")
			}
			req := deleting("bot", "shop", counting(&ran, fails))
			req.Projects = []string{"shop"}
			s := f.dying()
			if waiting, err := s.Ask(req); err != nil || !waiting {
				t.Fatalf("the ask answered %v %v", waiting, err)
			}
			c.end(t, f, s)
			first := f.ending(t)
			if first.outcome != c.want || ran != c.ran {
				t.Fatalf("want %+v after %d runs, got %+v after %d", c.want, c.ran, first.outcome, ran)
			}
			left := f.service().load()
			if stood, ok := left[first.approval.ID]; len(left) != 1 || !ok || stood.Outcome == nil || *stood.Outcome != c.want {
				t.Fatalf("the process that died left %+v", left)
			}

			restarted := f.service()
			restarted.Recover()
			again := f.ending(t)
			if again.approval.ID != first.approval.ID || again.approval.What != first.approval.What || again.outcome != first.outcome {
				t.Fatalf("the end was told again as %+v, first as %+v", again, first)
			}
			if len(restarted.load()) != 0 || ran != c.ran {
				t.Fatalf("the restart left %+v or ran the action again, %d runs", restarted.load(), ran)
			}
			f.service().Recover()
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.endings) != 0 {
				t.Fatalf("an end its owner heard was told once more: %+v", f.endings)
			}
		})
	}
}

// The same for what a restart itself declines: a process that dies while it
// tells an owner that the approval was lost leaves it for the next one, which
// tells the same.
func TestAnApprovalLostInARestartThatDiedIsToldAgain(t *testing.T) {
	f := newFixture(t)
	ran := 0
	f.s.Ask(deleting("bot", "shop", counting(&ran, nil)))
	f.question(t)
	through(t, f.dying().Recover)
	first := f.ending(t)
	f.service().Recover()
	again := f.ending(t)
	want := Outcome{Verdict: Declined, Text: "the approval was lost in a restart"}
	if first.outcome != want || again.outcome != want || again.approval.ID != first.approval.ID || ran != 0 {
		t.Fatalf("the restarts told %+v and %+v after %d runs", first, again, ran)
	}
	if left := f.service().load(); len(left) != 0 {
		t.Fatalf("the register still holds %+v", left)
	}
}

// A process that dies in the middle of an approved run leaves no approval
// that reads as declined: the user said yes and the action started. The next
// start tells its owner that it failed and that nobody knows how far it got,
// and never runs it again.
func TestAProcessThatDiesInAnApprovedRunLeavesItFailed(t *testing.T) {
	f := newFixture(t)
	ran := make(chan struct{}, 2)
	f.s.Ask(deleting("bot", "shop", func() (string, error) {
		ran <- struct{}{}
		runtime.Goexit()
		return "", nil
	}))
	f.decide(t, askpass.Decision{Approved: true})
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the approved action never ran")
	}
	f.service().Recover()
	e := f.ending(t)
	if e.outcome != (Outcome{Verdict: Failed, Text: "the cockpit restarted while it ran, so how far it got is not known"}) || len(ran) != 0 {
		t.Fatalf("the restart told %+v after %d more runs", e.outcome, len(ran))
	}
}

// The run of an approved action may start something that reports its own end,
// and the process may die right behind that start. The next one asks the kind
// about every approval it finds failed, the one that died in its run and the
// one whose run said so itself, under the id the request named: where the run
// started, the approval ends the way it would have and is told once, also
// where the process dies while it tells it; where it did not, the end that
// stands is told. An approval that did not fail is none to ask about.
func TestTheNextProcessAsksWhetherAnApprovedRunStarted(t *testing.T) {
	cut := Outcome{Verdict: Failed, Text: "the cockpit restarted while it ran, so how far it got is not known"}
	went := Outcome{Verdict: Done, Text: "started run 7", Quiet: true}
	for _, c := range []struct {
		name    string
		approve bool
		run     Run
		started bool
		asked   int
		want    Outcome
	}{
		{name: "died in a run that started", approve: true, started: true, asked: 1, want: went,
			run: func() (string, error) { runtime.Goexit(); return "", nil }},
		{name: "died in a run that never started", approve: true, asked: 1, want: cut,
			run: func() (string, error) { runtime.Goexit(); return "", nil }},
		{name: "a run that failed by its own word", approve: true, asked: 1, want: Outcome{Verdict: Failed, Text: "the directory is busy"},
			run: counting(new(int), errors.New("the directory is busy"))},
		{name: "declined by the user", started: true, want: Outcome{Verdict: Declined, Text: "declined by the user", Quiet: true},
			run: counting(new(int), nil)},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			req := deleting("bot", "shop", c.run)
			req.ID = "named-by-its-kind"
			s := f.dying()
			if waiting, err := s.Ask(req); err != nil || !waiting {
				t.Fatalf("the ask answered %v %v", waiting, err)
			}
			f.decide(t, askpass.Decision{Approved: c.approve})
			for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
				if left := f.service().load()["named-by-its-kind"]; left.Outcome != nil && (*left.Outcome == cut || *left.Outcome == c.want) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the approval never stood under its id with an end: %+v", f.service().load())
				}
			}
			f.mu.Lock()
			f.endings = nil
			f.mu.Unlock()

			asked := 0
			restart := func() *Service {
				next := f.dying()
				next.cfg.Started = func(a Approval) (Outcome, bool) {
					asked++
					if a.ID != "named-by-its-kind" || a.What != "Delete project shop" {
						t.Errorf("the kind was asked about %+v", a)
					}
					return went, c.started
				}
				return next
			}
			through(t, restart().Recover)
			first := f.ending(t)
			if left := f.service().load()["named-by-its-kind"]; first.outcome != c.want || asked != c.asked || left.Outcome == nil || *left.Outcome != c.want {
				t.Fatalf("want %+v told after %d questions and left for a process that died telling it, got %+v after %d, left %+v", c.want, c.asked, first.outcome, asked, left.Outcome)
			}
			next := restart()
			next.cfg.Ended = f.service().cfg.Ended
			next.Recover()
			if again := f.ending(t); again.outcome != c.want || len(next.load()) != 0 {
				t.Fatalf("want the same end told again and the register empty, got %+v and %+v", again.outcome, next.load())
			}
			f.service().Recover()
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.endings) != 0 {
				t.Fatalf("an end its owner heard was told once more: %+v", f.endings)
			}
		})
	}
}

// An approval that is settled waits for nobody any more: while its owner is
// still being told, the same action may be asked again, and it counts for no
// bound.
func TestASettledApprovalBlocksNoNewAsk(t *testing.T) {
	f := newFixture(t)
	hold, held := make(chan struct{}), make(chan struct{})
	hook := f.s.cfg.Ended
	f.s.cfg.Ended = func(a Approval, o Outcome) {
		close(held)
		<-hold
		hook(a, o)
	}
	f.s.Ask(deleting("bot", "shop", counting(new(int), nil)))
	f.decide(t, askpass.Decision{})
	<-held
	f.s.cfg.Ended = hook
	if waiting, err := f.s.Ask(deleting("bot", "shop", counting(new(int), nil))); err != nil || !waiting {
		t.Fatalf("the action could not be asked again while its last end was told: %v %v", waiting, err)
	}
	close(hold)
	f.ending(t)
	if q := f.question(t); q.Action != "Delete project shop" {
		t.Fatalf("the question standing is %+v", q)
	}
}
