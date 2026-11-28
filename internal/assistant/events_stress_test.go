package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/statefile"
)

// The stress test of the events. It plays random rounds over several
// assistants, their jobs, the events of every other source and their
// triggers, kills the process at random points and checks after every kill
// what each trigger took.
//
// A process is a world: a service, a reactor and a watcher over one state
// directory. A kill moves that directory away under the world, so no write of
// it lands any more, wherever its goroutines stand, and the next world is
// built over the moved directory, from the disk alone.
//
// What a trigger took is read off its file. Every take and every fire reads
// the reactor's clock under the reactor's lock before it writes, so a look at
// the files at every clock read sees every write of a trigger on its own: the
// window as a take left it, whole events and the fold, the account that names
// what the fold only counts, the turn a fire wrote before its conclusion takes
// it off again, a schedule before and after its tick, and the register of
// runs.
//
// The sources are played the way they hand over: a job closes through the
// watcher and a schedule ticks with the clock. A coder's signal, the end of a
// compose run and the end of an approval come out of the record their source
// keeps, through that source itself: the notify inbox with its poller, the
// register of runs with the docker service and the register of approvals
// with the approval service, each over the state directory of its world and
// wired the way runServe and the web layer wire them. Such an event happened
// once it stands in the record, also while no world runs, and whatever a kill
// leaves there the next start has to hand on its own: the test hands nothing
// again that a kill cut short there, it only holds the start to it.
//
// A pipeline's end and a review note stand on a forge, which is one fake
// forge that outlives every world, and come in through the forge service of
// a world with its poller, which keeps under the state directory what it
// handed on. Such an event happened once the forge shows it, also while no
// world runs, and is news as far as the forge still tells it: the working
// copy of its project stands on its branch and no later pipeline took the
// place of one nobody read. Now and then a working copy changes its branch,
// and a read finds the forge down or limiting its requests first.
//
// Every source now and then hands again what it already handed: its record
// holds the event once more, and of a forge the record of what was handed on
// lets it go, as if it had not got to move.
//
// A job closes with a report, and the close and the report are two files:
// every close of this test carries a number of its own in what it reports,
// which the job keeps on its line and the report in its text, so a look at
// the store says which closes reached the disk and a look at the threads how
// often each was reported. The report rings behind the thread write, through
// the notification center of its world, a third file, and the test books
// what that center holds of every report.
//
// A reaction is a real process in one round of five, held open until the test
// lets it go, so the wake slots fill up and turns wait for one, and a process
// outlives the kill and is read on by the next world. In the other rounds the
// coder refuses to start, which is a turn that failed: a process costs far
// more than everything else here.
//
// The seed is random and printed with every failure. The two variables give a
// run its seed and its length back.
const (
	stressSeedEnv   = "DC_EVENTS_STRESS_SEED"
	stressRoundsEnv = "DC_EVENTS_STRESS_ROUNDS"
	// stressRounds is the default run, a few seconds with the race detector.
	stressRounds = 10
	// stressEpoch is how many rounds one state lives. Every epoch starts on
	// files shaped like the releases before wrote them.
	stressEpoch  = 250
	stressOwners = 3
	// stressTriggers is how many triggers an assistant holds at most here.
	// Every take reads and writes the whole file, so this is what a round
	// costs.
	stressTriggers = 10
	// stressRecent is how many of the events handed last a source may hand
	// again.
	stressRecent = 12
	// stressLastBatch is the batch window of the one shots every run starts
	// with.
	stressLastBatch = 2 * time.Second
)

// stressSources are the sources the books are kept by, stressCoders the
// terminals a coder's signal comes from and stressProjects the projects a
// forge event is about.
var (
	stressSources  = []string{EventJob, EventCoder, EventCompose, EventForge, EventCron}
	stressCoders   = []string{"coder-1", "coder-2", "coder-3", "coder-4"}
	stressProjects = []string{"demo", "shop"}
)

// stressKey names one event on the books of the test: the end of a job by its
// terminal and when it closed, every other event by the name its source gave
// it, and an event the releases before left in a window, which has none, by
// what it says.
type stressKey string

func stressKeyOf(ev CockpitEvent) stressKey {
	switch {
	case ev.Source == EventJob:
		return stressJobKey(ev.Target, ev.Time)
	case ev.ID == "":
		return stressKey("unnamed " + ev.Headline)
	}
	return stressKey(ev.ID)
}

func stressJobKey(terminal string, closed time.Time) stressKey {
	return stressKey(fmt.Sprintf("job %s %d", terminal, closed.UnixNano()))
}

// stressSplit is a window or a turn as the test reads it: the events that
// stand whole, and how many the fold behind them counts under how many lines,
// minus one where its words do not say what it counts: the count in front,
// and a line of text for every line it keeps.
func stressSplit(events []CockpitEvent) (whole []CockpitEvent, folded, lines int) {
	n := len(events)
	if n == 0 || !events[n-1].folds() {
		return events, 0, 0
	}
	fold := events[n-1]
	folded, lines = fold.count(), len(fold.Folded)
	if !strings.HasPrefix(fold.Headline, fmt.Sprintf("%d more event", folded)) || strings.Count(fold.Body, "\n- ")+1 != lines || !strings.HasPrefix(fold.Body, "- ") {
		lines = -1
	}
	return events[:n-1], folded, lines
}

// stressEvent is one event on the books: enough of it to ask a trigger
// whether it matches, and from when it is owed. A job's end is owed from the
// moment it closed. An event of another source is owed from the last time its
// source handed it over and the process lived to take it everywhere; one
// whose hand over a kill cut short is owed nothing yet and is handed again by
// the next start. cut is when that hand over began, until the start has
// looked at the record, see letGo.
//
// leave puts the event into the record of its source, where it stands from
// then on whatever becomes of the world, and answers whether it got there:
// one that did not never happened. pass is the source reading its record,
// which is the poller's round, the end of a run and a start alike. forge is
// where an event of a forge stands there, and happened whether it does.
type stressEvent struct {
	ev       CockpitEvent
	owed     time.Time
	cut      time.Time
	leave    func(w *stressWorld) bool
	pass     func(w *stressWorld)
	forge    *stressForge
	happened bool
}

// stressForge is where an event stands on the forge: the project and the
// branch its working copy stood on, and the pipeline with the status it
// ended in, or the note, which has no status.
type stressForge struct {
	project, branch, id string
	status              forge.Status
}

// held reports whether the records of a forge service hold the event as
// handed on, as end and note read them, see fakeForge.handed.
func (at *stressForge) held(end func(project, id string, status forge.Status) bool, note func(project, branch, id string) bool) bool {
	if at.status != "" {
		return end(at.project, at.id, at.status)
	}
	return note(at.project, at.branch, at.id)
}

// stressCount is what one source did in a run: the events there were, how
// often one of them was handed again, the takes, how many of those the fold
// of a full window counted, the turns its triggers bought, the takes of an
// event handed again after its trigger's account had let it go, the events a
// start found in the record of their source, which a kill had left there or
// which, down counts those, came to stand there while no world ran.
type stressCount struct {
	events, again, taken, folded, turns, forgotten, left, down int
}

// stressAsk is one approval on the books, by the line it is read by: who
// asked and in which world, whether its entry reached the register for sure,
// whether the user approved it and its run started, and the event its end
// published once its owner heard, "none" for an end that publishes none.
type stressAsk struct {
	owner string
	gen   int
	sure  bool
	ran   bool
	ended bool
	event stressKey
}

// stressSeen is what the observer knows of one trigger.
type stressSeen struct {
	owner  string
	since  time.Time
	legacy bool
	fired  int
	// window are the events that wait whole, folded the ones the fold behind
	// them counts, and turned the folded ones the last fire spent, until the
	// observer sees the turn that reads them.
	window, folded, turned []stressKey
	// ends and keys are the account as the last look found it, of a job
	// trigger and of every other.
	ends map[stressKey]bool
	keys []string
	// taken counts the takes of an event. named counts the events the
	// account named so far and at is that count when it named an event last:
	// one handed again after the account let its key go is taken again, and
	// that is the bound and no loss of count.
	taken map[stressKey]int
	at    map[stressKey]int
	named int
	// spent are the events a fire moved into a turn.
	spent map[stressKey]string
	// nextAt is where a schedule stood at the last look.
	nextAt time.Time
}

func newStressSeen(owner string, since time.Time, fired int) *stressSeen {
	return &stressSeen{
		owner: owner, since: since, fired: fired,
		ends: map[stressKey]bool{}, taken: map[stressKey]int{}, at: map[stressKey]int{}, spent: map[stressKey]string{},
	}
}

// stressTurn is one reaction a fire wrote on a trigger.
type stressTurn struct {
	owner, trigger string
}

// stressClose is one close of a job on the books, by the number its report
// carries: whose job it was, what it reports, whether the close reached the
// disk, and how often its report was found in a thread.
type stressClose struct {
	owner, terminal, text string
	landed                bool
	reports               int
}

// stressCloseMark reads the number of a close out of a job's line and out of
// a report.
var stressCloseMark = regexp.MustCompile(`close (\d+)`)

type stressWorld struct {
	dir, next string
	gen       int
	svc       *Service
	reactor   *Reactor
	watcher   *Watcher
	// The sources that keep a record: the notification center with the inbox
	// it polls, the docker service with its register of runs, the approval
	// service with its register, behind the broker its questions stand in,
	// and the forge service with its records of what it handed on. draining
	// is the one poller an inbox has, and heard the signal it handed last.
	// kept are the review notes the forge service handed on while this world
	// lived, which its record holds from then on.
	notifier  *notify.Service
	docker    *docker.Service
	approvals *approval.Service
	broker    *askpass.Broker
	forge     *forge.Service
	draining  sync.Mutex
	heard     string
	kept      []stressKey
	dead      atomic.Bool
	// gone is closed once the directory moved: from then on nothing this
	// world writes lands.
	gone chan struct{}
	// fuse counts down the reactor's clock reads, pubFuse the published
	// events and turnFuse the service's clock reads until the process dies,
	// zero for never: a turn about to be registered, and the report of a job
	// between its close and the thread. srcFuse counts down the moments a
	// source that keeps a record is between two steps of letting an event go:
	// its listener came back and the record still holds the event, or the
	// record let it go and somebody else just heard of it. ringFuse counts
	// down the rings for a report: the thread holds it and nobody heard yet.
	// ringing is the one ring at a time whose entry the test reads back.
	fuse, pubFuse, turnFuse, srcFuse, ringFuse atomic.Int64
	published                                  atomic.Int64
	ringing                                    sync.Mutex
}

type stress struct {
	t     *testing.T
	seed  int64
	rng   *rand.Rand
	root  string
	gen   int
	round int
	clock atomic.Int64
	world *stressWorld

	owners []string

	// runner is the coder of every world of an epoch, and held are the
	// reactions it keeps open, the oldest first; flowing lets them through.
	runner  *fakeRunner
	holdMu  sync.Mutex
	held    []chan struct{}
	flowing bool

	mu    sync.Mutex
	raw   map[string][]byte
	seen  map[string]*stressSeen
	turns map[string]stressTurn
	runs  map[string]string
	// events are the books: every end of a job the store held and every
	// event a source handed. digests reads an account's entry back into the
	// event it stands for, and recent are the events handed last.
	events  map[stressKey]*stressEvent
	digests map[string]stressKey
	recent  []stressKey
	// left are the events a kill left in the record of their source, which
	// the next start hands. drops counts how often a signal was dropped into
	// the inbox and rings how often the person heard of it; asks are the
	// approvals. runsMu is the one writer the register of runs has at a time.
	left                []stressKey
	drops, hooks, rings map[string]int
	asks                map[string]*stressAsk
	runsMu              sync.Mutex
	// closes are the closes of jobs, see stressClose. reportRings counts by
	// the id of a report the rings that wrote an entry for it, and reportHeard
	// holds the reports the notification center was seen to hold.
	closes      map[int]*stressClose
	reportRings map[string]int
	reportHeard map[string]bool
	// bothDown has the next start find one event of every source that keeps
	// a record, which came while no world ran.
	bothDown bool
	// forge is the forge of an epoch, and forging its one reader at a time,
	// which also holds off whatever changes what a read finds there. standing
	// are the events the forge shows that no world lived to hand on yet, ends
	// the pipeline end each branch stands at, and unsettled the projects
	// whose working copy changed its branch in a world that died before it
	// read the branch for sure. polls counts the rounds of the forge poller.
	forge     *fakeForge
	forging   sync.Mutex
	standing  map[stressKey]bool
	ends      map[string]stressKey
	unsettled map[string]bool
	polls     int
	alive     map[string]string
	terminal  int
	serial    int
	failure   string

	count map[string]*stressCount
	// takes are the takes of every source but the schedules, which tick with
	// the clock whoever looks. lastTurns are the turns of one shots that had
	// more to read than one turn reads whole.
	// retold are the ends of approvals a start told again with the end the
	// last world wrote down, lost the approvals a restart declined. switches
	// are the working copies that changed their branch and failedReads the
	// rounds of the forge poller that found the forge failing.
	// reports are the reports of jobs found in the threads, reportsCut the
	// ones a start found on a job whose thread did not hold them, which a
	// kill between the close and the report leaves, and reportsTold the ones
	// it found there while the thread held them already. reportsUnrung are
	// the kills between the thread write of a report and its ring. barriers
	// are the triggers made a barrier behind what their window held, resumed
	// the approvals a start found failed whose run stood in the register of
	// runs.
	crashes, takes, maxWindow, recovered, reactions, waited, lastTurns, retold, lost, switches, failedReads, reports, reportsCut, reportsTold, reportsUnrung, barriers, resumed int
}

// open lets every reaction end, the held ones and the ones to come, hold
// keeps the next ones open until free lets the oldest go.
func (s *stress) open() {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	s.flowing = true
	for _, ch := range s.held {
		close(ch)
	}
	s.held = nil
}

func (s *stress) hold() {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	s.flowing = false
}

func (s *stress) free(n int) {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	n = min(n, len(s.held))
	for _, ch := range s.held[:n] {
		close(ch)
	}
	s.held = s.held[n:]
}

func (s *stress) holdTurn(TurnRequest) chan struct{} {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	if s.flowing {
		return nil
	}
	ch := make(chan struct{})
	s.held = append(s.held, ch)
	return ch
}

func (s *stress) fail(format string, args ...any) {
	if s.failure == "" {
		s.failure = fmt.Sprintf(format, args...)
	}
}

func (s *stress) failed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failure != ""
}

// tick is the one clock of every world. It never gives a time twice, so an
// end is named by its terminal and its time, and it runs on over a kill.
func (s *stress) tick() time.Time {
	return time.Unix(0, s.clock.Add(int64(time.Microsecond))).UTC()
}

func (s *stress) boot() *stressWorld {
	w := &stressWorld{
		dir:  filepath.Join(s.root, fmt.Sprintf("gen-%d", s.gen)),
		next: filepath.Join(s.root, fmt.Sprintf("gen-%d", s.gen+1)),
		gen:  s.gen,
		gone: make(chan struct{}),
	}
	store := NewStore(w.dir)
	w.svc = newService(store, NewRunStore(w.dir), fakeCoders{runner: s.runner}, &fakeWorkdirs{root: filepath.Join(w.dir, "projects")})
	w.svc.SetHooks(func() {}, func(string) {})
	// The report of a job rings behind its thread write, under the id of its
	// message, the way runServe wires it.
	w.svc.OnReport(func(owner, message string) {
		if w.dead.Load() {
			return
		}
		if w.ringFuse.Load() > 0 && w.ringFuse.Add(-1) == 0 {
			s.mu.Lock()
			s.reportsUnrung++
			s.mu.Unlock()
			s.kill(w)
			return
		}
		s.ring(w, owner, message)
	})
	// The service reads its clock once per turn, right before the turn is
	// launched and registered, and once per report of a job, behind the close
	// and before the thread is written.
	w.svc.now = func() time.Time {
		if !w.dead.Load() && w.turnFuse.Load() > 0 && w.turnFuse.Add(-1) == 0 {
			s.kill(w)
		}
		return time.Now()
	}
	w.reactor = w.svc.events
	w.reactor.now = func() time.Time {
		if !w.dead.Load() {
			s.observe(w)
			if w.fuse.Load() > 0 && w.fuse.Add(-1) == 0 {
				s.kill(w)
			}
		}
		return s.tick()
	}
	w.reactor.OnPublish(func(CockpitEvent) {
		if w.dead.Load() {
			return
		}
		w.published.Add(1)
		if w.pubFuse.Load() > 0 && w.pubFuse.Add(-1) == 0 {
			s.kill(w)
		}
	})
	w.watcher = NewWatcher(w.svc, w.reactor.jobs, nil, nil)
	w.watcher.now = s.tick
	s.wire(w)
	return w
}

// ring rings for the report of a job through the notification center of a
// world and books what the center holds of it, before and behind: an entry
// more is a ring the person heard, and no report is heard of twice, whichever
// world rings and however often. The person reads every entry at once, so the
// quiet window of the center swallows no ring here. A world that was killed
// reads and writes nothing real any more, so only a look that came back while
// it still stood is booked, see bookClose.
func (s *stress) ring(w *stressWorld, owner, message string) {
	w.ringing.Lock()
	defer w.ringing.Unlock()
	held := func() int {
		n := 0
		for _, entry := range w.notifier.List(0) {
			if entry.Key == message {
				n++
			}
		}
		return n
	}
	before := held()
	w.notifier.AddOnce(owner, message)
	w.notifier.MarkTargetRead(owner)
	after := held()
	if w.dead.Load() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if after > before {
		s.reportRings[message]++
	}
	if after > 0 {
		s.reportHeard[message] = true
	}
	if after > 1 || s.reportRings[message] > 1 {
		s.fail("the report %s of %s rang %d times and the notification center holds it %d times", message, owner, s.reportRings[message], after)
	}
}

// barred makes a trigger a barrier behind what its window holds: the trigger
// took more events than a window holds whole, and only now is it told to wait
// for the terminals of two of them, one that waits whole and one the fold
// counts. again is their source handing both once more, which for the end of
// a job the edit does itself. Each of them waits in the window, so the
// barrier misses neither terminal, and it took nothing twice for it, which
// the observer holds anyway.
func (s *stress) barred(w *stressWorld, id string, again func(), terminals ...string) {
	if _, _, err := w.reactor.Edit(id, "", TriggerSpec{Targets: targets(terminals...), TargetsSet: true, All: true, AllSet: true}); err != nil {
		s.t.Fatalf("a barrier behind its window: %v", err)
	}
	again()
	trigger, _ := w.reactor.Get(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.barriers++
	if trigger.Waiting() && !w.dead.Load() {
		s.fail("the barrier %s on %s was named behind the events of %v, which wait in its window of %d, and still misses one: %+v", id, trigger.Source, terminals, trigger.Held(), trigger.Targets)
	}
}

// between is a source standing between two steps of letting an event go,
// where the process may die, see stressWorld.srcFuse.
func (s *stress) between(w *stressWorld) {
	if !w.dead.Load() && w.srcFuse.Load() > 0 && w.srcFuse.Add(-1) == 0 {
		s.kill(w)
	}
}

// wire builds the sources of a world that keep a record, over its state
// directory, and connects them to its reactor the way runServe and the web
// layer do. The approvals the register holds at this moment are the last
// world's, as they are for a server that was just built.
func (s *stress) wire(w *stressWorld) {
	if err := os.MkdirAll(s.inbox(w), 0o755); err != nil {
		s.t.Fatalf("the inbox: %v", err)
	}
	w.notifier = notify.NewService(notify.StorePath(w.dir), nil)
	w.notifier.SetEvent(func(targetID, hook, entry string) {
		kind := CoderKindEnded
		if hook == "Notification" {
			kind = CoderKindAsks
		}
		w.reactor.Coder(targetID, kind, entry)
		s.between(w)
	})
	// Everybody but the reactor hears of a signal after its file went, the
	// hook listener first and the notification for the person behind it, and
	// neither more often than the signal was dropped.
	heard := func(count map[string]int, who, name string) {
		s.mu.Lock()
		if w.dead.Load() {
			s.mu.Unlock()
			return
		}
		count[name]++
		if count[name] > s.drops[name] {
			s.fail("the signal %s was dropped %d times and reached %s %d times", name, s.drops[name], who, count[name])
		}
		s.mu.Unlock()
		s.between(w)
	}
	w.notifier.SetHook(func(_ string, ev notify.HookEvent) {
		w.heard = ev.Session
		heard(s.hooks, "the hook listener", ev.Session)
	})
	w.notifier.SetSignal(func(string) { heard(s.rings, "the person", w.heard) })

	w.docker = docker.NewService(w.dir, func() string { return "" })
	w.docker.OnComposeDone(func(run docker.ComposeRun, err error, output string) {
		report := ComposeReport{Owner: run.Owner, Run: run.ID, Project: run.Label, Action: run.Action, Failed: run.Failed, Exited: run.Exited, Exit: run.Exit, Output: output, ByUser: true}
		if err != nil {
			report.Reason = err.Error()
		}
		if w.svc.RecordCompose(report) == "" {
			w.docker.DisownRun(run.ID)
		}
		s.between(w)
	})

	w.broker = askpass.New(w.dir)
	w.approvals = approval.New(approval.Config{
		Path:       filepath.Join(w.dir, "approvals.json"),
		Asks:       func(string) bool { return true },
		StopAsking: func(string) {},
		Asker:      func(string) (string, string) { return "Ops", "0a1b2c3d" },
		Ended:      func(a approval.Approval, outcome approval.Outcome) { s.told(w, a, outcome) },
		// An approved run is named after its approval and stands in the
		// register of runs, which says whether it started, the way the web
		// layer has it.
		Started: func(a approval.Approval) (approval.Outcome, bool) {
			_, started := w.docker.ComposeRunByID(stressRunOf(a.ID))
			if started && !w.dead.Load() {
				s.mu.Lock()
				s.resumed++
				s.mu.Unlock()
			}
			return approval.Outcome{Verdict: approval.Done, Text: "started", Quiet: true}, started
		},
	})
	w.approvals.SetBroker(w.broker)

	w.forge = s.forge.service(s.t, w.dir, w.reactor,
		func(id string, hand func()) { s.forgeHeard(w, id, hand) },
		func(id, _ string) { s.forgeTold(w, id) })
}

// forgeHeard is the forge service of a world handing an event to the
// reactor, which is before its record of the event moves. So the record must
// not hold the event: one that does is handed on a second time, and one that
// held it before the reactor did would be lost to a kill right here. And it
// holds every note of the same project handed on before, each of which went
// into the record on its own. An event the world lived to hand on is owed
// from before the reactor heard of it, and the process may die right behind
// it, with the reactor holding the event and the record not.
func (s *stress) forgeHeard(w *stressWorld, id string, hand func()) {
	key := stressKey(id)
	end, note := s.forge.handed(w.dir)
	s.mu.Lock()
	e := s.events[key]
	if !w.dead.Load() {
		switch {
		case e == nil || e.forge == nil:
			s.fail("the forge handed on %q, which is nobody's news", id)
		case e.forge.held(end, note):
			s.fail("the forge handed on %q while its record held it", id)
		default:
			for _, before := range w.kept {
				if at := s.events[before].forge; at.project == e.forge.project && !at.held(end, note) {
					s.fail("the forge handed on %q while its record did not hold %q, handed on before it", id, before)
				}
			}
		}
	}
	s.mu.Unlock()
	at := s.tick()
	hand()
	s.mu.Lock()
	if e != nil && e.forge != nil && !w.dead.Load() {
		delete(s.standing, key)
		if e.owed.IsZero() {
			e.owed = at
		}
		if e.forge.status == "" && !slices.Contains(w.kept, key) {
			w.kept = append(w.kept, key)
		}
	}
	s.mu.Unlock()
	s.between(w)
}

// forgeTold is the forge service of a world telling everybody else of a
// pipeline's end, the notification of a failed one: its record holds the end
// by then, and the process may die right behind it.
func (s *stress) forgeTold(w *stressWorld, id string) {
	end, note := s.forge.handed(w.dir)
	s.mu.Lock()
	if e := s.events[stressKey(id)]; e != nil && e.forge != nil && !w.dead.Load() && !e.forge.held(end, note) {
		s.fail("the forge told of %q before its record held it", id)
	}
	s.mu.Unlock()
	s.between(w)
}

// happen is an event coming to stand on the forge, and, of one that stood
// there before, the record of what was handed on letting it go again, so the
// next read hands it on once more. It answers whether the next read of a
// world that lives has to hand it on, and an event that could not get there
// never happened: its working copy changed its branch since, a pipeline
// would take the place of one no world lived to hand on, which the forge
// then tells nobody any more, or its project waits for a start to read the
// branch it changed to. A pipeline that takes the place of one the reactor
// holds while the record does not takes that one off what a start still has
// to hand.
func (s *stress) happen(w *stressWorld, key stressKey, author string) bool {
	s.forging.Lock()
	defer s.forging.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.events[key]
	at := e.forge
	spot := at.project + "/" + at.branch
	if w.dead.Load() || s.forge.branch(at.project) != at.branch {
		return false
	}
	switch {
	case e.happened:
		if (at.status != "" && s.ends[spot] != key) || !s.forget(w, at) || w.dead.Load() {
			return false
		}
		w.kept = slices.DeleteFunc(w.kept, func(k stressKey) bool { return k == key })
	case at.status == "":
		s.forge.note(at.project, at.branch, at.id, author)
		if s.serial%4 == 0 {
			s.forge.note(at.project, at.branch, at.id+"-own", strings.ToUpper(fakeForgeAccount))
		}
	default:
		prior, taken := s.ends[spot]
		if s.unsettled[at.project] || (taken && s.standing[prior]) {
			return false
		}
		s.left = slices.DeleteFunc(s.left, func(k stressKey) bool { return k == prior })
		s.ends[spot] = key
		s.forge.pipeline(at.project, at.branch, at.id, at.status)
	}
	if !e.happened {
		s.count[EventForge].events++
	}
	e.happened = true
	s.standing[key] = true
	return true
}

// forget takes an event out of the record the forge service of a world keeps
// of what it handed on, and answers whether it stood there and the record
// took the change: a note out of the notes of its change request, the end of
// a pipeline out of the ends of the pipeline its branch stands at.
func (s *stress) forget(w *stressWorld, at *stressForge) bool {
	name := "notes.json"
	if at.status != "" {
		name = "pipelines.json"
	}
	path := filepath.Join(w.dir, "forge", name)
	var file map[string]any
	if raw, err := os.ReadFile(path); err != nil || json.Unmarshal(raw, &file) != nil {
		return false
	}
	took := false
	drop := func(record map[string]any, list, value string) {
		before, _ := record[list].([]any)
		after := slices.DeleteFunc(slices.Clone(before), func(v any) bool { return v == value })
		took = took || len(after) != len(before)
		record[list] = after
	}
	projects, _ := file["projects"].(map[string]any)
	if at.status != "" {
		if record, _ := projects[at.project].(map[string]any); record != nil && record["pipeline"] == "#"+at.id && record["branch"] == at.branch {
			drop(record, "ended", string(at.status))
		}
	} else {
		records, _ := projects[at.project].([]any)
		for _, v := range records {
			if record, _ := v.(map[string]any); record != nil && record["change"] == float64(fakeForgeChanges[at.branch]) {
				drop(record, "notes", at.id)
			}
		}
	}
	if !took {
		return false
	}
	raw, err := json.Marshal(file)
	return err == nil && os.WriteFile(path, raw, 0o600) == nil
}

// poll is one round of the forge poller of a world, of which there is one at
// a time. Every seventh finds the forge down or limiting its requests first:
// a read that failed hands nothing on and moves no record, and the round
// behind it reads what that one could not.
func (s *stress) poll(w *stressWorld) {
	s.forging.Lock()
	defer s.forging.Unlock()
	if s.polls++; s.polls%7 == 0 {
		s.forge.failing([]error{errors.New("The forge is down."), &forge.RateLimitError{Wait: time.Minute}}[s.polls/7%2])
		w.forge.PollOnce(context.Background(), s.forge.watches)
		s.forge.failing(nil)
		if !w.dead.Load() {
			s.failedReads++
		}
	}
	w.forge.PollOnce(context.Background(), s.forge.watches)
}

// switchBranch has the working copy of a project change its branch, and the
// poller of a world read the branch it changed to, which it sees for the
// first time: whatever pipeline ended there is nobody's news. It waits for a
// project whose forge shows nothing that no world handed on yet, since the
// poller reads the branch a working copy stands on and no other. A world
// that dies before the read came back leaves the project to its next start.
func (s *stress) switchBranch(w *stressWorld, project string) {
	s.forging.Lock()
	defer s.forging.Unlock()
	s.mu.Lock()
	free := !w.dead.Load() && !s.unsettled[project]
	for key := range s.standing {
		free = free && s.events[key].forge.project != project
	}
	if free {
		s.unsettled[project] = true
	}
	s.mu.Unlock()
	if !free {
		return
	}
	s.forge.checkout(project, map[string]string{"main": "feature", "feature": "main"}[s.forge.branch(project)])
	w.forge.PollOnce(context.Background(), s.forge.watches)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !w.dead.Load() {
		delete(s.unsettled, project)
		s.switches++
	}
}

// inbox is the notify inbox of a world, the one the coder of this test drops
// its signals into.
func (s *stress) inbox(w *stressWorld) string { return notify.InboxDir(w.dir, "claude") }

// told is the hook an approval's end reaches its owner through. The end is
// booked before the reactor hears of it: which event it is shows only here,
// an approved run that failed, a decline, or the decline of a restart. An end
// that is told a second time, by the start after a kill, has to be the same
// one. A world that is dead books nothing and writes nothing that lands.
func (s *stress) told(w *stressWorld, a approval.Approval, outcome approval.Outcome) {
	report := ApprovalReport{ID: a.ID, Owner: a.Owner, What: a.What, Outcome: outcome, Event: ComposeApprovalEvent}
	s.mu.Lock()
	if w.dead.Load() {
		s.mu.Unlock()
		<-w.gone
		w.svc.RecordApproval(report)
		return
	}
	ask := s.asks[a.What]
	if ask == nil {
		// One the release before left waiting.
		ask = &stressAsk{owner: a.Owner, sure: true}
		s.asks[a.What] = ask
	}
	key := stressKey("none")
	if kind, ok := ComposeApprovalEvent.Kinds[outcome.Verdict]; ok {
		id := "approval " + a.ID + " " + kind
		key = stressKey(id)
		if s.events[key] == nil {
			s.events[key] = &stressEvent{ev: CockpitEvent{ID: id, Source: EventCompose, Kind: kind, Owner: a.Owner}}
			s.digests[eventKey(id)] = key
			s.count[EventCompose].events++
		}
	}
	_, started := w.docker.ComposeRunByID(stressRunOf(a.ID))
	switch {
	case started && key != "none":
		s.fail("the approval %q started the run %s, which reports its own end, and ends as %q beside it: %s", a.What, stressRunOf(a.ID), key, outcome.Text)
	case ask.ran && outcome.Verdict == approval.Declined:
		s.fail("the approval %q was approved and ran, and its owner reads that it was declined: %s", a.What, outcome.Text)
	case !ask.ended:
		if outcome.Text == "the approval was lost in a restart" {
			s.lost++
		}
	case ask.event != key:
		s.fail("the approval %q ended as %q and was told again as %q", a.What, ask.event, key)
	default:
		s.retold++
		s.count[EventCompose].again++
	}
	ask.ended, ask.event = true, key
	s.mu.Unlock()

	at := s.tick()
	w.svc.RecordApproval(report)
	s.mu.Lock()
	if e := s.events[key]; e != nil && !w.dead.Load() && e.owed.IsZero() {
		e.owed = at
	}
	s.mu.Unlock()
	s.between(w)
}

// kill is the kill -9: the directory goes away under the world. The
// processes of its reactions live on, like the detached ones they are.
func (s *stress) kill(w *stressWorld) {
	if !w.dead.CompareAndSwap(false, true) {
		return
	}
	if err := os.Rename(w.dir, w.next); err != nil {
		s.mu.Lock()
		s.fail("move the state away: %v", err)
		s.mu.Unlock()
	}
	close(w.gone)
	// The questions of a dead world wait for nobody: their goroutines end
	// here instead of in half an hour.
	for _, q := range w.broker.Questions() {
		if bridge := w.broker.Find(q.Key); bridge != nil {
			go bridge.End()
		}
	}
}

func (s *stress) instanceDir(w *stressWorld, owner string) string {
	return NewStore(w.dir).InstanceDir(owner)
}

// observe reads the trigger files of a world and books what every trigger
// took since the last look.
func (s *stress) observe(w *stressWorld) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, _ := RunPaths(w.dir)
	if raw, err := os.ReadFile(index); err == nil && !bytes.Equal(raw, s.raw[index]) {
		var runs []RunRecord
		if json.Unmarshal(raw, &runs) == nil {
			for _, rec := range runs {
				if rec.Kind != RunReaction {
					continue
				}
				if first, ok := s.runs[rec.MessageID]; ok && first != rec.ID {
					s.fail("the reaction %s was started as two turns, %s and %s", rec.MessageID, first, rec.ID)
				}
				s.runs[rec.MessageID] = rec.ID
			}
		}
	}
	for _, owner := range s.owners {
		dir := s.instanceDir(w, owner)
		raw, err := os.ReadFile(filepath.Join(dir, triggersFileName))
		if err != nil || bytes.Equal(raw, s.raw[owner]) {
			continue
		}
		// The bytes that were read are parsed, never the file a second time:
		// a kill can move it away in between.
		var list []Trigger
		if err := json.Unmarshal(raw, &list); err != nil {
			s.fail("the triggers of %s do not parse: %v", owner, err)
			continue
		}
		s.raw[owner] = raw
		listed := map[string]bool{}
		for _, trigger := range list {
			trigger.Owner = owner
			if len(trigger.Targets) == 0 && trigger.Target != "" {
				trigger.Targets = []TriggerTarget{{Terminal: trigger.Target}}
			}
			listed[trigger.ID] = true
			s.read(trigger)
		}
		for id, seen := range s.seen {
			if seen.owner == owner && !listed[id] {
				delete(s.seen, id)
			}
		}
	}
}

// stressAdded is what an account that keeps its newest entries named since
// the last look: the entries behind the longest tail of the old list the new
// one begins with.
func stressAdded(before, now []string) []string {
	for keep := min(len(before), len(now)); keep > 0; keep-- {
		if slices.Equal(before[len(before)-keep:], now[:keep]) {
			return now[keep:]
		}
	}
	return now
}

func (s *stress) read(trigger Trigger) {
	seen := s.seen[trigger.ID]
	if seen == nil {
		seen = newStressSeen(trigger.Owner, trigger.CreatedAt, trigger.Fired)
		s.seen[trigger.ID] = seen
	}
	count := s.count[trigger.Source]
	switch trigger.Fired {
	case seen.fired:
	case seen.fired + 1:
		// A fire spends a full window at most and the fold with it, the rest
		// is the next one. A one shot has no next one: its turn counts the
		// rest on its fold.
		spent := min(len(seen.window), maxTriggerPendingNotes)
		seen.window, seen.turned, seen.folded = seen.window[spent:], seen.folded, nil
		if trigger.State == TriggerDone {
			if len(seen.window) > 0 {
				s.lastTurns++
			}
			seen.window, seen.turned = nil, slices.Concat(seen.window, seen.turned)
		}
		seen.fired++
	default:
		s.fail("the observer lost a window of %s: fired %d, then %d", trigger.ID, seen.fired, trigger.Fired)
		return
	}
	if turn := trigger.Turn; turn != nil {
		whole, folded, _ := stressSplit(turn.Events)
		spend := func(key stressKey) {
			if other, ok := seen.spent[key]; ok && other != turn.MessageID {
				s.fail("trigger %s spent %q in two turns, %s and %s", trigger.ID, key, other, turn.MessageID)
			}
			seen.spent[key] = turn.MessageID
		}
		for _, ev := range whole {
			spend(stressKeyOf(ev))
		}
		if _, known := s.turns[turn.MessageID]; !known {
			s.turns[turn.MessageID] = stressTurn{owner: trigger.Owner, trigger: trigger.ID}
			s.reactions++
			count.turns++
			if len(whole) > maxTriggerPendingNotes {
				s.fail("a turn of %s reads %d events whole", trigger.ID, len(whole))
			}
			if folded != len(seen.turned) {
				s.fail("a turn of %s counts %d folded events, its window had folded %d", trigger.ID, folded, len(seen.turned))
			}
			for _, key := range seen.turned {
				spend(key)
			}
			seen.turned = nil
			// A one shot that fired has this turn and no other, and no window
			// any more: whatever it took is read here.
			if trigger.State == TriggerDone {
				for key := range seen.taken {
					if _, spent := seen.spent[key]; !spent {
						s.fail("the one shot %s took %q, which its one turn does not read", trigger.ID, key)
					}
				}
			}
		}
	}
	if !trigger.Open() {
		return
	}

	whole, counted, lines := stressSplit(trigger.Pending)
	s.maxWindow = max(s.maxWindow, len(whole))
	switch {
	case len(whole) > maxTriggerWindowNotes:
		s.fail("the window of %s holds %d events whole", trigger.ID, len(whole))
	case counted > 0 && len(whole) < maxTriggerWindowNotes:
		s.fail("the window of %s folds events while it has room for %d", trigger.ID, maxTriggerWindowNotes-len(whole))
	case lines < 0:
		s.fail("the fold of %s does not say in words what it counts", trigger.ID)
	case lines > maxTriggerPendingNotes+4:
		s.fail("the fold of %s grew to %d lines", trigger.ID, lines)
	case len(trigger.Taken) > maxTriggerTakenKeys:
		s.fail("the account of %s grew to %d entries", trigger.ID, len(trigger.Taken))
	}
	pending := make([]stressKey, 0, len(whole))
	arrived := map[stressKey]CockpitEvent{}
	for _, ev := range whole {
		if ev.folds() {
			s.fail("the window of %s holds a fold in front of its last entry", trigger.ID)
		}
		pending = append(pending, stressKeyOf(ev))
		arrived[stressKeyOf(ev)] = ev
	}
	if len(pending) < len(seen.window) || !slices.Equal(pending[:len(seen.window)], seen.window) {
		s.fail("the window of %s lost what it held: %v, then %v", trigger.ID, seen.window, pending)
		return
	}
	fresh := pending[len(seen.window):]

	// The account names what the take of this look took, and whatever it
	// names that does not stand whole in the window is what the fold counted.
	var named []stressKey
	if trigger.Source == EventJob {
		ends := map[stressKey]bool{}
		for _, end := range trigger.Ends {
			key := stressJobKey(end.Terminal, end.ClosedAt)
			if !seen.ends[key] {
				named = append(named, key)
			}
			ends[key] = true
		}
		seen.ends = ends
	} else {
		for _, digest := range stressAdded(seen.keys, trigger.Taken) {
			key, ok := s.digests[digest]
			if !ok {
				s.fail("the account of %s names an event nobody handed, %s", trigger.ID, digest)
				continue
			}
			named = append(named, key)
		}
		seen.keys = slices.Clone(trigger.Taken)
	}
	var folded []stressKey
	for _, key := range named {
		if !slices.Contains(fresh, key) {
			folded = append(folded, key)
		}
	}
	if grown := counted - len(seen.folded); grown != len(folded) {
		s.fail("the fold of %s counts %d more events while its account names %d that do not wait whole", trigger.ID, grown, len(folded))
		return
	}

	take := func(key stressKey) {
		ev, known := arrived[key]
		if booked := s.events[key]; !known && booked != nil {
			ev, known = booked.ev, true
		}
		if slices.Contains(named, key) {
			seen.named++
		}
		switch {
		case seen.taken[key] == 0:
		case trigger.Source != EventJob && seen.named-seen.at[key] > maxTriggerTakenKeys:
			// Taken anew, so a turn of its own reads it.
			count.forgotten++
			delete(seen.spent, key)
		default:
			s.fail("trigger %s took %q twice", trigger.ID, key)
		}
		seen.taken[key]++
		seen.at[key] = seen.named
		count.taken++
		if trigger.Source != EventCron {
			s.takes++
		}
		if !known {
			return
		}
		if !trigger.matches(ev) {
			s.fail("trigger %s on %s %s %v took an event it does not match: %s %s of %s", trigger.ID, trigger.Source, trigger.Kind, trigger.Terminals(), ev.Source, ev.Kind, ev.Target)
		}
		if seen.legacy && !trigger.All && !ev.Time.After(seen.since) {
			s.fail("trigger %s took the end of %s again, which the release before had delivered", trigger.ID, ev.Target)
		}
	}
	for _, key := range fresh {
		take(key)
	}
	for _, key := range folded {
		take(key)
		count.folded++
	}
	seen.window, seen.folded = pending, append(seen.folded, folded...)

	// A schedule moves on in the write that takes its tick, so the look that
	// finds it moved finds the tick taken.
	if trigger.Source == EventCron {
		if tick := stressKey(stressTickID(trigger.ID, seen.nextAt)); !seen.nextAt.IsZero() && !trigger.NextAt.Equal(seen.nextAt) {
			count.events++
			if seen.taken[tick] == 0 {
				s.fail("the schedule %s moved past its tick at %s and nobody took it", trigger.ID, seen.nextAt.Format(time.RFC3339))
			}
		}
		seen.nextAt = trigger.NextAt
		tick := stressTickID(trigger.ID, trigger.NextAt)
		s.digests[eventKey(tick)] = stressKey(tick)
	}
}

// stressTickID is the name a schedule gives the tick that falls due at a
// moment.
func stressTickID(trigger string, due time.Time) string {
	return "cron " + trigger + " " + due.UTC().Format(time.RFC3339)
}

// book reads the closed jobs a world holds onto the books.
func (s *stress) book(w *stressWorld) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, owner := range s.owners {
		for _, job := range NewJobStore(owner, s.instanceDir(w, owner)).List() {
			s.bookJob(job)
		}
	}
}

// bookClose books the end a driver just wrote, read back from the store. A
// world that was killed reads and writes nothing real any more: the flag is
// raised before the directory moves, so a read that returned while it was
// still down came from the real files.
func (s *stress) bookClose(w *stressWorld, terminal string) {
	job, ok := w.watcher.Get(terminal)
	if !ok || w.dead.Load() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bookJob(job)
}

func (s *stress) bookJob(job Job) {
	if ev, ok := jobEvent(job); ok {
		s.bookEnd(ev.Kind, ev.Target, ev.Owner, ev.Time)
		if closed := s.closeOf(job.Note); closed != nil {
			closed.landed = true
		}
	}
}

// closing puts a close on the books before it is tried and answers what it
// reports, the text of a check's report or the reason a job ran out for,
// with the number of the close behind it.
func (s *stress) closing(owner, terminal, text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serial++
	text = fmt.Sprintf("%s, close %d", text, s.serial)
	s.closes[s.serial] = &stressClose{owner: owner, terminal: terminal, text: text}
	return text
}

// closeOf is the close a job's line or a report names, nil where it names
// none: a job the releases before closed.
func (s *stress) closeOf(text string) *stressClose {
	match := stressCloseMark.FindStringSubmatch(text)
	if match == nil {
		return nil
	}
	n, _ := strconv.Atoi(match[1])
	return s.closes[n]
}

// owing reads the reports the jobs of a world still carry, which a kill
// behind a close leaves there, and answers how many of them their thread does
// not hold: those the watcher of the start that follows writes and publishes
// the end of. One the thread holds already was cut short behind it, and is
// neither written nor published again.
func (s *stress) owing(w *stressWorld) int {
	owed := 0
	for _, owner := range s.owners {
		c, _ := NewStore(w.dir).Load(owner)
		for _, job := range NewJobStore(owner, s.instanceDir(w, owner)).List() {
			switch {
			case job.Report == nil:
			case slices.ContainsFunc(c.Messages, func(m Message) bool { return m.ID == job.Report.MessageID }):
				s.reportsTold++
			default:
				s.reportsCut++
				owed++
			}
		}
	}
	return owed
}

// bookEnd books the end of a job, which is owed from the moment it closed.
func (s *stress) bookEnd(kind, terminal, owner string, closed time.Time) {
	if key := stressJobKey(terminal, closed); s.events[key] == nil {
		s.events[key] = &stressEvent{ev: CockpitEvent{Source: EventJob, Kind: kind, Target: terminal, Owner: owner, Time: closed}, owed: closed}
		s.count[EventJob].events++
	}
}

// The shapes of the events the sources but a job and a schedule hand: a
// coder's signal, the end of a compose run and of an approval, of a pipeline,
// and a review note.
const (
	stressSignal   = "signal"
	stressRun      = "run"
	stressApproval = "approval"
	stressPipeline = "pipeline"
	stressReview   = "review"
)

// source makes one event of a source, of whichever shape the dice pick.
func (s *stress) source(rng *rand.Rand, source, owner string) stressKey {
	shape := stressSignal
	switch source {
	case EventCompose:
		shape = stressRun
	case EventForge:
		shape = []string{stressPipeline, stressReview}[rng.Intn(2)]
	}
	return s.event(rng, shape, owner, stressProjects[rng.Intn(4)/3])
}

// event makes one event of a shape, under a name of its own, and puts it on
// the books, owed nothing until it is handed. Every one of them goes in
// through its source. One of a forge is about the branch the working copy of
// its project stands on.
func (s *stress) event(rng *rand.Rand, shape, owner, project string) stressKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serial++
	n := s.serial
	var e stressEvent
	switch shape {
	case stressSignal:
		// A coder's hook drops a file into the inbox, and the poller's next
		// round finds it.
		terminal, hook := stressCoders[rng.Intn(len(stressCoders))], []string{"Stop", "Notification"}[rng.Intn(2)]
		kind, name := map[string]string{"Stop": CoderKindEnded, "Notification": CoderKindAsks}[hook], fmt.Sprintf("%d-7", n)
		e.ev = CockpitEvent{ID: "coder claude/" + name, Source: EventCoder, Kind: kind, Target: terminal}
		e.leave = func(w *stressWorld) bool { return s.drop(w, name, terminal, hook) }
		e.pass = func(w *stressWorld) { s.drain(w) }
	case stressRun:
		// A run ends, the way the dice have it: with an exit code, killed
		// for taking too long, without a result, or called off.
		rec := docker.ComposeRecord{ID: fmt.Sprintf("run-%d", n), Dir: "/srv/" + project, Label: project, Action: "Compose up", Owner: owner, Timeout: 2 * time.Minute}
		result := []string{"0", "0", "0", "1", "124", ""}[rng.Intn(6)]
		if rng.Intn(8) == 0 {
			rec.Cancelled = true
		}
		kind := ComposeKindDone
		if result != "0" || rec.Cancelled {
			kind = ComposeKindFailed
		}
		e.ev = CockpitEvent{ID: "compose " + rec.ID + " " + kind, Source: EventCompose, Kind: kind, Target: rec.ID, Owner: owner}
		e.leave = func(w *stressWorld) bool { return s.runEnded(w, rec, result) }
		e.pass = func(w *stressWorld) { s.readRuns(w) }
	case stressReview:
		at := &stressForge{project: project, branch: s.forge.branch(project), id: fmt.Sprint(n)}
		author := fmt.Sprintf("author-%d", rng.Intn(30))
		id := s.forge.noteID(project, at.branch, at.id)
		e.ev = CockpitEvent{ID: id, Source: EventForge, Kind: ReviewKindComment, Project: project, Branch: at.branch}
		e.forge = at
		e.leave = func(w *stressWorld) bool { return s.happen(w, stressKey(id), author) }
		e.pass = func(w *stressWorld) { s.poll(w) }
	case stressPipeline:
		kind := pipelineEnds[rng.Intn(len(pipelineEnds))]
		at := &stressForge{project: project, branch: s.forge.branch(project), id: fmt.Sprint(n)}
		for status, name := range fakeForgeKinds {
			if name == kind {
				at.status = status
			}
		}
		id := s.forge.pipelineID(project, at.id, at.status)
		e.ev = CockpitEvent{ID: id, Source: EventForge, Kind: kind, Project: project, Branch: at.branch}
		e.forge = at
		e.leave = func(w *stressWorld) bool { return s.happen(w, stressKey(id), "") }
		e.pass = func(w *stressWorld) { s.poll(w) }
	}
	key := stressKey(e.ev.ID)
	s.events[key] = &e
	s.digests[eventKey(e.ev.ID)] = key
	// An event of a forge is one once the forge shows it, see happen.
	if e.forge == nil {
		s.count[e.ev.Source].events++
	}
	return key
}

// stressRunOf is the id of the run an approved command starts, named after
// its approval.
func stressRunOf(approval string) string {
	return statefile.DerivedID(nil, "compose-approval", approval)
}

// started puts the run of an approved command on the books, a run like every
// other that went through: it stands in the register of runs under the name
// its approval gives it, and the docker service reads its end from there.
func (s *stress) started(owner, approval string) stressKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := docker.ComposeRecord{ID: stressRunOf(approval), Dir: "/srv/approved", Label: "demo", Action: "Compose up", Owner: owner, Timeout: 2 * time.Minute}
	e := &stressEvent{ev: CockpitEvent{ID: "compose " + rec.ID + " " + ComposeKindDone, Source: EventCompose, Kind: ComposeKindDone, Target: rec.ID, Owner: owner}}
	e.leave = func(w *stressWorld) bool { return s.runEnded(w, rec, "0") }
	e.pass = func(w *stressWorld) { s.readRuns(w) }
	key := stressKey(e.ev.ID)
	s.events[key] = e
	s.digests[eventKey(e.ev.ID)] = key
	s.count[EventCompose].events++
	return key
}

// drop is a coder's hook writing a signal into the inbox of a world, under a
// .tmp name first like every writer. It answers whether the file got there
// for sure: a world that is dead when the write came back may have moved
// under it.
//
// A hook writes every signal under a name of its own. A signal that is handed
// again is dropped under its name once more, which no hook does, so that drop
// waits for the poller's round: a file that came to lie under a name the
// poller is about to let go would go with it, handed to nobody, and the books
// would hold a hand over that never was.
func (s *stress) drop(w *stressWorld, name, terminal, hook string) bool {
	s.mu.Lock()
	s.drops[name]++
	again := s.drops[name] > 1
	s.serial++
	tmp := filepath.Join(s.inbox(w), fmt.Sprintf("%s.%d.tmp", name, s.serial))
	s.mu.Unlock()
	if again {
		w.draining.Lock()
		defer w.draining.Unlock()
	}
	body := fmt.Sprintf(`{"terminal":%q,"event":{"session_id":%q,"hook_event_name":%q,"message":"Claude needs your permission"}}`, terminal, name, hook)
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return false
	}
	return os.Rename(tmp, filepath.Join(s.inbox(w), name+".json")) == nil && !w.dead.Load()
}

// drain is one round of the poller of a world's inbox, of which there is one.
func (s *stress) drain(w *stressWorld) {
	w.draining.Lock()
	defer w.draining.Unlock()
	w.notifier.DrainInbox(s.inbox(w), "claude")
}

// runEnded puts a run into the register of a world the way a run stands
// there that ended: registered by the server that started it, its lock gone,
// and what its hold process wrote down. It answers whether the entry got
// there for sure.
func (s *stress) runEnded(w *stressWorld, rec docker.ComposeRecord, result string) bool {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	index, files := filepath.Join(w.dir, "docker", "runs.json"), filepath.Join(w.dir, "docker", "runs")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return false
	}
	path := filepath.Join(files, rec.ID+".result")
	os.Remove(path)
	if result != "" {
		if err := os.WriteFile(path, []byte(result+"\n"), 0o600); err != nil {
			return false
		}
	}
	var list []docker.ComposeRecord
	if raw, err := os.ReadFile(index); err == nil {
		if err := json.Unmarshal(raw, &list); err != nil {
			return false
		}
	}
	list = append(slices.DeleteFunc(list, func(other docker.ComposeRecord) bool { return other.ID == rec.ID }), rec)
	raw, err := json.Marshal(list)
	if err != nil {
		return false
	}
	return os.WriteFile(index, raw, 0o600) == nil && !w.dead.Load()
}

// readRuns is the docker service of a world finding the runs that ended and
// the ends it still owes, which is what its recovery does.
func (s *stress) readRuns(w *stressWorld) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	w.docker.Recover()
}

// hand hands an event over the way its source does: it comes to stand in
// the record, and the source reads it. A hand over that came back while the
// world lived was taken everywhere, and the event is owed from before it
// began. One a kill cut short is the next start's to hand again: an event
// that reached the record is left there, and the start itself has to find
// it, unless the record let it go before the kill, see letGo. One that never
// got into the record never happened.
func (s *stress) hand(w *stressWorld, key stressKey) {
	s.mu.Lock()
	e := s.events[key]
	if !e.owed.IsZero() || slices.Contains(s.left, key) {
		s.count[e.ev.Source].again++
	}
	s.mu.Unlock()
	at := s.tick()
	stands := e.leave(w)
	e.pass(w)
	s.mu.Lock()
	defer s.mu.Unlock()
	if w.dead.Load() {
		if stands {
			if !slices.Contains(s.left, key) {
				s.left = append(s.left, key)
			}
			if at.After(e.cut) {
				e.cut = at
			}
		}
		return
	}
	if !stands {
		return
	}
	e.owed = at
	s.left = slices.DeleteFunc(s.left, func(k stressKey) bool { return k == key })
	s.recent = append(slices.DeleteFunc(s.recent, func(k stressKey) bool { return k == key }), key)
	if over := len(s.recent) - stressRecent; over > 0 {
		s.recent = slices.Clone(s.recent[over:])
	}
}

// again hands over once more some of what was handed last, the way a source
// does that could not write down in time that it had handed them.
func (s *stress) again(w *stressWorld, rng *rand.Rand, some int) {
	s.mu.Lock()
	var keys []stressKey
	for _, i := range rng.Perm(len(s.recent))[:min(some, len(s.recent))] {
		keys = append(keys, s.recent[i])
	}
	s.mu.Unlock()
	for _, key := range keys {
		s.hand(w, key)
	}
}

// ask puts an approval before the user of a world and ends it the way end
// has it, a number below a hundred, or the dice where it is negative: the
// project it touches is deleted, which ends it before this returns, the user
// approves and the run cannot start, or goes through, which publishes
// nothing, the user declines, or nobody answers before the world is gone, and
// the next start declines it. A run that goes through is a run in the
// register of runs, named after the approval, which ends and reports its own
// end like every run, and the process may die on either side of its start,
// before the approval's own end is written.
func (s *stress) ask(w *stressWorld, rng *rand.Rand, owner string, end int) {
	s.mu.Lock()
	s.serial++
	n := s.serial
	what := fmt.Sprintf("Compose up on demo, the %d.", n)
	ask := &stressAsk{owner: owner, gen: w.gen}
	s.asks[what] = ask
	s.mu.Unlock()
	if end < 0 {
		end = rng.Intn(100)
	}
	project := fmt.Sprintf("project-%d", n)
	id := statefile.NewID()
	waiting, err := w.approvals.Ask(approval.Request{
		ID: id, Owner: owner, Kind: "compose-actions", Key: fmt.Sprint(n), What: what, Projects: []string{project},
		Run: func() (string, error) {
			// The process may die in the middle of the run it approved.
			s.mu.Lock()
			ask.ran = ask.ran || !w.dead.Load()
			s.mu.Unlock()
			s.between(w)
			switch {
			case end < 40:
				return "", errors.New("a compose run is already under way here")
			case w.dead.Load():
				return "", errors.New("the process died before the run started")
			}
			s.hand(w, s.started(owner, id))
			s.between(w)
			return "started", nil
		},
	})
	s.mu.Lock()
	if ask.sure = err == nil && waiting && !w.dead.Load(); !ask.sure && !ask.ended {
		delete(s.asks, what)
	}
	s.mu.Unlock()
	switch {
	case err != nil || !waiting:
	case end < 25:
		w.approvals.DeclineProject(project)
	case end < 85:
		// The question stands a moment after the ask came back.
		for deadline := time.Now().Add(2 * time.Second); !w.dead.Load() && time.Now().Before(deadline); time.Sleep(100 * time.Microsecond) {
			for _, q := range w.broker.Questions() {
				if bridge := w.broker.Find(q.Key); q.Action == what && bridge != nil {
					bridge.Decide(q.ID, askpass.Decision{Approved: end < 55})
					return
				}
			}
		}
	}
}

// check holds the invariants against the disk of a world that is quiet: every
// event on the books reached every standing trigger that matches it and
// stood when it was owed, every event a trigger took is in its window or in a
// turn, and no trigger keeps an end on its account whose job the store let
// go. A trigger that is spent answers where its last turn is read, see read.
func (s *stress) check(w *stressWorld) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, owner := range s.owners {
		dir := s.instanceDir(w, owner)
		held := map[stressKey]bool{}
		for _, job := range NewJobStore(owner, dir).List() {
			if ev, ok := jobEvent(job); ok {
				held[stressKeyOf(ev)] = true
			}
		}
		for _, trigger := range NewTriggerStore(owner, dir).List() {
			for _, end := range trigger.Ends {
				if !held[stressJobKey(end.Terminal, end.ClosedAt)] {
					s.fail("trigger %s keeps the end of %s on its account, whose job the store let go", trigger.ID, end.Terminal)
				}
			}
			seen := s.seen[trigger.ID]
			if seen == nil {
				s.fail("the observer never saw trigger %s", trigger.ID)
				continue
			}
			if !trigger.Open() {
				continue
			}
			for key := range seen.taken {
				if _, spent := seen.spent[key]; !spent && !slices.Contains(seen.window, key) && !slices.Contains(seen.folded, key) {
					s.fail("trigger %s took %q, which is neither in its window nor in a turn", trigger.ID, key)
				}
			}
			// A barrier counts a terminal as arrived with the take of its
			// event and until a turn spends it, so none it still misses has
			// an event waiting in the window, whole or counted by the fold.
			for _, key := range slices.Concat(seen.window, seen.folded) {
				if e := s.events[key]; trigger.All && e != nil && slices.ContainsFunc(trigger.Targets, func(t TriggerTarget) bool {
					return t.Terminal == e.ev.Target && !t.Met && !t.Gone
				}) {
					s.fail("the barrier %s misses %s, whose event %q waits in its window", trigger.ID, e.ev.Target, key)
				}
			}
			for key, e := range s.events {
				if !e.owed.IsZero() && trigger.matches(e.ev) && e.owed.After(seen.since) && seen.taken[key] == 0 {
					s.fail("trigger %s on %s %s %v, made %s, never took %q, owed since %s", trigger.ID, trigger.Source, trigger.Kind, trigger.Terminals(),
						seen.since.Format(time.RFC3339Nano), key, e.owed.Format(time.RFC3339Nano))
				}
			}
		}
	}
}

// answered reads the answers pushed into the threads, and holds that every
// reaction whose trigger carries its turn no longer left exactly one of them,
// a turn's answer or its interruption. A trigger that was removed is the
// user's own bound and answers for nothing.
func (s *stress) answered(w *stressWorld) {
	running := map[string]bool{}
	for _, owner := range s.owners {
		for _, trigger := range NewTriggerStore(owner, s.instanceDir(w, owner)).List() {
			if trigger.Turn != nil {
				running[trigger.Turn.MessageID] = true
			}
		}
	}
	now := map[string]int{}
	for _, owner := range s.owners {
		if c, ok := NewStore(w.dir).Load(owner); ok {
			for _, m := range c.Messages {
				if m.Auto {
					now[m.ID]++
				}
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, turn := range s.turns {
		got := now[id]
		switch {
		case got > 1:
			s.fail("the reaction %s of trigger %s landed in the thread %d times", id, turn.trigger, got)
		case running[id] || s.seen[turn.trigger] == nil:
		case got == 0:
			s.fail("the events of the reaction %s of trigger %s ended in no answer and no interruption in the thread", id, turn.trigger)
		}
	}
}

// keep moves what the last world left to where the next one finds it: the
// register names the files of its runs by their full path, which moved with
// the directory.
func (s *stress) keep(w *stressWorld, old string) {
	index, _ := RunPaths(w.dir)
	if raw, err := os.ReadFile(index); err == nil {
		if err := os.WriteFile(index, bytes.ReplaceAll(raw, []byte(old+"/"), []byte(w.dir+"/")), 0o600); err != nil {
			s.t.Fatalf("move the register: %v", err)
		}
	}
}

// noted holds that no end stands in a thread twice, the end of a compose run
// by its run and the end of an approval by the line it is read by, and then
// takes the notes and the reports of the jobs out of the threads so they stay
// small. It runs once a start is through: a note the last world wrote is
// still there when that start reports its end again. The answers of the
// reactions stay: an answer is pushed once because the thread still holds it.
//
// The reports of the jobs are counted on the way out, each on the close it
// names, and every close that reached the disk has been reported exactly once
// by now, counted over every look since: by the world that closed the job or
// by the start behind the kill that cut it short, and by none of them again
// after this took the report out of the thread. And the person heard of every
// report that stands in a thread: from the world that wrote it, or from the
// start behind the kill that came between the thread write and the ring.
func (s *stress) noted(w *stressWorld) {
	for _, owner := range s.owners {
		store := NewStore(w.dir)
		c, ok := store.Load(owner)
		if !ok || len(c.Messages) == 0 {
			continue
		}
		ends := map[string]int{}
		for _, m := range c.Messages {
			switch {
			case m.Note == nil:
			case m.Note.Source == NoteCompose:
				ends["the run "+m.Note.Run]++
			case m.Note.Source == NoteApproval:
				ends["the approval "+m.Note.Name]++
			case m.Note.Source == NoteCheck:
				s.mu.Lock()
				s.reports++
				// A check's report is the text it closed the job with, whole, and
				// the report of a job that ran out the one written about it, with
				// the reason in it: neither is the one line the job keeps.
				ranOut := m.Note.Verdict == string(VerdictExpired)
				switch closed := s.closeOf(m.Content); {
				case closed == nil || closed.owner != owner || closed.terminal != m.Note.Terminal:
					s.fail("the thread of %s holds a report no close of its jobs owed: %s on %s, %q", owner, m.Note.Verdict, m.Note.Terminal, m.Content)
				case !ranOut && m.Content != closed.text, ranOut && !(strings.HasPrefix(m.Content, "I stopped steering") && strings.Contains(m.Content, closed.text)):
					s.fail("the report of %s on %s is not the whole report: %q", m.Note.Verdict, m.Note.Terminal, m.Content)
				default:
					closed.reports++
				}
				if !s.reportHeard[m.ID] {
					s.fail("the report of %s on %s stands in the thread of %s and nobody heard of it", m.Note.Verdict, m.Note.Terminal, owner)
				}
				s.mu.Unlock()
			}
		}
		for end, n := range ends {
			if n > 1 {
				s.mu.Lock()
				s.fail("the end of %s stands in the thread of %s %d times", end, owner, n)
				s.mu.Unlock()
			}
		}
		c.Messages = slices.DeleteFunc(c.Messages, func(m Message) bool { return !m.Auto })
		store.Save(c)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for n, closed := range s.closes {
		switch {
		case closed.landed && closed.reports != 1:
			s.fail("the job on %s closed with the report %d, which landed in the thread of its owner %d times", closed.terminal, n, closed.reports)
		case !closed.landed && closed.reports != 0:
			s.fail("the close %d of the job on %s never reached the disk and was reported %d times", n, closed.terminal, closed.reports)
		}
	}
}

// down is what happens while no world runs: coders end their turns and drop
// their signals, a run ends, and on the forge a pipeline ends and somebody
// writes a note. It all stands in the records and on the forge the world
// that is about to start finds. both makes it one of each, whatever the dice
// say.
func (s *stress) down(w *stressWorld, both bool) {
	var shapes []string
	for range s.rng.Intn(3) * s.rng.Intn(3) {
		shapes = append(shapes, []string{stressSignal, stressSignal, stressRun, stressPipeline, stressReview}[s.rng.Intn(5)])
	}
	if both {
		shapes = append(shapes, stressSignal, stressRun, stressPipeline, stressReview)
	}
	for _, shape := range shapes {
		owner := s.owners[s.rng.Intn(len(s.owners))]
		key := s.event(s.rng, shape, owner, stressProjects[0])
		s.mu.Lock()
		e := s.events[key]
		s.mu.Unlock()
		if e.leave(w) {
			s.mu.Lock()
			s.left = append(s.left, key)
			s.count[e.ev.Source].down++
			s.mu.Unlock()
		}
	}
}

// stands reports whether the record of an event's source still holds it for
// a start to hand: the file of a signal lies in the inbox, the register has
// not finished a run or owes its report, the records of the forge service do
// not hold a pipeline's end or a note as handed on. A run the register does
// not know stands, there is nothing that says it was let go.
func (s *stress) stands(w *stressWorld, e *stressEvent, end func(project, id string, status forge.Status) bool, note func(project, branch, id string) bool) bool {
	switch {
	case e.forge != nil:
		return !e.forge.held(end, note)
	case e.ev.Source == EventCoder:
		_, err := os.Stat(filepath.Join(s.inbox(w), strings.TrimPrefix(e.ev.ID, "coder claude/")+".json"))
		return err == nil
	case e.ev.Source == EventCompose:
		var runs []docker.ComposeRecord
		if raw, err := os.ReadFile(filepath.Join(w.dir, "docker", "runs.json")); err == nil && json.Unmarshal(raw, &runs) == nil {
			for _, run := range runs {
				if run.ID == e.ev.Target {
					return !run.Finished || run.Owed
				}
			}
		}
	}
	return true
}

// letGo closes the books of the hand overs a kill cut, before a start reads
// the records. hand sees only that the process died, not where: a hand over
// that ran to its end let its event go before the death, and the record no
// longer holds it. Such an event was not left to this start. It is owed from
// before its hand over began, to every trigger that stood then, and to none
// that was made behind it: a source lets an event go only once the reactor
// holds it, and one that let it go earlier leaves it owed here and taken by
// nobody. What the record still holds is left to this start like everything
// else it finds there.
func (s *stress) letGo(w *stressWorld) {
	end, note := s.forge.handed(w.dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	left := s.left[:0]
	for _, key := range s.left {
		e := s.events[key]
		cut := e.cut
		e.cut = time.Time{}
		if cut.IsZero() || s.stands(w, e, end, note) {
			left = append(left, key)
			continue
		}
		if cut.After(e.owed) {
			e.owed = cut
		}
	}
	s.left = left
}

// sources is the part of a start that reads the records of the sources, in
// runServe's order: the runs that ended and the ends still owed, the
// approvals the last world left, the first round of the inbox poller and the
// first round of the forge poller.
func (s *stress) sources(w *stressWorld) {
	s.readRuns(w)
	w.approvals.Recover()
	s.drain(w)
	s.poll(w)
}

// handed closes the books of a start that came through: whatever stood in
// the record of a source when it began is owed from before it, whether that
// source handed it or not. And the sources hold nothing they still owe: the
// inbox is empty, no run owes its report, no approval of a world before this
// one stands in the register or ended without its owner hearing it to the
// end, and the forge shows nothing of the branches the working copies stand
// on that its service did not hand on and take into its records.
func (s *stress) handed(w *stressWorld, at time.Time) {
	owes := s.forge.owed(w.dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range s.left {
		e := s.events[key]
		e.owed = at
		s.count[e.ev.Source].left++
	}
	s.left = nil
	if owes != "" {
		s.fail("a start left %s out of the records of the forge", owes)
	}
	for key := range s.standing {
		s.fail("a start left %q on the forge and never handed it on", key)
	}
	clear(s.unsettled)
	if left, err := os.ReadDir(s.inbox(w)); err == nil {
		for _, entry := range left {
			if strings.HasSuffix(entry.Name(), ".json") {
				s.fail("a start left the signal %s in the inbox", entry.Name())
			}
		}
	}
	var runs []docker.ComposeRecord
	if raw, err := os.ReadFile(filepath.Join(w.dir, "docker", "runs.json")); err == nil && json.Unmarshal(raw, &runs) == nil {
		for _, run := range runs {
			if !run.Finished || run.Owed {
				s.fail("a start left the run %s unfinished or its report owed: %+v", run.ID, run)
			}
		}
	}
	var register map[string]approval.Approval
	if raw, err := os.ReadFile(filepath.Join(w.dir, "approvals.json")); err == nil && json.Unmarshal(raw, &register) == nil {
		for _, a := range register {
			s.fail("a start left the approval %q of the world before in the register", a.What)
		}
	}
	for what, ask := range s.asks {
		if ask.gen == w.gen {
			continue
		}
		switch e := s.events[ask.event]; {
		case !ask.sure:
		case !ask.ended:
			s.fail("the approval %q left the register and its owner never heard how it ended", what)
		case e != nil && e.owed.IsZero():
			s.fail("the end of the approval %q, %q, was never handed to its end", what, ask.event)
		}
		delete(s.asks, what)
	}
}

// quiet waits until no reaction of a world runs. A reaction holds a wake slot
// from before its turn to after the fire that follows it, and every turn asks
// the runner.
func (s *stress) quiet(w *stressWorld) {
	s.open()
	deadline := time.Now().Add(60 * time.Second)
	for !w.dead.Load() {
		asked := len(s.runner.turns())
		busy := s.slots(w) > 0
		w.svc.mu.Lock()
		busy = busy || len(w.svc.running) > 0
		w.svc.mu.Unlock()
		// The files are read only once nothing is seen running: reading them
		// is what this wait costs.
		if !busy {
			for _, trigger := range w.reactor.triggers.All() {
				busy = busy || trigger.Reacting()
			}
		}
		if !busy && s.slots(w) == 0 && len(s.runner.turns()) == asked {
			return
		}
		if time.Now().After(deadline) {
			s.mu.Lock()
			s.fail("the reactions never came to rest")
			s.mu.Unlock()
			return
		}
		time.Sleep(200 * time.Microsecond)
	}
}

func (s *stress) slots(w *stressWorld) int {
	w.svc.slots.mu.Lock()
	defer w.svc.slots.mu.Unlock()
	return w.svc.slots.taken
}

func (s *stress) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.takes
}

// start is the startup of the next process over what the last one left, in
// the order runServe has it: the sweep of the jobs whose terminal is gone,
// the turns, the watcher, the reactor, and then the sources that keep a
// record, which hand what stands in it: what a kill left there and what came
// to stand there while no world ran, see down. The watcher writes the reports
// the last world closed a job with and died over, and the ends of those are
// all a recovery publishes. Now and then it dies in the middle and the one
// after it starts over. Then the invariants are held,
// every source hands again some of what it handed last, the invariants are
// held once more, and a second recovery has to hand out nothing, the sources
// read once more included.
func (s *stress) start() {
	for {
		old := s.world.dir
		s.gen++
		w := s.boot()
		s.world = w
		s.observe(w)
		s.book(w)
		s.keep(w, old)
		s.letGo(w)
		s.down(w, s.bothDown)
		s.bothDown = false
		for _, owner := range s.owners {
			for _, trigger := range NewTriggerStore(owner, s.instanceDir(w, owner)).List() {
				if trigger.Turn != nil && !trigger.Turn.Started {
					s.waited++
				}
			}
		}
		switch s.rng.Intn(8) {
		case 0:
			w.fuse.Store(1 + s.rng.Int63n(40))
		case 1:
			w.turnFuse.Store(1 + s.rng.Int63n(3))
		case 2:
			w.srcFuse.Store(1 + s.rng.Int63n(4))
		case 3:
			w.ringFuse.Store(1)
		}
		s.mu.Lock()
		keep := map[string]bool{}
		for terminal := range s.alive {
			keep[terminal] = true
		}
		s.mu.Unlock()
		owed := s.owing(w)
		w.reactor.PruneTerminals(keep)
		adopted := w.svc.Recover()
		w.watcher.Recover(adopted)
		w.reactor.Recover()
		if got := w.published.Load(); got != int64(owed) && !w.dead.Load() {
			s.mu.Lock()
			s.fail("a recovery published %d events, and %d jobs owed their report", got, owed)
			s.mu.Unlock()
		}
		at := s.tick()
		s.sources(w)
		if w.dead.Load() {
			s.crashes++
			continue
		}
		w.fuse.Store(0)
		w.turnFuse.Store(0)
		w.srcFuse.Store(0)
		w.ringFuse.Store(0)
		s.quiet(w)
		if w.dead.Load() {
			s.crashes++
			continue
		}
		s.observe(w)
		s.book(w)
		s.handed(w, at)
		s.check(w)
		s.answered(w)
		s.noted(w)
		s.again(w, s.rng, 1+s.rng.Intn(4))
		s.quiet(w)
		s.observe(w)
		s.check(w)
		before, published := s.total(), w.published.Load()
		w.watcher.Recover(nil)
		w.reactor.Recover()
		s.sources(w)
		s.quiet(w)
		s.observe(w)
		if got := s.total(); got != before || w.published.Load() != published {
			s.mu.Lock()
			s.fail("a second recovery handed out %d events and its sources handed %d", got-before, w.published.Load()-published)
			s.mu.Unlock()
		}
		s.recovered++
		s.hold()
		return
	}
}

// releaseBefore writes the files of every assistant the way the releases
// before wrote them: jobs without closedAt, triggers with a watermark and
// neither since nor ends, one of them with the single target of older files.
// The watermark stands somewhere among the closes, so that release delivered
// the ends before it and missed the ones after. And of the other sources a
// window that stopped at what one turn reads, its events without a name, and
// a schedule whose tick fell due while nothing ran. The registers of the
// sources are that release's too: a run it finished and reported, which
// carries no word about owing anything and is reported to nobody again, and
// an approval it left waiting, which the first start declines. And so are
// the records of the forge, which that release wrote the same way: of every
// project the pipeline it read running on the branch its working copy stands
// on, and the notes it read on the change request of every branch. That
// pipeline ended and somebody wrote a note while nothing ran, which the
// first start hands on.
func (s *stress) releaseBefore(w *stressWorld) {
	base := time.Unix(0, s.clock.Load()).UTC().Add(-3 * time.Hour)
	stamp := func(minute int) string { return base.Add(time.Duration(minute) * time.Minute).Format(time.RFC3339Nano) }
	states := []JobState{JobDone, JobDone, JobBlocked, JobExpired}
	kinds := []string{string(VerdictDone), string(VerdictBlocked), string(VerdictExpired), JobKindClosed}
	for _, owner := range s.owners {
		var jobs, triggers []map[string]any
		var closed []string
		for i := range 5 + s.rng.Intn(6) {
			terminal := s.newTerminal(owner)
			job := map[string]any{
				"terminal": terminal, "name": terminal, "project": "demo", "state": JobSteering,
				"createdAt": stamp(i), "updatedAt": stamp(i),
			}
			if s.rng.Intn(4) > 0 {
				at := 10 + 10*i
				job["state"] = states[s.rng.Intn(len(states))]
				job["note"] = "the report of " + terminal
				job["noteAt"] = stamp(at)
				job["updatedAt"] = stamp(at + s.rng.Intn(3)*7)
				closed = append(closed, terminal)
				verdict, _ := jobEndVerdict(job["state"].(JobState))
				s.bookEnd(string(verdict), terminal, owner, base.Add(time.Duration(at)*time.Minute))
			}
			jobs = append(jobs, job)
		}
		for i := range 4 + s.rng.Intn(4) {
			seen := s.rng.Intn(130)
			id := statefile.NewID()
			trigger := map[string]any{
				"id": id, "source": EventJob, "kind": kinds[s.rng.Intn(len(kinds))], "task": "NOTE",
				"batchSeconds": []int{0, 3, 30}[s.rng.Intn(3)], "state": TriggerStanding, "fired": s.rng.Intn(5),
				"createdAt": stamp(0), "updatedAt": stamp(seen), "seenAt": stamp(seen),
			}
			switch {
			case i == 0 && len(closed) > 0:
				trigger["target"] = closed[s.rng.Intn(len(closed))]
			case i == 1 && len(closed) > 1:
				trigger["kind"] = JobKindClosed
				trigger["all"] = true
				trigger["targets"] = []map[string]any{{"terminal": closed[0]}, {"terminal": closed[1]}}
			}
			triggers = append(triggers, trigger)
			s.seen[id] = newStressSeen(owner, base.Add(time.Duration(seen)*time.Minute), trigger["fired"].(int))
			s.seen[id].legacy = true
		}
		var window []map[string]any
		for range 1 + s.rng.Intn(maxTriggerPendingNotes) {
			s.serial++
			window = append(window, map[string]any{
				"source": EventCoder, "kind": CoderKindEnded, "target": stressCoders[0], "time": stamp(140),
				"headline": fmt.Sprintf("Coder ended its turn: before the update %d", s.serial),
			})
		}
		triggers = append(triggers,
			map[string]any{
				"id": statefile.NewID(), "source": EventCoder, "kind": CoderKindNews, "task": "NOTE", "batchSeconds": 30,
				"state": TriggerStanding, "createdAt": stamp(0), "updatedAt": stamp(140), "seenAt": stamp(140),
				"pending": window, "batchDue": stamp(141),
			},
			map[string]any{
				"id": statefile.NewID(), "source": EventCron, "kind": CronKindTick, "task": "NOTE", "batchSeconds": 0,
				"spec": "* * * * *", "timezone": "UTC", "state": TriggerStanding,
				"createdAt": stamp(0), "updatedAt": stamp(0), "seenAt": stamp(0), "nextAt": stamp(150),
			},
			map[string]any{
				"id": statefile.NewID(), "source": EventCompose, "kind": ComposeKindEnded, "task": "NOTE", "batchSeconds": 30,
				"state": TriggerStanding, "createdAt": stamp(0), "updatedAt": stamp(0), "seenAt": stamp(0),
			})
		dir := s.instanceDir(w, owner)
		for name, list := range map[string]any{jobsFileName: jobs, triggersFileName: triggers} {
			raw, err := json.Marshal(list)
			if err != nil {
				s.t.Fatalf("marshal %s: %v", name, err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
				s.t.Fatalf("write %s: %v", name, err)
			}
		}
	}
	s.serial++
	what := fmt.Sprintf("Compose up on demo, before the update %d", s.serial)
	registers := map[string]any{
		"approvals.json": map[string]any{"0a1b2c3d4e5f6071": map[string]any{
			"id": "0a1b2c3d4e5f6071", "owner": s.owners[0], "kind": "compose-actions", "key": "demo\nup", "what": what, "createdAt": stamp(140),
		}},
		filepath.Join("docker", "runs.json"): []map[string]any{{
			"id": fmt.Sprintf("run-before-%d", s.serial), "dir": "/srv/demo", "label": "demo", "action": "Compose up", "argv": []string{"docker", "compose", "up", "-d"},
			"timeout": 120000000000, "owner": s.owners[0], "pid": 4711, "startedAt": stamp(100), "finished": true, "endedAt": stamp(101), "exited": true,
		}},
	}
	read := time.Now().UTC().Format(time.RFC3339Nano)
	host, ends, notes := s.forge.host().ID, map[string]any{}, map[string]any{}
	for _, project := range stressProjects {
		s.forge.checkout(project, "main")
		var records []map[string]any
		for _, branch := range []string{"feature", "main"} {
			s.serial++
			id := fmt.Sprintf("%d-before", s.serial)
			s.forge.note(project, branch, id, "author-0")
			records = append(records, map[string]any{"host": host, "path": "team/" + project, "change": fakeForgeChanges[branch], "notes": []string{id}, "at": read})
		}
		notes[project] = records
		for _, shape := range []string{stressPipeline, stressReview} {
			key := s.event(s.rng, shape, s.owners[0], project)
			e := s.events[key]
			if shape == stressPipeline {
				ends[project] = map[string]any{"branch": "main", "host": host, "path": "team/" + project, "pipeline": "#" + e.forge.id, "status": forge.Running, "at": read}
			}
			if !e.leave(w) {
				s.t.Fatalf("the forge of the release before did not take %q", key)
			}
			s.left = append(s.left, key)
			s.count[EventForge].down++
		}
	}
	registers[filepath.Join("forge", "pipelines.json")] = map[string]any{"projects": ends}
	registers[filepath.Join("forge", "notes.json")] = map[string]any{"projects": notes}
	for name, content := range registers {
		raw, err := json.Marshal(content)
		if err != nil {
			s.t.Fatalf("marshal %s: %v", name, err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(w.dir, name)), 0o700); err != nil {
			s.t.Fatalf("the directory of %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(w.dir, name), raw, 0o600); err != nil {
			s.t.Fatalf("write %s: %v", name, err)
		}
	}
}

func (s *stress) newTerminal(owner string) string {
	s.terminal++
	terminal := fmt.Sprintf("term-%d", s.terminal)
	s.alive[terminal] = owner
	return terminal
}

// step moves the job of one terminal on by one thing that happens to a job.
func (s *stress) step(w *stressWorld, rng *rand.Rand, owner, terminal string) {
	job, ok := w.watcher.Get(terminal)
	steer := func() {
		w.watcher.Steer(Job{Owner: owner, Terminal: terminal, Name: terminal, Project: "demo"})
	}
	roll := rng.Intn(100)
	switch {
	case !ok:
		steer()
	case job.State.Open():
		seen := checkContext{JobCreatedAt: job.CreatedAt}
		switch {
		case roll < 45:
			w.watcher.report(job, seen, wakeOutcome{Verdict: VerdictDone, Text: s.closing(owner, terminal, "the whole report of "+terminal+",\nwhich is more than one line")})
		case roll < 70:
			w.watcher.report(job, seen, wakeOutcome{Verdict: VerdictBlocked, Text: s.closing(owner, terminal, "the whole report of "+terminal+",\nwhich is more than one line")})
		case roll < 90:
			w.watcher.expire(job, s.closing(owner, terminal, "the time it was given is over"))
		case roll < 95:
			w.watcher.NoteAssistantInput(terminal, owner)
		default:
			steer()
		}
		s.bookClose(w, terminal)
	case roll < 60:
		steer()
	case roll < 75:
		// A check that was running when the job closed: its spinner stands,
		// and either the killed check clears it or the next start does.
		job = w.watcher.mark(job, func(fresh *Job) { fresh.CheckingSince = s.tick() })
		if rng.Intn(2) == 0 && job.Terminal != "" {
			w.watcher.conclude(job, checkContext{JobCreatedAt: job.CreatedAt}, wakeOutcome{}, errors.New("killed"))
		}
	case roll < 90:
		// Only a blocked job is taken up again by it.
		w.watcher.NoteAssistantInput(terminal, owner)
	default:
		s.mu.Lock()
		delete(s.alive, terminal)
		s.mu.Unlock()
		w.watcher.TerminalDeleted(terminal)
	}
}

// add makes a trigger of a random shape: every source and kind, with and
// without a batch window, one shots, expiring ones, targets and barriers. An
// assistant that holds enough of them loses its oldest instead, a spent one
// first.
func (s *stress) add(w *stressWorld, rng *rand.Rand, owner string, terminals []string) {
	if listed := w.reactor.ListOf(owner); len(listed) >= stressTriggers {
		w.reactor.Remove(listed[len(listed)-1].ID, owner)
		return
	}
	spec := TriggerSpec{
		Owner: owner, Task: "NOTE", BatchSet: true,
		Batch: []time.Duration{0, 0, 2 * time.Second, 5 * time.Second, DefaultTriggerBatch}[rng.Intn(5)],
		Once:  rng.Intn(7) == 0,
	}
	if rng.Intn(10) == 0 {
		spec.Until = 20 * time.Second
	}
	switch roll := rng.Intn(100); {
	case roll < 45:
		spec.Event = []string{"job-done", "job-blocked", "job-expired", "job-closed", "job-closed"}[rng.Intn(5)]
	case roll < 65:
		spec.Event, terminals = "coder-news", slices.Clone(stressCoders)
	case roll < 77:
		spec.Event, terminals = []string{"compose-done", "compose-failed", "compose-declined", "compose-ended"}[rng.Intn(4)], nil
	case roll < 92:
		spec.Event, terminals = []string{"forge-pipeline-succeeded", "forge-pipeline-failed", "forge-pipeline-ended", "forge-review-comment"}[rng.Intn(4)], nil
		spec.Project = stressProjects[rng.Intn(len(stressProjects))]
		if rng.Intn(3) == 0 {
			spec.Branch, spec.BranchSet = "main", true
		}
	default:
		spec.Event, terminals = "cron", nil
		spec.Spec, spec.Timezone = []string{"* * * * *", "*/2 * * * *"}[rng.Intn(2)], "UTC"
	}
	if len(terminals) > 0 && rng.Intn(3) == 0 {
		rng.Shuffle(len(terminals), func(i, j int) { terminals[i], terminals[j] = terminals[j], terminals[i] })
		spec.Targets = targets(terminals[:min(len(terminals), 1+rng.Intn(3))]...)
		spec.All = rng.Intn(2) == 0
	}
	w.reactor.Add(spec)
}

// flood hands more events of one shape than a window holds whole, some of
// them twice, and now and then more than its fold has lines for.
func (s *stress) flood(w *stressWorld, rng *rand.Rand, shape, owner string) {
	project := stressProjects[rng.Intn(len(stressProjects))]
	for range maxTriggerWindowNotes + []int{3, 3, 2 * maxTriggerPendingNotes}[rng.Intn(3)] + rng.Intn(8) {
		if shape == stressApproval {
			s.ask(w, rng, owner, 0)
			continue
		}
		key := s.event(rng, shape, owner, project)
		s.hand(w, key)
		if rng.Intn(6) == 0 {
			s.hand(w, key)
		}
	}
}

// play is one round: a handful of things at once, each on a terminal or a
// trigger of its own, and maybe a kill somewhere among them.
func (s *stress) play() {
	w := s.world
	s.clock.Add(int64(time.Duration(s.rng.Intn(1500)) * time.Millisecond))
	s.mu.Lock()
	byOwner := map[string][]string{}
	for terminal, owner := range s.alive {
		byOwner[owner] = append(byOwner[owner], terminal)
	}
	s.mu.Unlock()
	for _, terminals := range byOwner {
		slices.Sort(terminals)
	}

	// run plays one thing beside the others, with dice of its own drawn from
	// the run's, which only the round itself touches. beside is for what such
	// a thing starts next to itself.
	var wg sync.WaitGroup
	beside := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}
	run := func(fn func(rng *rand.Rand)) {
		rng := rand.New(rand.NewSource(s.rng.Int63()))
		beside(func() { fn(rng) })
	}
	switch s.rng.Intn(20) {
	case 0, 1, 2, 3:
		at := time.Duration(s.rng.Intn(3000)) * time.Microsecond
		run(func(*rand.Rand) {
			time.Sleep(at)
			s.kill(w)
		})
	case 4, 5, 6, 7:
		w.fuse.Store(1 + s.rng.Int63n(40))
	case 8, 9, 10:
		w.pubFuse.Store(1 + s.rng.Int63n(3))
	case 11, 12:
		w.turnFuse.Store(1 + s.rng.Int63n(3))
	case 13, 14:
		w.srcFuse.Store(1 + s.rng.Int63n(6))
	case 15:
		w.ringFuse.Store(1 + s.rng.Int63n(3))
	}
	s.runner.mu.Lock()
	s.runner.commandErr = errors.New("the coder refuses to start")
	if s.rng.Intn(5) == 0 {
		s.runner.commandErr = nil
	}
	s.runner.mu.Unlock()

	used := map[string]bool{}
	for range 2 + s.rng.Intn(6) {
		owner := s.owners[s.rng.Intn(len(s.owners))]
		terminals := byOwner[owner]
		switch roll := s.rng.Intn(100); {
		case roll < 38:
			terminal := ""
			if len(terminals) > 0 && (len(terminals) >= 8 || s.rng.Intn(6) > 0) {
				terminal = terminals[s.rng.Intn(len(terminals))]
			} else {
				s.mu.Lock()
				terminal = s.newTerminal(owner)
				s.mu.Unlock()
			}
			if used[terminal] {
				continue
			}
			used[terminal] = true
			steps := 1 + s.rng.Intn(3)
			run(func(rng *rand.Rand) {
				for range steps {
					s.step(w, rng, owner, terminal)
				}
			})
		case roll < 41:
			// Many jobs close at once, more than one turn reads, and now and
			// then more than a window holds whole.
			var burst []string
			s.mu.Lock()
			for range []int{maxTriggerPendingNotes, maxTriggerWindowNotes}[s.rng.Intn(2)] + 2 + s.rng.Intn(8) {
				burst = append(burst, s.newTerminal(owner))
			}
			s.mu.Unlock()
			for _, terminal := range burst {
				stays := s.rng.Intn(4) == 0
				run(func(rng *rand.Rand) {
					w.watcher.Steer(Job{Owner: owner, Terminal: terminal, Name: terminal, Project: "demo"})
					job, _ := w.watcher.Get(terminal)
					verdict := VerdictDone
					if rng.Intn(3) == 0 {
						verdict = VerdictBlocked
					}
					w.watcher.report(job, checkContext{JobCreatedAt: job.CreatedAt}, wakeOutcome{Verdict: verdict, Text: s.closing(owner, terminal, "the whole report of "+terminal+",\nwhich is more than one line")})
					s.bookClose(w, terminal)
					if !stays {
						s.mu.Lock()
						delete(s.alive, terminal)
						s.mu.Unlock()
					}
				})
			}
		case roll < 57:
			// A source hands a few events over, now and then one of them
			// twice, in a row or from two sides at once.
			source := []string{EventCoder, EventCoder, EventCompose, stressApproval, EventForge, EventForge}[s.rng.Intn(6)]
			run(func(rng *rand.Rand) {
				for range 1 + rng.Intn(3) {
					if source == stressApproval {
						s.ask(w, rng, owner, -1)
						continue
					}
					key := s.source(rng, source, owner)
					switch rng.Intn(8) {
					case 0:
						s.hand(w, key)
						s.hand(w, key)
					case 1:
						beside(func() { s.hand(w, key) })
						s.hand(w, key)
					default:
						s.hand(w, key)
					}
				}
			})
		case roll < 60:
			shape := []string{stressSignal, stressRun, stressApproval, stressPipeline, stressReview, stressReview}[s.rng.Intn(6)]
			run(func(rng *rand.Rand) { s.flood(w, rng, shape, owner) })
		case roll < 62:
			some := 1 + s.rng.Intn(3)
			run(func(rng *rand.Rand) { s.again(w, rng, some) })
		case roll < 64:
			project := stressProjects[s.rng.Intn(len(stressProjects))]
			run(func(*rand.Rand) { s.switchBranch(w, project) })
		case roll < 77:
			run(func(rng *rand.Rand) { s.add(w, rng, owner, slices.Clone(terminals)) })
		case roll < 82:
			run(func(rng *rand.Rand) {
				if listed := w.reactor.ListOf(owner); len(listed) > 0 {
					trigger := listed[rng.Intn(len(listed))]
					if rng.Intn(2) == 0 {
						w.reactor.Remove(trigger.ID, owner)
					} else {
						w.reactor.Edit(trigger.ID, owner, TriggerSpec{Task: "NOTE " + strconv.Itoa(rng.Intn(1000))})
					}
				}
			})
		case roll < 90:
			// The clock moves on, a few seconds for the batch windows, or a
			// minute and more for the schedules.
			ahead := time.Duration(1+s.rng.Intn(4)) * time.Second
			if s.rng.Intn(3) == 0 {
				ahead = time.Duration(20+s.rng.Intn(100)) * time.Second
			}
			run(func(*rand.Rand) {
				s.clock.Add(int64(ahead))
				w.reactor.Tick()
			})
		case roll < 96:
			tokens := 1 + s.rng.Intn(8)
			run(func(*rand.Rand) { s.free(tokens) })
		default:
			// A recovery that runs while reports are still on their way.
			run(func(*rand.Rand) { w.reactor.Recover() })
		}
	}
	wg.Wait()
	w.fuse.Store(0)
	w.pubFuse.Store(0)
	w.turnFuse.Store(0)
	w.srcFuse.Store(0)
	w.ringFuse.Store(0)
	switch {
	case w.dead.Load():
		s.crashes++
		s.start()
	case s.rng.Intn(5) == 0:
		// A restart that was asked for: everything comes to rest first.
		s.quiet(w)
		s.kill(w)
		s.start()
	default:
		s.observe(w)
	}
}

// epoch is one state from its first start on files of the releases before to
// the last check after a clean stop.
func (s *stress) epoch(rounds int) {
	s.gen++
	s.raw, s.seen, s.alive = map[string][]byte{}, map[string]*stressSeen{}, map[string]string{}
	s.events, s.digests, s.recent, s.owners = map[stressKey]*stressEvent{}, map[string]stressKey{}, nil, nil
	s.left, s.drops, s.hooks, s.rings, s.asks = nil, map[string]int{}, map[string]int{}, map[string]int{}, map[string]*stressAsk{}
	s.closes, s.reportRings, s.reportHeard = map[int]*stressClose{}, map[string]int{}, map[string]bool{}
	s.forge, s.standing, s.ends, s.unsettled = newFakeForge(), map[stressKey]bool{}, map[string]stressKey{}, map[string]bool{}
	s.turns, s.runs = map[string]stressTurn{}, map[string]string{}
	over := make(chan struct{})
	s.runner = &fakeRunner{dir: s.t.TempDir(), over: over, hold: s.holdTurn, events: []Event{{Kind: EventDelta, Text: "an answer"}}}
	defer s.runner.end(over)
	s.hold()
	w := s.boot()
	s.world = w
	for range stressOwners {
		owner, err := w.svc.create("claude")
		if err != nil {
			s.t.Fatalf("create an assistant: %v", err)
		}
		s.owners = append(s.owners, owner.ID)
	}
	s.releaseBefore(w)
	s.kill(w)
	s.start()

	// A window that takes more than it holds whole, of every source, in
	// every run. None of these triggers has a batch window: the first event
	// buys a turn, which is held open, and everything after it waits. Every
	// fifth event of a source that hands is handed twice. The one shots
	// beside them have one, which closes behind the last event.
	w = s.world
	const past = maxTriggerWindowNotes + 4
	w.reactor.Add(TriggerSpec{Owner: s.owners[0], Event: "cron", Spec: "* * * * *", Timezone: "UTC", Task: "NOTE"})
	for range past {
		s.clock.Add(int64(time.Minute))
		w.reactor.Tick()
	}
	w.reactor.Add(TriggerSpec{Owner: s.owners[1], Event: "job-closed", Task: "NOTE", BatchSet: true})
	// Beside it a trigger that is told to wait for two of those terminals
	// only once it took their ends, see barred.
	late, _ := w.reactor.Add(TriggerSpec{Owner: s.owners[1], Event: "job-closed", Task: "NOTE", BatchSet: true})
	var closed []string
	for range past {
		terminal := s.newTerminal(s.owners[1])
		w.watcher.Steer(Job{Owner: s.owners[1], Terminal: terminal, Name: terminal, Project: "demo"})
		job, _ := w.watcher.Get(terminal)
		w.watcher.report(job, checkContext{JobCreatedAt: job.CreatedAt}, wakeOutcome{Verdict: VerdictDone, Text: s.closing(s.owners[1], terminal, "the whole report of "+terminal+",\nwhich is more than one line")})
		delete(s.alive, terminal)
		closed = append(closed, terminal)
	}
	s.barred(w, late.ID, func() {}, closed[1], closed[past-1])
	for i, shape := range []string{stressSignal, stressRun, stressPipeline, stressReview} {
		owner := s.owners[(i+2)%len(s.owners)]
		spec := TriggerSpec{Owner: owner, Task: "NOTE", BatchSet: true}
		spec.Event = []string{"coder-news", "compose-ended", "forge-pipeline-ended", "forge-review-comment"}[i]
		if i > 1 {
			spec.Project = stressProjects[0]
		}
		if _, err := w.reactor.Add(spec); err != nil {
			s.t.Fatalf("a trigger on %s: %v", spec.Event, err)
		}
		// A second one on the coders is told to wait for two of them only
		// once it took their signals, see below.
		var late Trigger
		if shape == stressSignal {
			late, _ = w.reactor.Add(spec)
		}
		// Beside it a one shot whose batch window takes all of them, more
		// than its one turn reads whole and more than a window holds whole.
		spec.Once, spec.Batch = true, stressLastBatch
		if _, err := w.reactor.Add(spec); err != nil {
			s.t.Fatalf("a one shot on %s: %v", spec.Event, err)
		}
		var handed []stressKey
		for n := range past {
			key := s.event(s.rng, shape, owner, stressProjects[0])
			s.hand(w, key)
			if n%5 == 0 {
				s.hand(w, key)
			}
			handed = append(handed, key)
		}
		if shape != stressSignal {
			continue
		}
		// The terminals it is told to wait for are those of the last signal,
		// which the fold counts, and of one from another coder that waits
		// whole in its window. Their source hands both again.
		whole, folded := handed[1], handed[past-1]
		for _, key := range handed[1 : 1+maxTriggerWindowNotes] {
			if s.events[key].ev.Target != s.events[folded].ev.Target {
				whole = key
				break
			}
		}
		s.barred(w, late.ID, func() {
			s.hand(w, whole)
			s.hand(w, folded)
		}, s.events[whole].ev.Target, s.events[folded].ev.Target)
	}
	s.clock.Add(int64(stressLastBatch))
	w.reactor.Tick()

	// A working copy changes its branch and comes back in every run, with a
	// pipeline that ends and a note on each side of it: what the forge
	// service handed on before is not handed on again by the branch coming
	// back.
	for range 2 {
		s.switchBranch(w, stressProjects[0])
		for _, shape := range []string{stressPipeline, stressReview} {
			s.hand(w, s.event(s.rng, shape, s.owners[0], stressProjects[0]))
		}
	}

	// And every source that keeps a record is cut short once in every run,
	// at the moment the reactor hears of its event, before any trigger took
	// it: the kill leaves the event in the record, and the start after has to
	// hand it. That start also finds what came while no world ran. The last
	// two are approvals the user approves, cut short in the middle of their
	// run: one before the run started, whose end says that nobody knows how
	// far it got, and one behind the start of a run that went through, whose
	// only end is the run's own.
	for _, shape := range []string{stressSignal, stressRun, stressPipeline, stressReview, stressApproval, "approved " + stressRun, "started " + stressRun} {
		w = s.world
		switch w.pubFuse.Store(1); shape {
		case stressApproval:
			s.ask(w, s.rng, s.owners[0], 0)
		case "approved " + stressRun:
			w.pubFuse.Store(0)
			w.srcFuse.Store(1)
			s.ask(w, s.rng, s.owners[0], 30)
		case "started " + stressRun:
			// The run of the approval, the reactor hearing of that run's end,
			// and the moment behind the start: the process dies at the third.
			w.pubFuse.Store(0)
			w.srcFuse.Store(3)
			before := s.resumed
			s.ask(w, s.rng, s.owners[0], 45)
			defer func() {
				if !s.failed() && s.resumed == before {
					s.t.Fatalf("the kill behind the start of an approved run left no approval to ask the register of runs about")
				}
			}()
		default:
			s.hand(w, s.event(s.rng, shape, s.owners[0], stressProjects[0]))
		}
		for deadline := time.Now().Add(5 * time.Second); !w.dead.Load(); time.Sleep(100 * time.Microsecond) {
			if time.Now().After(deadline) {
				s.t.Fatalf("the hand over of a %s never reached the point it was to die at", shape)
			}
		}
		s.crashes++
		s.bothDown = true
		s.start()
	}

	// And the close of a job is cut short in every run, of a job a check
	// closed and of one that ran out: between the close and the report, where
	// the job carries the report and the thread holds none, between the thread
	// write and the ring, where the thread holds it and nobody heard of it,
	// and behind the report, at the first trigger that takes the end, where
	// the thread holds it and the job has not let it go. The start after each
	// has to leave the report in the thread once, rung for once.
	for _, cut := range []string{"a report", "an expiry", "a report nobody heard of", "an expiry nobody heard of", "a report that landed", "an expiry that landed"} {
		w = s.world
		owner := s.owners[1]
		terminal := s.newTerminal(owner)
		w.watcher.Steer(Job{Owner: owner, Terminal: terminal, Name: terminal, Project: "demo"})
		job, _ := w.watcher.Get(terminal)
		switch {
		case strings.HasSuffix(cut, "that landed"):
			w.fuse.Store(1)
		case strings.HasSuffix(cut, "nobody heard of"):
			w.ringFuse.Store(1)
		default:
			w.turnFuse.Store(1)
		}
		if strings.HasPrefix(cut, "an expiry") {
			w.watcher.expire(job, s.closing(owner, terminal, "the time it was given is over"))
		} else {
			w.watcher.report(job, checkContext{JobCreatedAt: job.CreatedAt}, wakeOutcome{Verdict: VerdictDone, Text: s.closing(owner, terminal, "the whole report of "+terminal+",\nwhich is more than one line")})
		}
		if !w.dead.Load() {
			s.t.Fatalf("the close of a job never reached the point %s was to die at", cut)
		}
		s.crashes++
		before := s.reportsCut + s.reportsTold
		s.start()
		if !s.failed() && s.reportsCut+s.reportsTold == before {
			s.t.Fatalf("the kill over %s left no job carrying its report", cut)
		}
	}

	for range rounds {
		s.round++
		s.play()
		if s.failed() {
			break
		}
	}
	if !s.failed() {
		s.quiet(s.world)
		s.kill(s.world)
		s.start()
	}
	s.quiet(s.world)
	s.kill(s.world)
	s.open()
}

// TestEventsUnderStress holds, after every kill and at the end, for the ends
// of jobs, coders' signals, the ends of compose runs and approvals, pipeline
// ends, review notes and the ticks of schedules alike: every event reached
// every standing trigger that matches it and stood when it was handed, no
// trigger took one event twice however often its source handed it, none took
// an event it does not match or one the release before had delivered, a
// schedule never stands past a tick nobody took, a recovery published nothing
// and a second one handed out nothing, and what a trigger keeps stays inside
// its bounds: the window, the fold, the account of the names, and the ends
// only of jobs the store still holds. And for the reactions: every event a
// trigger took is in its window, whole or counted by the fold, or in one turn
// and in no second one, a one shot that fired left none of them behind its
// one turn, no reaction is registered as two turns, and every one leaves
// exactly one answer in the thread, a turn's or an interruption. And for the
// reports of the jobs: every close that reached the disk left exactly one
// report in the thread of its owner, also the one whose process died between
// the close and the report or behind the report, no thread holds a report no
// close owed, and a recovery publishes the ends of the reports it writes and
// no other. The person heard of every report in a thread exactly once, also
// of the one whose process died between the thread write and the ring. And
// for a barrier: one named behind events that wait in its window, whole or
// counted by its fold, misses none of their terminals once they are handed
// again, and at no quiet moment does a barrier miss a terminal whose event
// waits in its window.
//
// And for the sources that keep a record, the inbox of the coders' signals,
// the register of compose runs, the register of approvals and the forge
// service with its records of what it handed on: what stood in a record, or
// on the forge, when a start began is handed by that start, whatever killed
// the world before and wherever, and what came to stand there while no world
// ran too. A start that came through leaves nothing owed in any of them. A
// signal reaches the person and the hook listener no more often than it was
// dropped, the end of a run and of an approval stands in its thread once,
// and an approval ends one way, also when its end is told a second time, and
// one whose run started ends as approved and publishes nothing, whatever
// killed the process that approved it: the end of that run is its own. The
// forge service hands nothing on that its records hold, takes a note into
// them before it hands on the next, tells of a pipeline's end once they hold
// it, hands on no note of the token's own account, and loses nothing to a
// working copy that changes its branch or to a forge that fails a read.
func TestEventsUnderStress(t *testing.T) {
	seed, rounds := time.Now().UnixNano(), stressRounds
	if raw := os.Getenv(stressSeedEnv); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			t.Fatalf("%s: %v", stressSeedEnv, err)
		}
		seed = parsed
	}
	if raw := os.Getenv(stressRoundsEnv); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			t.Fatalf("%s wants a number of rounds, got %q", stressRoundsEnv, raw)
		}
		rounds = parsed
	}
	out := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(out)

	s := &stress{t: t, seed: seed, rng: rand.New(rand.NewSource(seed)), root: t.TempDir(), count: map[string]*stressCount{}}
	for _, source := range stressSources {
		s.count[source] = &stressCount{}
	}
	s.clock.Store(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC).UnixNano())
	started := time.Now()
	for left := rounds; left > 0 && !s.failed(); left -= stressEpoch {
		s.epoch(min(left, stressEpoch))
		if err := os.RemoveAll(s.root); err != nil {
			t.Fatalf("clear the state: %v", err)
		}
	}
	summary := fmt.Sprintf("seed %d, %d rounds in %s: %d kills, %d starts checked, widest window %d, %d reactions, %d of them waiting for a slot at a kill, %d of them a one shot's with more than a turn reads whole, %d approvals a restart declined, %d ends of approvals told again, %d branches changed, %d reads of a failing forge, %d reports of jobs, %d of them cut short between the close and the thread, %d behind the thread before the job let go, %d kills before the ring of a report, %d barriers named behind their window, %d approved runs a kill cut behind their start",
		seed, s.round, time.Since(started).Round(time.Millisecond), s.crashes, s.recovered, s.maxWindow, s.reactions, s.waited, s.lastTurns, s.lost, s.retold, s.switches, s.failedReads, s.reports, s.reportsCut, s.reportsTold, s.reportsUnrung, s.barriers, s.resumed)
	stressed := s.recovered > 0 && s.maxWindow == maxTriggerWindowNotes && s.lastTurns > 0
	for _, source := range stressSources {
		c := s.count[source]
		summary += fmt.Sprintf("\n%-7s %d events, %d handed again, %d taken, %d of them folded, %d turns, %d taken again past the account's bound, %d handed by a start out of the source's record, %d of them had happened while no world ran",
			source, c.events, c.again, c.taken, c.folded, c.turns, c.forgotten, c.left, c.down)
		// A job's end and a tick are handed by nobody, so nobody hands one
		// again.
		stressed = stressed && c.events > 0 && c.taken > 0 && c.folded > 0 && c.turns > 0 && (c.again > 0 || source == EventJob || source == EventCron)
		// A coder's signal and the end of a compose run stand in a record and
		// an event of a forge on the forge, so a kill leaves some there and
		// some come while no world runs.
		stressed = stressed && (c.left > c.down && c.down > 0 || source == EventJob || source == EventCron)
	}
	stressed = stressed && s.lost > 0 && s.retold > 0 && s.switches > 0 && s.failedReads > 0 && s.reports > 0 && s.reportsCut > 0 && s.reportsTold > 0 && s.reportsUnrung > 0 && s.barriers > 0 && s.resumed > 0
	if s.failure != "" {
		t.Fatalf("%s\nround %d: %s\nagain with %s=%d %s=%d", summary, s.round, s.failure, stressSeedEnv, seed, stressRoundsEnv, rounds)
	}
	if !stressed {
		t.Fatalf("the run stressed nothing: %s", summary)
	}
	t.Log(summary)
}
