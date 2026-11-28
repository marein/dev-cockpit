package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/notify"
)

// The sources that keep a record of their own hand an event over until the
// reactor holds it: a coder's signal lies in the notify inbox, the end of a
// compose run in the register of runs and the end of an approval in the
// register of approvals. A forge keeps the event itself, and the forge
// service records of its own of the pipeline ends and review notes it handed
// on, which move once the reactor holds them. These tests run the real
// sources over one state directory, wired the way runServe and the web layer
// wire them, let a process die at every point between two writes of a hand
// over, and start the next one over what it left.

// handoverProcess is one process: the service with its reactor, and the four
// sources that hand to it, the forge service among them reading world.
type handoverProcess struct {
	f         *eventFixture
	dir       string
	notifier  *notify.Service
	docker    *docker.Service
	approvals *approval.Service
	broker    *askpass.Broker
	forge     *forge.Service
	world     *fakeForge
	// die is where this process dies, see handoverDeaths, watch the event it
	// dies over and died whether it did. over is closed when a hand over came
	// back or died.
	die   string
	watch string
	died  atomic.Bool
	over  chan struct{}
	// rang counts what the person heard: the news of the thread and the
	// signals of the notification center.
	rang *int
}

// The points a process dies at, in the order a hand over passes them: before
// the source's listener did anything, at the reactor's first look at its clock
// while no trigger or one of the two holds the event, and after the listener
// came back, before the source lets its record go.
var handoverDeaths = []string{"", "before the reactor heard", "before a trigger took", "between two triggers", "before the source let go"}

func handoverInbox(dir string) string { return notify.InboxDir(dir, "claude") }

// newHandoverProcess starts a process over dir. The approvals it finds are
// the last process's from here on, as they are for a server that was built.
func newHandoverProcess(t *testing.T, dir, owner, die, watch string, rang *int, world *fakeForge) *handoverProcess {
	t.Helper()
	p := &handoverProcess{dir: dir, die: die, watch: watch, over: make(chan struct{}), rang: rang, world: world}
	p.f = newEventFixtureIn(t, dir, &fakeRunner{answer: eventAnswer})
	p.f.svc.SetHooks(func() {}, func(string) { *rang++ })
	clock := p.f.reactor.now
	p.f.reactor.now = func() time.Time {
		switch held := p.held(owner); {
		case p.die == "before a trigger took" && held == 0, p.die == "between two triggers" && held == 1:
			p.dies()
		}
		return clock()
	}
	// heard wraps a listener of a source in the two deaths around it.
	heard := func(fn func()) {
		defer func() {
			select {
			case <-p.over:
			default:
				close(p.over)
			}
		}()
		if p.die == "before the reactor heard" {
			p.dies()
		}
		fn()
		if p.die == "before the source let go" {
			p.dies()
		}
	}

	p.notifier = notify.NewService(notify.StorePath(dir), nil)
	p.notifier.SetSignal(func(string) { *rang++ })
	p.notifier.SetEvent(func(targetID, hook, entry string) {
		heard(func() {
			kind := CoderKindEnded
			if hook == "Notification" {
				kind = CoderKindAsks
			}
			p.f.reactor.Coder(targetID, kind, entry)
		})
	})

	p.docker = docker.NewService(dir, func() string { return "" })
	p.docker.OnComposeDone(func(run docker.ComposeRun, err error, output string) {
		heard(func() {
			report := ComposeReport{Owner: run.Owner, Run: run.ID, Project: run.Label, Action: run.Action, Failed: run.Failed, Exited: run.Exited, Exit: run.Exit, Output: output, ByUser: run.ByUser}
			if err != nil {
				report.Reason = err.Error()
			}
			if p.f.svc.RecordCompose(report) == "" {
				p.docker.DisownRun(run.ID)
			}
		})
	})

	p.broker = askpass.New(dir)
	p.approvals = approval.New(approval.Config{
		Path:       filepath.Join(dir, "approvals.json"),
		Asks:       func(string) bool { return true },
		StopAsking: func(string) {},
		Asker:      func(string) (string, string) { return "Ops", "0a1b2c3d" },
		Ended: func(a approval.Approval, outcome approval.Outcome) {
			heard(func() {
				p.f.svc.RecordApproval(ApprovalReport{ID: a.ID, Owner: a.Owner, What: a.What, Outcome: outcome, Event: ComposeApprovalEvent})
			})
		},
	})
	p.approvals.SetBroker(p.broker)

	p.forge = world.service(t, dir, p.f.reactor, func(_ string, hand func()) { heard(hand) }, func(_, kind string) {
		if kind == PipelineKindFailed {
			*rang++
		}
	})
	return p
}

// dies ends the goroutine that hands over, right where it stands, which is
// what a kill leaves of it.
func (p *handoverProcess) dies() {
	p.died.Store(true)
	runtime.Goexit()
}

// held counts the triggers of the owner that hold the watched event.
func (p *handoverProcess) held(owner string) int {
	n := 0
	for _, trigger := range p.f.reactor.ListOf(owner) {
		if slices.ContainsFunc(trigger.Pending, func(ev CockpitEvent) bool { return ev.ID == p.watch }) {
			n++
		}
	}
	return n
}

// start is what a start does with the sources, in runServe's order, each to
// its end or to where the process dies in it.
func (p *handoverProcess) start(t *testing.T) {
	t.Helper()
	passes := []func(){
		p.f.reactor.Recover, p.docker.Recover, p.approvals.Recover,
		func() { p.notifier.DrainInbox(handoverInbox(p.dir), "claude") },
		func() { p.forge.PollOnce(context.Background(), p.world.watches) },
	}
	for _, pass := range passes {
		done := make(chan struct{})
		go func() {
			defer close(done)
			pass()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("a start never came back")
		}
	}
}

// handoverSource is one way an event comes to stand in a source's record.
// leave puts it there the way a process that is gone left it, and answers the
// name the event is handed under. live is the same for an end that only a
// running process brings about: it runs in the process that then dies.
type handoverSource struct {
	name  string
	event string
	// project is the project a trigger on a forge event names.
	project string
	leave   func(t *testing.T, dir, owner string, world *fakeForge) string
	live    func(t *testing.T, p *handoverProcess, owner string)
	// notes is how many notes the end leaves in the thread, rings how often
	// the person hears of it.
	notes, rings int
}

// approvalAsked asks for an approval in p and answers its id.
func approvalAsked(t *testing.T, p *handoverProcess, owner string, run approval.Run) string {
	t.Helper()
	waiting, err := p.approvals.Ask(approval.Request{Owner: owner, Kind: "compose-actions", Key: "shop\nup", What: "Compose up on shop", Run: run})
	if err != nil || !waiting {
		t.Fatalf("the approval was not asked: %v %v", waiting, err)
	}
	var list map[string]approval.Approval
	raw, _ := os.ReadFile(filepath.Join(p.dir, "approvals.json"))
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 1 {
		t.Fatalf("the register reads %s: %v", raw, err)
	}
	for id := range list {
		return id
	}
	return ""
}

// decided answers the one question standing in p.
func decided(t *testing.T, p *handoverProcess, decision askpass.Decision) {
	t.Helper()
	var q askpass.Question
	waitFor(t, "the question", func() bool {
		list := p.broker.Questions()
		if len(list) == 1 {
			q = list[0]
		}
		return len(list) == 1
	})
	if !p.broker.Find(q.Key).Decide(q.ID, decision) {
		t.Fatal("the decision was refused")
	}
}

var handoverSources = []handoverSource{
	{name: "a coder's signal", event: "coder-news", rings: 1,
		leave: func(t *testing.T, dir, owner string, _ *fakeForge) string {
			if err := os.MkdirAll(handoverInbox(dir), 0o755); err != nil {
				t.Fatal(err)
			}
			body := `{"terminal":"term-1","event":{"session_id":"s","hook_event_name":"Stop"}}`
			if err := os.WriteFile(filepath.Join(handoverInbox(dir), "1792071315017168190-11.json"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			return "coder claude/1792071315017168190-11"
		}},
	{name: "a compose run that failed", event: "compose-ended", notes: 1, rings: 1,
		leave: func(t *testing.T, dir, owner string, _ *fakeForge) string {
			runs := filepath.Join(dir, "docker", "runs")
			if err := os.MkdirAll(runs, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(runs, "run-1.result"), []byte("1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			entry := fmt.Sprintf(`[{"id":"run-1","dir":"/srv/shop","label":"shop","action":"Compose up","argv":["docker","compose","up","-d"],"timeout":120000000000,"owner":%q,"pid":4711,"startedAt":"2026-11-20T10:00:00Z"}]`, owner)
			if err := os.WriteFile(filepath.Join(dir, "docker", "runs.json"), []byte(entry), 0o600); err != nil {
				t.Fatal(err)
			}
			return "compose run-1 " + ComposeKindFailed
		}},
	{name: "an approval a restart declines", event: "compose-declined", notes: 1, rings: 1,
		leave: func(t *testing.T, dir, owner string, world *fakeForge) string {
			gone := newHandoverProcess(t, dir, owner, "", "", new(int), world)
			return "approval " + approvalAsked(t, gone, owner, nil) + " " + ComposeKindDeclined
		}},
	{name: "an approval the user declines", event: "compose-ended", notes: 1,
		live: func(t *testing.T, p *handoverProcess, owner string) {
			p.watch = "approval " + approvalAsked(t, p, owner, nil) + " " + ComposeKindDeclined
			decided(t, p, askpass.Decision{})
		}},
	{name: "an approved run that could not start", event: "compose-failed", notes: 1, rings: 1,
		live: func(t *testing.T, p *handoverProcess, owner string) {
			p.watch = "approval " + approvalAsked(t, p, owner, func() (string, error) { return "", fmt.Errorf("no reachable Docker host") }) + " " + ComposeKindFailed
			decided(t, p, askpass.Decision{Approved: true})
		}},
	{name: "a pipeline that succeeded", event: "forge-pipeline-succeeded", project: "shop", leave: pipelineLeft(forge.Success)},
	{name: "a pipeline that failed", event: "forge-pipeline-failed", project: "shop", rings: 1, leave: pipelineLeft(forge.Failed)},
	{name: "a pipeline that was canceled", event: "forge-pipeline-ended", project: "shop", leave: pipelineLeft(forge.Canceled)},
	{name: "a pipeline that was skipped", event: "forge-pipeline-ended", project: "shop", leave: pipelineLeft(forge.Skipped)},
	{name: "a review note of somebody else", event: "forge-review-comment", project: "shop", leave: noteLeft},
}

// An event that stands in a source's record reaches every trigger that
// matches it exactly once, wherever the process dies while it is handed over,
// and so does one that came to stand there while no process ran, which is the
// row that dies nowhere. The thread holds the note of an end once, the person
// hears of it once, the source lets its record go, and a further start hands
// nothing. For a forge that is the end of a pipeline of every kind and a note
// somebody else wrote, with the failed pipeline as the person's news, and the
// notes of the token's own account beside it reach nobody.
func TestAnEventOfADurableSourceIsTakenOnceWhereverTheProcessDies(t *testing.T) {
	for _, source := range handoverSources {
		for _, die := range handoverDeaths {
			t.Run(source.name+"/dies "+die, func(t *testing.T) {
				dir := t.TempDir()
				setup := newEventFixtureIn(t, dir, &fakeRunner{answer: eventAnswer})
				owner := setup.create(t).ID
				for range 2 {
					setup.trigger(t, TriggerSpec{Event: source.event, Project: source.project, Task: "say so", Batch: time.Hour, BatchSet: true})
				}

				rang := 0
				watch := ""
				world := newFakeForge()
				if source.leave != nil {
					watch = source.leave(t, dir, owner, world)
				}
				first := newHandoverProcess(t, dir, owner, die, watch, &rang, world)
				if source.live != nil {
					source.live(t, first, owner)
					watch = first.watch
					select {
					case <-first.over:
					case <-time.After(5 * time.Second):
						t.Fatal("the end was never handed")
					}
					if die == "" {
						waitFor(t, "the approval to leave the register", func() bool {
							raw, _ := os.ReadFile(filepath.Join(dir, "approvals.json"))
							return string(raw) == "{}\n" || string(raw) == "{}"
						})
					}
				} else {
					first.start(t)
				}
				if first.died.Load() != (die != "") {
					t.Fatalf("the process was to die %q, died: %v", die, first.died.Load())
				}

				for range 2 {
					next := newHandoverProcess(t, dir, owner, "", watch, &rang, world)
					next.start(t)
					for _, trigger := range next.f.reactor.ListOf(owner) {
						held := 0
						for _, ev := range trigger.Pending {
							if ev.ID == watch {
								held++
							}
						}
						if held != 1 || len(trigger.Pending) != 1 || !slices.Equal(trigger.Taken, []string{eventKey(watch)}) {
							t.Fatalf("want %q once in the window and on the account of trigger %s, got %d of %d waiting and %v", watch, trigger.ID, held, len(trigger.Pending), trigger.Taken)
						}
					}
					c, _ := next.f.store.Load(owner)
					if len(c.Messages) != source.notes {
						t.Fatalf("want %d notes in the thread, got %d: %+v", source.notes, len(c.Messages), c.Messages)
					}
					if rang != source.rings {
						t.Fatalf("want the person to hear of it %d times, got %d", source.rings, rang)
					}
				}

				if left, _ := os.ReadDir(handoverInbox(dir)); len(left) != 0 {
					t.Fatalf("the inbox still holds %v", left)
				}
				if raw, err := os.ReadFile(filepath.Join(dir, "approvals.json")); err == nil && string(raw) != "{}\n" && string(raw) != "{}" {
					t.Fatalf("the register of approvals still holds %s", raw)
				}
				var runs []docker.ComposeRecord
				if raw, err := os.ReadFile(filepath.Join(dir, "docker", "runs.json")); err == nil {
					if err := json.Unmarshal(raw, &runs); err != nil {
						t.Fatal(err)
					}
				}
				for _, run := range runs {
					if !run.Finished || run.Owed {
						t.Fatalf("the register of runs still owes %+v", run)
					}
				}
				if owed := world.owed(dir); owed != "" {
					t.Fatalf("the records of the forge do not hold %s", owed)
				}
			})
		}
	}
}

// The end of a compose run and of an approval that is reported a second time
// leaves the note that stands, under the id the first report answered, rings
// nobody again, and reaches a trigger that was not there the first time.
func TestAnEndReportedAgainLeavesOneNoteAndRingsOnce(t *testing.T) {
	for _, source := range eventSources[1:3] {
		t.Run(source.name, func(t *testing.T) {
			f := newEventFixture(t)
			f.create(t)
			early := f.sourceTrigger(t, source, "say so", time.Hour)
			rang := 0
			f.svc.SetHooks(func() {}, func(string) { rang++ })
			source.hand(f, 1)
			first, _ := f.store.Load(f.owner.ID)
			late := f.sourceTrigger(t, source, "say so", time.Hour)
			f.clock.advance(time.Minute)
			source.hand(f, 1)

			c, _ := f.store.Load(f.owner.ID)
			if len(c.Messages) != 1 || len(first.Messages) != 1 || c.Messages[0].ID != first.Messages[0].ID || !c.Messages[0].CreatedAt.Equal(first.Messages[0].CreatedAt) || rang != 1 {
				t.Fatalf("want the one note of the first report and one ring, got %d notes and %d rings", len(c.Messages), rang)
			}
			for _, trigger := range []Trigger{early, late} {
				fresh, _ := f.reactor.Get(trigger.ID)
				if len(fresh.Pending) != 1 || !fresh.Pending[0].Time.Equal(c.Messages[0].CreatedAt) {
					t.Fatalf("want the end once in the window of %s, dated like its note, got %+v", trigger.ID, fresh.Pending)
				}
			}
		})
	}
}
