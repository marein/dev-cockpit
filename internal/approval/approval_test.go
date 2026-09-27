package approval

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/askpass"
)

// fixture is a service over a real broker, a switch per kind and a record of
// every end it reported.
type fixture struct {
	s      *Service
	broker *askpass.Broker
	path   string

	mu      sync.Mutex
	off     map[string]bool
	endings []ending
}

type ending struct {
	approval Approval
	outcome  Outcome
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{broker: askpass.New(dir), path: filepath.Join(dir, "approvals.json"), off: map[string]bool{}}
	f.s = f.service()
	f.s.SetBroker(f.broker)
	return f
}

// service builds a service over the fixture's state, which is also what a
// restart does.
func (f *fixture) service() *Service {
	return New(Config{
		Path: f.path,
		Asks: func(kind string) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return !f.off[kind]
		},
		StopAsking: func(kind string) {
			f.mu.Lock()
			f.off[kind] = true
			f.mu.Unlock()
		},
		Asker: func(owner string) string { return "Ops" },
		Ended: func(a Approval, o Outcome) {
			f.mu.Lock()
			f.endings = append(f.endings, ending{a, o})
			f.mu.Unlock()
		},
	})
}

func (f *fixture) question(t *testing.T) askpass.Question {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if list := f.broker.Questions(); len(list) == 1 {
			return list[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("the question never stood")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *fixture) ending(t *testing.T) ending {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		if len(f.endings) > 0 {
			e := f.endings[0]
			f.endings = f.endings[1:]
			f.mu.Unlock()
			return e
		}
		f.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("the approval never ended")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *fixture) decide(t *testing.T, decision askpass.Decision) {
	t.Helper()
	q := f.question(t)
	if !f.broker.Find(q.Key).Decide(q.ID, decision) {
		t.Fatal("the decision was refused")
	}
}

func counting(ran *int, err error) Run {
	return func() (string, error) {
		*ran++
		if err != nil {
			return "", err
		}
		return "done it", nil
	}
}

var details = []askpass.Detail{{Label: "Project", Value: "shop"}}

// deleting is the request to delete a project, keyed by its name.
func deleting(owner, name string, run Run) Request {
	return Request{Owner: owner, Kind: "delete", Key: name, What: "Delete project " + name, Details: details, Run: run}
}

// With the kind's switch off nothing is asked, run or written down: the
// caller takes the action itself.
func TestASwitchedOffKindAsksNothing(t *testing.T) {
	f := newFixture(t)
	f.off["delete"] = true
	ran := 0
	waiting, err := f.s.Ask(deleting("bot", "shop", counting(&ran, nil)))
	if err != nil || waiting || ran != 0 {
		t.Fatalf("the ask answered %v %v after %d runs", waiting, err, ran)
	}
	if len(f.broker.Questions()) != 0 || len(f.s.load()) != 0 {
		t.Fatal("a switched off kind asked or wrote something down")
	}
}

// An approval that asks runs nothing yet: the question stands with who asks,
// the line and the rows, and the entry is on disk without its run. Approving
// runs it and ends Done, quiet; the entry goes.
func TestAnApprovedActionRunsAndEndsDone(t *testing.T) {
	f := newFixture(t)
	ran := 0
	waiting, err := f.s.Ask(deleting("bot", "shop", counting(&ran, nil)))
	if err != nil || !waiting || ran != 0 {
		t.Fatalf("the ask answered %v %v after %d runs", waiting, err, ran)
	}
	q := f.question(t)
	if q.Kind != askpass.KindApproval || q.Assistant != "Ops" || q.Action != "Delete project shop" || len(q.Details) != 1 || q.Details[0] != details[0] {
		t.Fatalf("the question reads %+v", q)
	}
	if q.Command != "" || q.Dir != "" {
		t.Fatalf("an action without a program shows a command: %+v", q)
	}
	raw, err := os.ReadFile(f.path)
	if err != nil || !strings.Contains(string(raw), `"owner": "bot"`) || !strings.Contains(string(raw), `"kind": "delete"`) {
		t.Fatalf("the entry on disk reads %s, %v", raw, err)
	}
	f.decide(t, askpass.Decision{Approved: true})
	e := f.ending(t)
	if ran != 1 || e.approval.Owner != "bot" || e.approval.What != "Delete project shop" || e.outcome != (Outcome{Verdict: Done, Text: "done it", Quiet: true}) {
		t.Fatalf("the approval ended %+v after %d runs", e, ran)
	}
	if len(f.s.load()) != 0 || len(f.broker.Questions()) != 0 {
		t.Fatal("the approval outlived its decision")
	}
}

// An action that runs a program shows its command line and the directory it
// runs in, the way the kind handed them in.
func TestTheQuestionCarriesTheCommand(t *testing.T) {
	f := newFixture(t)
	req := deleting("bot", "shop", counting(new(int), nil))
	req.Command, req.Dir = "docker compose down -v", "/projects/shop"
	f.s.Ask(req)
	if q := f.question(t); q.Command != req.Command || q.Dir != req.Dir {
		t.Fatalf("the question reads %+v", q)
	}
}

// A run that fails once approved ends Failed with its reason, and that is news.
func TestAnApprovedActionThatFailsEndsFailed(t *testing.T) {
	f := newFixture(t)
	ran := 0
	f.s.Ask(deleting("bot", "shop", counting(&ran, errors.New("the directory is busy"))))
	f.decide(t, askpass.Decision{Approved: true})
	if e := f.ending(t); e.outcome != (Outcome{Verdict: Failed, Text: "the directory is busy"}) {
		t.Fatalf("the approval ended %+v", e.outcome)
	}
}

// A denial runs nothing and is the user's own click, quiet; a question that
// ends unanswered, which is what the timeout does, runs nothing and is news.
func TestADeniedOrUnansweredActionRunsNothing(t *testing.T) {
	f := newFixture(t)
	ran := 0
	f.s.Ask(deleting("bot", "shop", counting(&ran, nil)))
	f.decide(t, askpass.Decision{Approved: false, Remember: true})
	if e := f.ending(t); e.outcome != (Outcome{Verdict: Declined, Text: "declined by the user", Quiet: true}) {
		t.Fatalf("the denial ended %+v", e.outcome)
	}
	f.s.Ask(deleting("bot", "shop", counting(&ran, nil)))
	q := f.question(t)
	f.broker.Find(q.Key).End()
	if e := f.ending(t); e.outcome != (Outcome{Verdict: Declined, Text: "the approval expired unanswered"}) {
		t.Fatalf("the unanswered approval ended %+v", e.outcome)
	}
	if ran != 0 || f.off["delete"] {
		t.Fatalf("a declined approval ran %d times or remembered the box", ran)
	}
}

// Approve and don't ask again turns the kind's switch off.
func TestDontAskAgainTurnsTheKindOff(t *testing.T) {
	f := newFixture(t)
	ran := 0
	f.s.Ask(deleting("bot", "shop", counting(&ran, nil)))
	f.decide(t, askpass.Decision{Approved: true, Remember: true})
	f.ending(t)
	if !f.off["delete"] || f.off["compose"] {
		t.Fatalf("the switches read %v", f.off)
	}
}

// The same action waits once, whoever asks and however it is worded, one
// owner waits for at most MaxPerOwner, and without a broker nothing can be
// asked.
func TestAskingIsBounded(t *testing.T) {
	f := newFixture(t)
	ran := 0
	if _, err := f.s.Ask(deleting("bot", "shop", counting(&ran, nil))); err != nil {
		t.Fatal(err)
	}
	again := deleting("other", "shop", counting(&ran, nil))
	again.What = "Remove shop"
	if _, err := f.s.Ask(again); err == nil || err.Error() != "Delete project shop already waits for the user's approval." {
		t.Fatalf("the same action asked twice: %v", err)
	}
	other := deleting("other", "shop", counting(&ran, nil))
	other.Kind = "compose"
	if _, err := f.s.Ask(other); err != nil {
		t.Fatalf("the same key of another kind was refused: %v", err)
	}
	for i := 1; i < MaxPerOwner; i++ {
		if _, err := f.s.Ask(deleting("bot", "p"+strconv.Itoa(i), counting(&ran, nil))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.s.Ask(deleting("bot", "z", counting(&ran, nil))); err == nil {
		t.Fatal("an owner waited for more than the bound")
	}
	if _, err := f.s.Ask(deleting("other", "z", counting(&ran, nil))); err != nil {
		t.Fatalf("another owner was held to the first one's bound: %v", err)
	}
	bare := New(f.s.cfg)
	if _, err := bare.Ask(deleting("bot", "y", counting(&ran, nil))); err == nil {
		t.Fatal("an approval was asked without a broker")
	}
}

// An owner going away takes its approvals and their questions down, reports
// nothing and leaves the others standing.
func TestDecliningAnOwnerTakesItsApprovalsDown(t *testing.T) {
	f := newFixture(t)
	ran := 0
	f.s.Ask(deleting("bot", "shop", counting(&ran, nil)))
	f.s.Ask(deleting("other", "cart", counting(&ran, nil)))
	deadline := time.Now().Add(5 * time.Second)
	for len(f.broker.Questions()) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("the questions never stood")
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.s.DeclineOwner("bot")
	for len(f.broker.Questions()) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the owner's question still stands")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.endings) != 0 || ran != 0 {
		t.Fatalf("a declined owner was reported %+v or ran %d", f.endings, ran)
	}
	if list := f.s.load(); len(list) != 1 {
		t.Fatalf("the register holds %+v", list)
	}
}

// A deleted project takes down every approval touching it, one of several
// projects included, each owner hears why, and approvals of another project
// or of none keep standing.
func TestDecliningAProjectTakesItsApprovalsDown(t *testing.T) {
	f := newFixture(t)
	ran := 0
	shop := deleting("bot", "shop", counting(&ran, nil))
	shop.Projects = []string{"shop"}
	compose := Request{Owner: "other", Kind: "compose", Key: "shop up", What: "Up in shop", Projects: []string{"shop"}, Run: counting(&ran, nil)}
	coders := Request{Owner: "bot", Kind: "coder", Key: "c1\nc2", What: "Delete 2 coders", Projects: []string{"cart", "shop"}, Run: counting(&ran, nil)}
	cart := deleting("bot", "cart", counting(&ran, nil))
	cart.Projects = []string{"cart"}
	none := Request{Owner: "bot", Kind: "assistant", Key: "a1", What: "Delete assistant Ops", Run: counting(&ran, nil)}
	for _, req := range []Request{shop, compose, coders, cart, none} {
		if _, err := f.s.Ask(req); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(f.broker.Questions()) != 5 {
		if time.Now().After(deadline) {
			t.Fatal("the questions never stood")
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.s.DeclineProject("")
	f.s.DeclineProject("shop")
	owners := map[string]bool{}
	for range 3 {
		e := f.ending(t)
		if !slices.Contains(e.approval.Projects, "shop") || e.outcome != (Outcome{Verdict: Declined, Text: "the project was deleted"}) {
			t.Fatalf("the project's delete ended %+v", e)
		}
		owners[e.approval.Owner] = true
	}
	if !owners["bot"] || !owners["other"] {
		t.Fatalf("not every owner heard it: %v", owners)
	}
	for len(f.broker.Questions()) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("the project's questions still stand")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.endings) != 0 || ran != 0 {
		t.Fatalf("more was reported %+v or ran %d", f.endings, ran)
	}
	list := f.s.load()
	if len(list) != 2 {
		t.Fatalf("the register holds %+v", list)
	}
	for _, a := range list {
		if slices.Contains(a.Projects, "shop") {
			t.Fatalf("the register kept %+v", a)
		}
	}
}

// What the last process left waiting is declined at the next start, and each
// owner hears why; the run lived in that process and never runs.
func TestARestartDeclinesWhatWaited(t *testing.T) {
	f := newFixture(t)
	ran := 0
	f.s.Ask(deleting("bot", "shop", counting(&ran, nil)))
	f.question(t)
	f.service().Recover()
	e := f.ending(t)
	if e.approval.Owner != "bot" || e.outcome != (Outcome{Verdict: Declined, Text: "the approval was lost in a restart"}) {
		t.Fatalf("the restart ended %+v", e)
	}
	if len(f.s.load()) != 0 || ran != 0 {
		t.Fatal("the restart left the approval or ran it")
	}
}

// What the last process left waits for nobody, so the same action asked
// again before Recover declines it is no duplicate, and it counts for no
// owner's bound either.
func TestWhatTheLastProcessLeftBlocksNothing(t *testing.T) {
	f := newFixture(t)
	ran := 0
	for i := 0; i < MaxPerOwner; i++ {
		key := "a"
		if i > 0 {
			key = "p" + strconv.Itoa(i)
		}
		if _, err := f.s.Ask(deleting("bot", key, counting(&ran, nil))); err != nil {
			t.Fatal(err)
		}
	}
	f.broker = askpass.New(t.TempDir())
	restarted := f.service()
	restarted.SetBroker(f.broker)
	if waiting, err := restarted.Ask(deleting("bot", "a", counting(&ran, nil))); err != nil || !waiting {
		t.Fatalf("the action left by the last process blocked it again: %v %v", waiting, err)
	}
	restarted.Recover()
	for range MaxPerOwner {
		if e := f.ending(t); e.outcome.Text != "the approval was lost in a restart" {
			t.Fatalf("the recovery ended %+v", e)
		}
	}
	if list := restarted.load(); len(list) != 1 {
		t.Fatalf("the register holds %+v", list)
	}
	if q := f.question(t); q.Action != "Delete project a" {
		t.Fatalf("the question standing is %+v", q)
	}
}

// Recover declines what the last process left and nothing asked since: the
// local API serves before recovery runs, so a turn may ask in between, and
// that approval keeps waiting, its question standing and its run intact. A
// question never outlives its approval either way.
func TestRecoveryLeavesWhatWasAskedSince(t *testing.T) {
	f := newFixture(t)
	ran := 0
	f.s.Ask(deleting("bot", "shop", counting(&ran, nil)))
	f.question(t)
	// The restart: a new broker, nothing of the old process stands in it.
	f.broker = askpass.New(t.TempDir())
	restarted := f.service()
	restarted.SetBroker(f.broker)
	fresh := 0
	if waiting, err := restarted.Ask(deleting("bot", "cart", counting(&fresh, nil))); err != nil || !waiting {
		t.Fatalf("the ask in the gap answered %v %v", waiting, err)
	}
	f.question(t)
	restarted.Recover()
	e := f.ending(t)
	if e.approval.What != "Delete project shop" || e.outcome.Text != "the approval was lost in a restart" {
		t.Fatalf("the recovery ended %+v", e)
	}
	q := f.question(t)
	if q.Action != "Delete project cart" {
		t.Fatalf("the question standing is %+v", q)
	}
	if list := restarted.load(); len(list) != 1 {
		t.Fatalf("the register holds %+v", list)
	}
	f.decide(t, askpass.Decision{Approved: true})
	if e := f.ending(t); e.approval.What != "Delete project cart" || e.outcome.Verdict != Done || fresh != 1 || ran != 0 {
		t.Fatalf("the fresh approval ended %+v after %d runs, the old one ran %d", e, fresh, ran)
	}
}
