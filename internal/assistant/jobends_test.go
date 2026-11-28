package assistant

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// restart is the next process over the same disk, at the same moment: what
// the reactor finds there is all it knows.
func (f *eventFixture) restart(t *testing.T) *eventFixture {
	t.Helper()
	quiesce(t, f.svc, nil)
	next := newEventFixtureIn(t, f.dir, &fakeRunner{answer: eventAnswer})
	next.owner = f.owner
	next.clock.t = f.clock.now()
	return next
}

// endJob writes a job the way its close leaves it, a minute further on the
// clock, and answers the entry. Nothing is published: that is the report's
// part, see report.
func (f *eventFixture) endJob(t *testing.T, name string, state JobState) Job {
	t.Helper()
	f.clock.advance(time.Minute)
	at := f.clock.now()
	store := f.reactor.jobs.Of(f.owner.ID)
	store.Save(Job{
		Terminal: "term-" + name, Name: name, Project: "demo", State: state,
		Note: "the report of " + name, CreatedAt: at.Add(-time.Hour),
		UpdatedAt: at, NoteAt: at, ClosedAt: at,
	})
	job, _ := store.Get("term-" + name)
	return job
}

// report is the rest of a close, the report in the thread and its event, the
// way the watcher sends it.
func (f *eventFixture) report(t *testing.T, job Job) {
	t.Helper()
	verdict, _ := jobEndVerdict(job.State)
	f.svc.recordWake(job, statefile.NewID(), verdict, "the whole report of "+job.Name, "")
	for {
		select {
		case <-f.news:
		default:
			return
		}
	}
}

// taken is what a trigger holds of job ends: the headlines waiting in its
// window, and the ends on its account.
func (f *eventFixture) taken(t *testing.T, id string) (pending []string, ends int) {
	t.Helper()
	trigger, ok := f.reactor.Get(id)
	if !ok {
		t.Fatalf("the trigger %s is gone", id)
	}
	for _, ev := range trigger.Pending {
		pending = append(pending, ev.Headline)
	}
	return pending, len(trigger.Ends)
}

func (f *eventFixture) wantPending(t *testing.T, id string, want ...string) {
	t.Helper()
	pending, _ := f.taken(t, id)
	if strings.Join(pending, "|") != strings.Join(want, "|") {
		t.Fatalf("want %q in the window of %s, got %q", want, id, pending)
	}
}

// publishes counts what the plugins are handed from here on.
func (f *eventFixture) publishes() *int {
	n := new(int)
	f.reactor.OnPublish(func(CockpitEvent) { *n++ })
	return n
}

// Must case 1, the incident of 2026-11-21: three triggers on job-done, two on
// job-blocked and two on job-expired, four jobs the done triggers took live.
// The triggers on the other kinds never take a done job, so their watermark
// stood days back, and every restart published the four jobs once for each of
// them.
func TestARestartDeliversNoJobEndAgain(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	var done, other []Trigger
	for _, event := range []string{"job-expired", "job-blocked", "job-done", "job-expired", "job-blocked", "job-done", "job-done"} {
		trigger := f.trigger(t, TriggerSpec{Event: event, Task: "SAY_NOTHING"})
		if event == "job-done" {
			done = append(done, trigger)
		} else {
			other = append(other, trigger)
		}
	}
	for _, name := range []string{"rebase", "build", "review", "fix"} {
		f.report(t, f.endJob(t, name, JobDone))
	}
	f.clock.advance(time.Minute)
	f.reactor.Tick()
	f.waitQuiet(t)
	fired := map[string]int{}
	for _, trigger := range done {
		fresh, _ := f.reactor.Get(trigger.ID)
		if fresh.Fired == 0 || len(fresh.Pending) != 0 || len(fresh.Ends) != 4 {
			t.Fatalf("want the four ends taken live and spent, got %+v", fresh)
		}
		fired[trigger.ID] = fresh.Fired
	}

	for round := 1; round <= 3; round++ {
		f = f.restart(t)
		published := f.publishes()
		f.reactor.Recover()
		f.clock.advance(time.Minute)
		f.reactor.Tick()
		f.waitQuiet(t)
		if *published != 0 {
			t.Fatalf("restart %d handed %d events to the plugins", round, *published)
		}
		for _, trigger := range done {
			if fresh, _ := f.reactor.Get(trigger.ID); fresh.Fired != fired[trigger.ID] || len(fresh.Pending) != 0 {
				t.Fatalf("restart %d delivered again to a trigger on job-done: %+v", round, fresh)
			}
		}
		for _, trigger := range other {
			if fresh, _ := f.reactor.Get(trigger.ID); fresh.Fired != 0 || len(fresh.Pending) != 0 {
				t.Fatalf("restart %d fired a trigger no job end was for: %+v", round, fresh)
			}
		}
		if len(f.reactionTurns()) != 0 {
			t.Fatalf("restart %d bought a turn", round)
		}
	}
}

// Must case 2: the job is closed in jobs.json and the process died before any
// trigger took its event.
func TestAJobEndOfTheCrashWindowIsTakenOnce(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	done := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING"})
	closed := f.trigger(t, TriggerSpec{Event: "job-closed", Task: "SAY_NOTHING"})
	blocked := f.trigger(t, TriggerSpec{Event: "job-blocked", Task: "SAY_NOTHING"})
	f.endJob(t, "crashed", JobDone)

	for round := 1; round <= 2; round++ {
		f = f.restart(t)
		published := f.publishes()
		f.reactor.Recover()
		f.wantPending(t, done.ID, "Job done: crashed in demo")
		f.wantPending(t, closed.ID, "Job done: crashed in demo")
		f.wantPending(t, blocked.ID)
		// The plugins hear of an end from the report alone, which this job
		// never got to: no register says which of them missed it.
		if *published != 0 {
			t.Fatalf("restart %d handed %d events to the plugins", round, *published)
		}
	}
	if fresh, _ := f.reactor.Get(done.ID); fresh.Pending[0].Body != "the report of crashed" {
		t.Fatalf("want the line the close wrote as the body, got %q", fresh.Pending[0].Body)
	}
}

// Must case 3: the process died after one trigger took the event and before
// the next one did.
func TestARestartHandsAJobEndOnlyToTheTriggerThatMissedIt(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	first := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING"})
	second := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING"})
	ev, _ := jobEvent(f.endJob(t, "halfway", JobDone))
	f.reactor.mu.Lock()
	f.reactor.take(first, ev)
	f.reactor.mu.Unlock()

	for round := 1; round <= 2; round++ {
		f = f.restart(t)
		f.reactor.Recover()
		f.wantPending(t, first.ID, "Job done: halfway in demo")
		f.wantPending(t, second.ID, "Job done: halfway in demo")
	}
}

// Must case 4: two jobs close at almost the same time and the later end is
// taken first. The watermark then stands past the earlier one, which is why
// it cannot be what decides.
func TestAnEarlierJobEndTakenLateSurvivesARestart(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING"})
	f.endJob(t, "earlier", JobDone)
	f.report(t, f.endJob(t, "later", JobDone))
	f.wantPending(t, trigger.ID, "Job done: later in demo")
	if fresh, _ := f.reactor.Get(trigger.ID); !fresh.SeenAt.After(fresh.Since) {
		t.Fatalf("want the watermark past the earlier end, got %+v", fresh)
	}

	for round := 1; round <= 2; round++ {
		f = f.restart(t)
		f.reactor.Recover()
		f.wantPending(t, trigger.ID, "Job done: later in demo", "Job done: earlier in demo")
	}
}

// Must case 5: a closed job is still written to. The check its expiry killed
// clears its spinner, and a restart finds the spinner of a check that died
// with the process. Neither is an end, and neither may put its own words where
// the close wrote the report.
func TestAJobTouchedAfterItClosedIsNotDeliveredAgain(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-closed", Task: "SAY_NOTHING"})
	watcher := NewWatcher(f.svc, f.reactor.jobs, nil, nil)
	watcher.now = f.clock.now
	f.clock.advance(time.Minute)
	job, err := watcher.Steer(Job{Owner: f.owner.ID, Terminal: "term-late", Name: "late", Project: "demo"})
	if err != nil {
		t.Fatalf("steer: %v", err)
	}
	job = watcher.mark(job, func(fresh *Job) { fresh.CheckingSince = f.clock.now() })

	f.clock.advance(time.Minute)
	watcher.expire(job, "the time it was given is over")
	closed, _ := watcher.Get("term-late")
	const note = "Stopped steering: the time it was given is over."
	if closed.Note != note || !closed.ClosedAt.Equal(f.clock.now()) {
		t.Fatalf("want the close on the entry, got %+v", closed)
	}
	f.wantPending(t, trigger.ID, "Job expired: late in demo")

	// The killed check comes back without a verdict.
	f.clock.advance(time.Minute)
	watcher.conclude(job, checkContext{JobCreatedAt: job.CreatedAt}, wakeOutcome{}, errors.New("killed"))
	touched, _ := watcher.Get("term-late")
	if touched.Checking() || !touched.UpdatedAt.After(closed.UpdatedAt) {
		t.Fatalf("want the spinner cleared by a later write, got %+v", touched)
	}

	// And the process dies with the spinner of another one still standing.
	f.clock.advance(time.Minute)
	watcher.mark(touched, func(fresh *Job) { fresh.CheckingSince = f.clock.now() })
	f = f.restart(t)
	watcher = NewWatcher(f.svc, f.reactor.jobs, nil, nil)
	f.clock.advance(time.Minute)
	watcher.now = f.clock.now
	watcher.Recover(nil)
	f.reactor.Recover()

	after, _ := watcher.Get("term-late")
	if after.Checking() || after.Note != note || !after.NoteAt.Equal(closed.NoteAt) || !after.ClosedAt.Equal(closed.ClosedAt) {
		t.Fatalf("want the spinner gone and the close as it was written, got %+v", after)
	}
	if ev, _ := jobEvent(after); ev.Body != note || !ev.Time.Equal(closed.ClosedAt) {
		t.Fatalf("want the event the close published, got %+v", ev)
	}
	// The window of the one event closed meanwhile, so it is spent or still
	// waiting, and it is the only one either way.
	if fresh, _ := f.reactor.Get(trigger.ID); fresh.Fired+len(fresh.Pending) != 1 || len(fresh.Ends) != 1 {
		t.Fatalf("want the one end the trigger took live and no other, got %+v", fresh)
	}
}

// Must case 6: a barrier takes a job that closed before it was made, which is
// its own rule and none of the recovery's, and the recovery counts no target a
// second time.
func TestARecoveryCountsABarriersTargetOnce(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.endJob(t, "first", JobDone)
	f.clock.advance(time.Minute)
	f.steerJob(t, "second")
	barrier := f.trigger(t, TriggerSpec{
		Event: "job-closed", All: true, Task: "MAGIC summarize both",
		Targets: targets("term-first", "term-second"), BatchSet: true,
	})
	f.wantPending(t, barrier.ID, "Job done: first in demo")

	f = f.restart(t)
	f.reactor.Recover()
	f.wantPending(t, barrier.ID, "Job done: first in demo")
	if fresh, _ := f.reactor.Get(barrier.ID); !fresh.Waiting() || fresh.Fired != 0 {
		t.Fatalf("want the barrier still waiting for its second target, got %+v", fresh)
	}

	// The second target closes in a crash window: the recovery brings that
	// one and leaves the first alone.
	f.endJob(t, "second", JobBlocked)
	f = f.restart(t)
	f.reactor.Recover()
	answer := f.waitPushed(t, 1)
	if answer.Origin.Count != 2 {
		t.Fatalf("want one turn about both targets, got %+v", answer.Origin)
	}
	f = f.restart(t)
	f.reactor.Recover()
	if fresh, _ := f.reactor.Get(barrier.ID); fresh.Fired != 1 || len(fresh.Pending) != 0 {
		t.Fatalf("want nothing taken again after the barrier fired, got %+v", fresh)
	}
}

// Must case 7: a trigger made after a job closed gets nothing from a
// recovery, and neither does a one shot that is spent.
func TestARecoveryHandsNothingToALaterOrASpentTrigger(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	once := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING", Once: true, BatchSet: true})
	f.report(t, f.endJob(t, "spends-it", JobDone))
	f.waitQuiet(t)
	if fresh, _ := f.reactor.Get(once.ID); fresh.State != TriggerDone || fresh.Fired != 1 {
		t.Fatalf("want the one shot spent, got %+v", fresh)
	}
	f.endJob(t, "before", JobDone)
	f.clock.advance(time.Minute)
	later := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING"})

	f = f.restart(t)
	f.reactor.Recover()
	f.wantPending(t, later.ID)
	if fresh, _ := f.reactor.Get(once.ID); fresh.Fired != 1 || len(fresh.Pending) != 0 {
		t.Fatalf("a spent one shot took a job end: %+v", fresh)
	}
}

// Must case 8: a terminal steered again carries a new job under the same key.
// Its end is a new end, and the one before it is not delivered again.
func TestATerminalSteeredAgainEndsAnew(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING"})
	f.report(t, f.endJob(t, "again", JobDone))
	f.clock.advance(time.Minute)
	f.reactor.Tick()
	f.waitQuiet(t)
	if fresh, _ := f.reactor.Get(trigger.ID); fresh.Fired != 1 || len(fresh.Ends) != 1 {
		t.Fatalf("want the first end taken and spent, got %+v", fresh)
	}

	f.steerJob(t, "again")
	second := f.endJob(t, "again", JobDone)
	for round := 1; round <= 2; round++ {
		f = f.restart(t)
		f.reactor.Recover()
		f.wantPending(t, trigger.ID, "Job done: again in demo")
	}
	fresh, _ := f.reactor.Get(trigger.ID)
	if len(fresh.Ends) != 1 || !fresh.Ends[0].ClosedAt.Equal(second.ClosedAt) || !fresh.Pending[0].Time.Equal(second.ClosedAt) {
		t.Fatalf("want the new end alone on the account, got %+v", fresh)
	}
}

// The rebase onto the codex coder: a codex coder started without a prompt
// takes codex's own id with its first message, and its job moves under that
// id, see Watcher.TerminalMoved. An end a trigger took under the key is no
// new end under the id, one it missed under the key is taken once, and a
// barrier that names the id afterwards counts both as arrived.
func TestAnEndIsOneEndAfterItsTerminalTookAnotherId(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	wide := f.trigger(t, TriggerSpec{Event: "job-closed", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	f.report(t, f.endJob(t, "taken", JobBlocked))
	f.endJob(t, "missed", JobDone)
	f.wantPending(t, wide.ID, "Job blocked: taken in demo")

	watcher := NewWatcher(f.svc, f.reactor.jobs, nil, nil)
	for _, name := range []string{"taken", "missed"} {
		watcher.TerminalMoved("term-"+name, "record-"+name)
		if job, ok := f.reactor.jobs.Of(f.owner.ID).Get("record-" + name); !ok || job.MovedFrom != "term-"+name {
			t.Fatalf("want the job of %s under the id with the key it left, got %+v", name, job)
		}
	}

	for round := 1; round <= 2; round++ {
		f = f.restart(t)
		published := f.publishes()
		f.reactor.Recover()
		f.wantPending(t, wide.ID, "Job blocked: taken in demo", "Job done: missed in demo")
		if _, ends := f.taken(t, wide.ID); ends != 2 || *published != 0 {
			t.Fatalf("restart %d: want the two ends on the account and nothing published, got %d ends, %d published", round, ends, *published)
		}
	}

	f.edit(t, wide.ID, TriggerSpec{Targets: targets("record-taken", "record-missed"), TargetsSet: true, All: true, AllSet: true})
	f.wantPending(t, wide.ID, "Job blocked: taken in demo", "Job done: missed in demo")
	if fresh, _ := f.reactor.Get(wide.ID); fresh.Waiting() || len(fresh.Ends) != 2 {
		t.Fatalf("want both targets arrived and no end taken again, got %+v", fresh)
	}
}

// oldRelease writes the two files of an assistant the way the release before
// ClosedAt, Since and Ends wrote them.
func (f *eventFixture) oldRelease(t *testing.T, triggers, jobs []map[string]any) {
	t.Helper()
	dir := f.store.InstanceDir(f.owner.ID)
	for name, list := range map[string][]map[string]any{triggersFileName: triggers, jobsFileName: jobs} {
		raw, err := json.Marshal(list)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// oldReleasePublishes is the recovery of the release before, by its own rule:
// every closed job written to after any standing job trigger's watermark is
// published again, to all of them.
func (f *eventFixture) oldReleasePublishes(t *testing.T) []string {
	t.Helper()
	dir := f.store.InstanceDir(f.owner.ID)
	var triggers []struct {
		ID     string       `json:"id"`
		Source string       `json:"source"`
		State  TriggerState `json:"state"`
		SeenAt time.Time    `json:"seenAt"`
	}
	var jobs []struct {
		Name      string    `json:"name"`
		State     JobState  `json:"state"`
		UpdatedAt time.Time `json:"updatedAt"`
	}
	for name, into := range map[string]any{triggersFileName: &triggers, jobsFileName: &jobs} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
	}
	var out []string
	for _, trigger := range triggers {
		if trigger.State != TriggerStanding || trigger.Source != EventJob {
			continue
		}
		for _, job := range jobs {
			if _, ok := jobEndVerdict(job.State); ok && job.UpdatedAt.After(trigger.SeenAt) {
				out = append(out, trigger.ID+" publishes "+job.Name)
			}
		}
	}
	return out
}

// Must case 9: the state the release before wrote is read without a step,
// nothing it delivered is delivered again, what it would have caught up is
// caught up, and what this binary writes floods nothing when that release
// reads it after a rollback, nor when this one reads what that release wrote
// over it.
func TestTheStateOfTheReleaseBeforeIsTakenOverAndHandedBack(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	at := func(minute int) string {
		return f.clock.now().Add(time.Duration(minute) * time.Minute).Format(time.RFC3339Nano)
	}
	trigger := func(id, kind string, seen int) map[string]any {
		return map[string]any{
			"id": id, "source": EventJob, "kind": kind, "task": "SAY_NOTHING", "batchSeconds": 30,
			"state": TriggerStanding, "createdAt": at(0), "updatedAt": at(seen), "seenAt": at(seen),
		}
	}
	job := func(name string, closed, written int) map[string]any {
		return map[string]any{
			"terminal": "term-" + name, "name": name, "project": "demo", "state": JobDone,
			"note": "the report of " + name, "createdAt": at(0), "noteAt": at(closed), "updatedAt": at(written),
		}
	}
	f.oldRelease(t,
		// The trigger on job-done took its last end at minute 20, the one on
		// job-blocked never took any.
		[]map[string]any{trigger("on-done", "done", 20), trigger("on-blocked", "blocked", 0)},
		[]map[string]any{
			job("delivered", 10, 10),
			// Delivered too, and written to afterwards.
			job("touched", 15, 25),
			// Closed after the last end the trigger took: the crash window of
			// the release before.
			job("missed", 30, 30),
		})
	if len(f.oldReleasePublishes(t)) == 0 {
		t.Fatal("want a state the release before floods on")
	}
	f.clock.advance(time.Hour)

	for round := 1; round <= 2; round++ {
		f = f.restart(t)
		f.reactor.Recover()
		f.wantPending(t, "on-done", "Job done: missed in demo")
		f.wantPending(t, "on-blocked")
		if got := f.oldReleasePublishes(t); len(got) != 0 {
			t.Fatalf("after a rollback the release before would publish again: %q", got)
		}
	}
	listed := f.reactor.ListOf(f.owner.ID)
	if len(listed) != 2 || listed[0].Task != "SAY_NOTHING" || listed[1].Task != "SAY_NOTHING" {
		t.Fatalf("want both triggers as they were written, got %+v", listed)
	}

	// The release before writes the files back without the fields it does not
	// know, and this binary reads them again.
	for _, trigger := range listed {
		f.reactor.triggers.Of(f.owner.ID).Update(trigger.ID, func(s *Trigger) bool {
			s.Since, s.Ends = time.Time{}, nil
			return true
		})
	}
	f = f.restart(t)
	f.reactor.Recover()
	f.wantPending(t, "on-done", "Job done: missed in demo")
	f.wantPending(t, "on-blocked")
}

// Must case 10: what a trigger keeps of the ends it took is bounded by the
// jobs the store holds, and a trigger that is deleted leaves nothing.
func TestTheEndsATriggerTookGoWithTheirJobs(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING"})
	total := maxClosedJobs + 30
	for i := range total {
		f.report(t, f.endJob(t, fmt.Sprintf("job-%d", i), JobDone))
		if i%10 == 9 {
			f.clock.advance(time.Minute)
			f.reactor.Tick()
			f.waitQuiet(t)
		}
	}
	held := len(f.reactor.jobs.Of(f.owner.ID).List())
	fresh, _ := f.reactor.Get(trigger.ID)
	if held > maxClosedJobs || len(fresh.Ends) != held {
		t.Fatalf("want the ends of the %d jobs the store holds of %d, got %d", held, total, len(fresh.Ends))
	}

	// The terminals are deleted and the startup prunes their jobs.
	f.reactor.jobs.PruneTerminals(map[string]bool{"term-job-" + fmt.Sprint(total-1): true})
	f = f.restart(t)
	f.reactor.Recover()
	if fresh, _ := f.reactor.Get(trigger.ID); len(fresh.Ends) != 1 || len(fresh.Pending) != 0 {
		t.Fatalf("want the one end left whose job is, got %d ends, %d waiting", len(fresh.Ends), len(fresh.Pending))
	}

	// A jobs file that reads empty says nothing about the jobs: the account
	// stays, or the file coming back would bring every end again.
	path := filepath.Join(f.store.InstanceDir(f.owner.ID), jobsFileName)
	if err := os.Rename(path, path+".away"); err != nil {
		t.Fatalf("move the jobs away: %v", err)
	}
	f = f.restart(t)
	f.reactor.Recover()
	if err := os.Rename(path+".away", path); err != nil {
		t.Fatalf("move the jobs back: %v", err)
	}
	f = f.restart(t)
	f.reactor.Recover()
	if fresh, _ := f.reactor.Get(trigger.ID); len(fresh.Ends) != 1 || len(fresh.Pending) != 0 {
		t.Fatalf("want the account kept over an empty read, got %d ends, %d waiting", len(fresh.Ends), len(fresh.Pending))
	}

	if err := f.reactor.Remove(trigger.ID, ""); err != nil {
		t.Fatalf("remove: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(f.store.InstanceDir(f.owner.ID), triggersFileName))
	if err != nil {
		t.Fatalf("read the triggers: %v", err)
	}
	if strings.Contains(string(raw), "term-job-") {
		t.Fatalf("a deleted trigger left its ends behind: %s", raw)
	}
}

// wantEachEndInOneTurn reads the reactions the runner was asked for: every
// job-<i> below total stands in exactly one of them, and none reads more than
// a full window.
func (f *eventFixture) wantEachEndInOneTurn(t *testing.T, total int) {
	t.Helper()
	prompts := ""
	for _, req := range f.reactionTurns() {
		if got := strings.Count(req.Prompt, "**Job done: "); got > maxTriggerPendingNotes {
			t.Fatalf("want at most %d events in one turn, got %d", maxTriggerPendingNotes, got)
		}
		prompts += req.Prompt
	}
	for i := range total {
		if got := strings.Count(prompts, fmt.Sprintf("Job done: job-%d in demo", i)); got != 1 {
			t.Fatalf("want job-%d in exactly one turn, got %d", i, got)
		}
	}
}

// Must case 11: a window that is full takes a job end all the same, and the
// turn it buys reads a full window of them, the rest the next one. The store
// is no place to fetch such an end from later: the last job here leaves it
// before the window fires, its terminal is steered again.
func TestAFullWindowLosesNoJobEnd(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	total := maxTriggerPendingNotes + 2
	for i := range total {
		f.report(t, f.endJob(t, fmt.Sprintf("job-%d", i), JobDone))
	}
	if fresh, _ := f.reactor.Get(trigger.ID); len(fresh.Pending) != total || len(fresh.Ends) != total {
		t.Fatalf("want every end taken, got %d waiting, %d on the account", len(fresh.Pending), len(fresh.Ends))
	}
	f.steerJob(t, fmt.Sprintf("job-%d", total-1))

	f.clock.advance(time.Hour)
	f.reactor.Tick()
	waitFor(t, "the second turn", func() bool {
		fresh, _ := f.reactor.Get(trigger.ID)
		return fresh.Fired == 2 && len(fresh.Pending) == 0 && !fresh.Reacting()
	})
	f.wantEachEndInOneTurn(t, total)
}

// Round 2, the window that fires inside a recovery: 19 ends wait, the batch
// time passed while the cockpit was down, and 3 more closed in that gap. The
// first of them fills the window and fires it, and the pass, which still
// holds the trigger as it read it before, hands on the other two. Each of the
// 22 is read by one turn.
func TestAWindowThatFiresInARecoveryTakesEachMissedEndOnce(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	waiting, missed := maxTriggerPendingNotes-1, 3
	for i := range waiting {
		f.report(t, f.endJob(t, fmt.Sprintf("job-%d", i), JobDone))
	}
	for i := range missed {
		f.endJob(t, fmt.Sprintf("job-%d", waiting+i), JobDone)
	}
	f.clock.advance(2 * time.Hour)

	f = f.restart(t)
	f.reactor.Recover()
	waitFor(t, "the turn about the rest", func() bool {
		fresh, _ := f.reactor.Get(trigger.ID)
		return fresh.Fired == 2 && len(fresh.Pending) == 0 && !fresh.Reacting()
	})
	if fresh, _ := f.reactor.Get(trigger.ID); len(fresh.Ends) != waiting+missed {
		t.Fatalf("want %d ends on the account, got %d", waiting+missed, len(fresh.Ends))
	}
	f.wantEachEndInOneTurn(t, waiting+missed)
}

// Round 2, the report behind the recovery: the recovery read the closed job
// out of the store while the report that closed it was still on its way.
func TestAReportBehindTheRecoveryIsNotTakenAgain(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING"})
	job := f.endJob(t, "raced", JobDone)
	f.reactor.Recover()
	f.wantPending(t, trigger.ID, "Job done: raced in demo")

	f.report(t, job)
	f.wantPending(t, trigger.ID, "Job done: raced in demo")
	if _, ends := f.taken(t, trigger.ID); ends != 1 {
		t.Fatalf("want the one end on the account, got %d", ends)
	}
}

// Round 2, the seed of a barrier: an edit seeds the targets that have not
// arrived, and after a fire none has. The ends the barrier already spent are
// not taken again, and one that still waits in the window counts its target
// as arrived instead of standing there twice.
func TestABarrierSeedTakesNoEndAgain(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.endJob(t, "first", JobDone)
	f.endJob(t, "second", JobBlocked)
	f.clock.advance(time.Minute)
	barrier := f.trigger(t, TriggerSpec{
		Event: "job-closed", All: true, Task: "SAY_NOTHING",
		Targets: targets("term-first", "term-second"), BatchSet: true,
	})
	f.waitQuiet(t)
	f.edit(t, barrier.ID, TriggerSpec{Task: "SAY_NOTHING again"})
	f.waitQuiet(t)
	if fresh, _ := f.reactor.Get(barrier.ID); fresh.Fired != 1 || len(fresh.Pending) != 0 || !fresh.Waiting() {
		t.Fatalf("want the barrier spent once and waiting again, got %+v", fresh)
	}

	wide := f.trigger(t, TriggerSpec{Event: "job-closed", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	f.report(t, f.endJob(t, "third", JobDone))
	f.wantPending(t, wide.ID, "Job done: third in demo")
	f.edit(t, wide.ID, TriggerSpec{Targets: targets("term-third", "term-first"), TargetsSet: true, All: true, AllSet: true})
	f.wantPending(t, wide.ID, "Job done: third in demo", "Job done: first in demo")
	if fresh, _ := f.reactor.Get(wide.ID); fresh.Waiting() {
		t.Fatalf("want both targets arrived, got %+v", fresh.Targets)
	}
}

// Round 2, found while the stress test was built: a trigger was dated before
// it was stored, off the reactor's lock. A job that closed in between was past
// the trigger's date and its report found no trigger to hand the end to.
func TestAJobThatClosesWhileATriggerIsMadeReachesIt(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	clock := f.reactor.now
	var dated atomic.Bool
	reported := make(chan struct{})
	f.reactor.now = func() time.Time {
		at := clock()
		if dated.CompareAndSwap(false, true) {
			go func() {
				defer close(reported)
				f.report(t, f.endJob(t, "meanwhile", JobDone))
			}()
			// The report either runs through, or waits for the lock the trigger
			// is made under: then nothing more happens until it is stored.
			select {
			case <-reported:
			case <-time.After(50 * time.Millisecond):
			}
		}
		return at
	}
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	<-reported
	f.wantPending(t, trigger.ID, "Job done: meanwhile in demo")
}

// Round 2, found while the stress test was built: the startup sweeps the
// jobs of terminals that are gone before the recovery runs. A job that closed
// while the process died is only in the store, so the sweep leaves it until
// its end is taken.
func TestTheStartupSweepKeepsAJobWhoseEndIsOwed(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	f.report(t, f.endJob(t, "taken", JobDone))
	f.endJob(t, "crashed", JobDone)

	f = f.restart(t)
	if removed := f.reactor.PruneTerminals(nil); removed != 1 {
		t.Fatalf("want the job swept whose end was taken and no other, got %d", removed)
	}
	f.reactor.Recover()
	f.wantPending(t, trigger.ID, "Job done: taken in demo", "Job done: crashed in demo")

	f = f.restart(t)
	if removed := f.reactor.PruneTerminals(nil); removed != 1 {
		t.Fatalf("want the job swept once its end is taken, got %d", removed)
	}
	f.reactor.Recover()
	f.wantPending(t, trigger.ID, "Job done: taken in demo", "Job done: crashed in demo")
}

// Round 2, found by the stress test: a trigger was removed off the reactor's
// lock. An edit that had read the entry stored it whole afterwards, and the
// removed trigger stood again.
func TestAnEditDoesNotBringBackARemovedTrigger(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING"})
	clock := f.reactor.now
	var reading atomic.Bool
	removed := make(chan error, 1)
	f.reactor.now = func() time.Time {
		if reading.CompareAndSwap(false, true) {
			go func() { removed <- f.reactor.Remove(trigger.ID, "") }()
			// The removal either runs through, or waits for the lock the edit
			// holds: then it happens once the edit is stored.
			select {
			case err := <-removed:
				removed <- err
			case <-time.After(50 * time.Millisecond):
			}
		}
		return clock()
	}
	f.edit(t, trigger.ID, TriggerSpec{Task: "SAY_NOTHING twice"})
	if err := <-removed; err != nil {
		t.Fatalf("remove: %v", err)
	}
	if fresh, ok := f.reactor.Get(trigger.ID); ok {
		t.Fatalf("the removed trigger stands again: %+v", fresh)
	}
}

// waitingTurn fires a trigger on one job end while every wake slot is taken,
// and answers the fixture of the process that comes after a kill: the
// reaction of the first one still waits for its slot and is never let go, so
// the events it holds are in the memory of a process that is gone.
func waitingTurn(t *testing.T) (*eventFixture, Trigger) {
	t.Helper()
	f := newEventFixture(t)
	f.create(t)
	f.svc.slots = newCheckSlots(func() int { return 1 })
	f.svc.slots.take()
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "MAGIC carry on", BatchSet: true})
	f.report(t, f.endJob(t, "queued", JobDone))
	fresh, _ := f.reactor.Get(trigger.ID)
	if !fresh.Reacting() || len(fresh.Pending) != 0 || fresh.Turn == nil || fresh.Turn.Started || len(fresh.Turn.Events) != 1 {
		t.Fatalf("want the window spent and its event held for a turn that waits, got %+v", fresh)
	}
	if len(f.reactionTurns()) != 0 || len(f.svc.runs.List()) != 0 {
		t.Fatal("the turn started although no slot is free")
	}
	next := newEventFixtureIn(t, f.dir, &fakeRunner{answer: eventAnswer})
	next.owner = f.owner
	next.clock.t = f.clock.now()
	return next, fresh
}

// wantOneAnswer restarts twice more and holds that the thread carries the one
// pushed answer and that no further turn was bought.
func (f *eventFixture) wantOneAnswer(t *testing.T, trigger Trigger) {
	t.Helper()
	for round := 1; round <= 2; round++ {
		f = f.restart(t)
		f.svc.Recover()
		f.reactor.Recover()
		f.waitQuiet(t)
		if len(f.reactionTurns()) != 0 || len(f.pushed(t)) != 1 {
			t.Fatalf("restart %d: want the one answer and no further turn, got %d turns, %d answers", round, len(f.reactionTurns()), len(f.pushed(t)))
		}
	}
	if fresh, _ := f.reactor.Get(trigger.ID); fresh.Fired != 1 || fresh.Reacting() || fresh.Turn != nil || len(fresh.Pending) != 0 {
		t.Fatalf("want the trigger at rest after its one fire, got %+v", fresh)
	}
}

// Round 2, item 4: the process dies between the write that empties the window
// and the turn's registration, while the turn waits for a wake slot. The next
// process starts that turn, once, and its answer lands in the thread.
func TestARestartWhileAReactionWaitsForASlotLosesNoEvent(t *testing.T) {
	f, trigger := waitingTurn(t)
	f.svc.Recover()
	answer := f.waitPushed(t, 1)
	if answer.ID != trigger.Turn.MessageID || answer.State != StateComplete || answer.Origin.Headline != "Job done: queued in demo" {
		t.Fatalf("want the answer of the turn the fire reserved, got %+v", answer)
	}
	f.waitQuiet(t)
	turns := f.reactionTurns()
	if len(turns) != 1 || !strings.Contains(turns[0].Prompt, "the whole report of queued") {
		t.Fatalf("want one turn that reads the event, got %d", len(turns))
	}
	f.wantOneAnswer(t, trigger)
}

// Round 2, item 4: the process dies between the turn's registration and the
// write that says so on the trigger. The register answers for the turn, the
// next process reads it on and starts no second one.
func TestARestartRightAfterAReactionRegisteredStartsNoSecondTurn(t *testing.T) {
	f, trigger := waitingTurn(t)
	origin := turnOrigin(trigger)
	rec := RunRecord{
		ID: statefile.NewID(), Kind: RunReaction, Instance: f.owner.ID, MessageID: trigger.Turn.MessageID,
		CoderID: "claude", SessionID: "reaction-session-" + statefile.NewID(), Origin: &origin,
	}
	launcher := &fakeRunner{answer: eventAnswer}
	launching, _, _, _ := newTestServiceIn(t, t.TempDir(), launcher)
	launching.runs = f.svc.runs
	if _, err := launching.launch(&rec, launcher, TurnRequest{Instance: f.owner.ID, SessionID: rec.SessionID, Workdir: mustWorkdir(t, f.svc, f.owner.ID), Prompt: reactionPrompt(origin, "")}); err != nil {
		t.Fatalf("launch: %v", err)
	}

	f.svc.Recover()
	answer := f.waitPushed(t, 1)
	if answer.ID != trigger.Turn.MessageID || answer.State != StateComplete {
		t.Fatalf("want the answer of the registered turn, got %+v", answer)
	}
	f.waitQuiet(t)
	if len(f.reactionTurns()) != 0 {
		t.Fatal("the next process bought a second turn for a reaction that was registered")
	}
	f.wantOneAnswer(t, trigger)
}

// Round 2, item 4: the turn was registered and is gone from the register
// without its answer in the thread, the process died between the two. The
// events land in the thread as an answer the restart interrupted, once.
func TestAReactionLostAfterItRanLandsInTheThreadAsInterrupted(t *testing.T) {
	f, trigger := waitingTurn(t)
	f.reactor.triggers.Of(f.owner.ID).Update(trigger.ID, func(s *Trigger) bool {
		s.Turn.Started = true
		return true
	})
	f.svc.Recover()
	answer := f.waitPushed(t, 1)
	if answer.ID != trigger.Turn.MessageID || answer.State != StateInterrupted || answer.Origin.Headline != "Job done: queued in demo" {
		t.Fatalf("want the event in the thread as an interrupted answer, got %+v", answer)
	}
	if fresh, _ := f.reactor.Get(trigger.ID); !fresh.Broke || fresh.Reacting() {
		t.Fatalf("want the trigger to say its reaction broke off, got %+v", fresh)
	}
	if len(f.reactionTurns()) != 0 {
		t.Fatal("a reaction that ran was started again")
	}
	f.wantOneAnswer(t, trigger)
}

// Round 2, found on the way to item 4: a reaction ended off the reactor's
// lock. An edit that had read the entry stored it whole afterwards, with the
// reaction still on it, and the trigger held its window for a turn that was
// over, until a restart started that turn a second time.
func TestAnEditDoesNotBringBackAReactionThatEnded(t *testing.T) {
	held := make(chan struct{})
	runner := &fakeRunner{answer: eventAnswer, hold: func(TurnRequest) chan struct{} { return held }}
	f := newEventFixtureIn(t, t.TempDir(), runner)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "MAGIC carry on", BatchSet: true})
	f.report(t, f.endJob(t, "edited", JobDone))
	waitFor(t, "the turn to start", func() bool {
		fresh, _ := f.reactor.Get(trigger.ID)
		return fresh.Turn != nil && fresh.Turn.Started
	})
	clock := f.reactor.now
	var reading atomic.Bool
	f.reactor.now = func() time.Time {
		if reading.CompareAndSwap(false, true) {
			// The reaction ends while the edit holds the entry it read: its
			// conclusion either runs through, or waits for the edit's lock.
			close(held)
			deadline := time.Now().Add(time.Second)
			for len(f.pushed(t)) == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			time.Sleep(50 * time.Millisecond)
		}
		return clock()
	}
	f.edit(t, trigger.ID, TriggerSpec{Task: "MAGIC carry on twice"})
	waitFor(t, "the reaction to be off the trigger", func() bool {
		fresh, _ := f.reactor.Get(trigger.ID)
		return !fresh.Reacting() && fresh.Turn == nil
	})
}
