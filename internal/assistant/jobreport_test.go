package assistant

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/detach"
	"github.com/marein/dev-cockpit/internal/notify"
)

// A job closes in jobs.json and its report lands in the thread of its owner,
// two files, and a process can die between any two writes on the way. These
// tests copy the state directory at every look a close takes at a clock, the
// watcher's before the close is written, the service's between the close and
// the report, the reactor's between the report and each trigger that takes
// the end, and start the next process over each copy.

// reportWhole is what a check answered: more than the one line the job keeps
// of it.
const reportWhole = "The README is there.\n\nIt names the project and says how to run it."

// reportDeath is the disk a process left that died at one moment, and how
// often the person had heard of the report by then.
type reportDeath struct {
	at   string
	dir  string
	rang int
}

// reportFixture is one process over a state directory: the service with its
// reactor, the watcher, and the count of what the person heard. beforeRing
// runs behind the thread write of a report and before its ring.
type reportFixture struct {
	*eventFixture
	watcher    *Watcher
	sessions   *fakeSessions
	rang       *int
	beforeRing func()
}

// reportBell is the notification center over a state directory, rung for the
// report of a job the way runServe wires it, and it answers whether the ring
// wrote an entry. The person reads every entry at once, so the quiet window
// of the center swallows no ring here.
func reportBell(dir string) func(owner, message string) bool {
	center := notify.NewService(notify.StorePath(dir), nil)
	return func(owner, message string) bool {
		before := len(center.List(0))
		center.AddOnce(owner, message)
		center.MarkTargetRead(owner)
		return len(center.List(0)) > before
	}
}

func newReportFixture(t *testing.T, dir string, owner Instance, at time.Time, rang int) *reportFixture {
	t.Helper()
	f := &reportFixture{eventFixture: newEventFixtureIn(t, dir, &fakeRunner{answer: eventAnswer}), rang: &rang}
	f.owner = owner
	f.clock.t = at
	f.svc.SetHooks(func() {}, func(string) { *f.rang++ })
	ring := reportBell(dir)
	f.svc.OnReport(func(owner, message string) {
		if f.beforeRing != nil {
			f.beforeRing()
		}
		if ring(owner, message) {
			*f.rang++
		}
	})
	f.sessions = &fakeSessions{activity: Activity{Text: "the coder said something"}, gone: map[string]bool{}}
	f.watcher = NewWatcher(f.svc, f.reactor.jobs, f.sessions, nil)
	f.watcher.now = f.clock.now
	return f
}

// steered is the job every one of these tests closes.
func (f *reportFixture) steered(t *testing.T) Job {
	t.Helper()
	job, err := f.watcher.Steer(Job{
		Owner: f.owner.ID, Terminal: "term-1", Name: "readme-task", Project: "demo", CoderID: "claude",
		Task: "Write the README", DoneWhen: "README.md exists and mentions the project",
	})
	if err != nil {
		t.Fatalf("steer: %v", err)
	}
	return job
}

// start is the startup of a process in runServe's order: the sweep of the jobs
// whose terminal is gone, which every one is that alive does not name, the
// turns, the watcher and the reactor.
func (f *reportFixture) start(alive ...string) {
	keep := map[string]bool{}
	for _, terminal := range alive {
		keep[terminal] = true
	}
	f.reactor.PruneTerminals(keep)
	f.watcher.Recover(f.svc.Recover())
	f.reactor.Recover()
}

// reports are the reports of jobs in the owner's thread.
func (f *reportFixture) reports(t *testing.T) []Message {
	t.Helper()
	var out []Message
	for _, m := range f.notes(t) {
		if m.Note.Source == NoteCheck {
			out = append(out, m)
		}
	}
	return out
}

// ends are the ends of the job a trigger holds in its window.
func (f *reportFixture) ends(t *testing.T, id string) []CockpitEvent {
	t.Helper()
	var out []CockpitEvent
	for _, ev := range f.mustGet(t, id).Pending {
		if ev.Source == EventJob && ev.Target == "term-1" {
			out = append(out, ev)
		}
	}
	return out
}

// owes reports whether the job file still carries a report.
func (f *reportFixture) owes(t *testing.T) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.store.InstanceDir(f.owner.ID), jobsFileName))
	if err != nil {
		t.Fatalf("read the jobs: %v", err)
	}
	return strings.Contains(string(raw), `"report"`)
}

// Must 1, 3 and 4: every path that closes a job with a report, and the process
// dying at every moment of it, the one between the thread write and the ring
// among them. Once the close is on the disk the report is in the thread once
// after the next start, whole and under the verdict it closed with, the
// person heard of it once, both triggers on the end took it once with its
// body, the job carries the report no longer, and a start after that one
// changes nothing.
func TestTheReportOfAJobLandsOnceWhereverTheProcessDies(t *testing.T) {
	check := checkContext{MessageID: "the-check-s-own-id", Model: "opus"}
	closes := []struct {
		name    string
		verdict Verdict
		// text is what the report has to say, where the path words it itself.
		text    string
		prepare func(f *reportFixture, job *Job)
		close   func(f *reportFixture, job Job, seen checkContext)
	}{
		{name: "a check's DONE", verdict: VerdictDone, text: reportWhole,
			close: func(f *reportFixture, job Job, seen checkContext) {
				f.watcher.conclude(job, seen, wakeOutcome{Verdict: VerdictDone, Text: reportWhole}, nil)
			}},
		{name: "a check's BLOCKED", verdict: VerdictBlocked, text: reportWhole,
			close: func(f *reportFixture, job Job, seen checkContext) {
				f.watcher.conclude(job, seen, wakeOutcome{Verdict: VerdictBlocked, Text: reportWhole}, nil)
			}},
		{name: "a check that left the coder standing", verdict: VerdictBlocked, text: "is idle and the job is not done",
			close: func(f *reportFixture, job Job, seen checkContext) {
				seen.Idle = true
				f.watcher.conclude(job, seen, wakeOutcome{Verdict: VerdictWorking, Text: "still looking"}, nil)
			}},
		{name: "a refused check", verdict: VerdictBlocked, text: "could not run: The coder does not know the model opus-9.",
			close: func(f *reportFixture, job Job, seen checkContext) {
				f.watcher.conclude(job, seen, wakeOutcome{}, UnknownModel("opus-9"))
			}},
		{name: "the silent limit", verdict: VerdictBlocked, text: "I cannot check on **readme-task** any more: The check ended without saying anything.",
			prepare: func(_ *reportFixture, job *Job) { job.Silent = maxSilentChecks - 1 },
			close: func(f *reportFixture, job Job, seen checkContext) {
				f.watcher.conclude(job, seen, wakeOutcome{}, errors.New("The check ended without saying anything."))
			}},
		{name: "the checks used up", verdict: VerdictExpired, text: "the checks it was given are used up",
			prepare: func(_ *reportFixture, job *Job) { job.Wakes = job.MaxWakes },
			close:   func(f *reportFixture, _ Job, _ checkContext) { f.watcher.SweepExpired() }},
		{name: "the last check", verdict: VerdictExpired, text: "The last check said: still going",
			prepare: func(_ *reportFixture, job *Job) { job.Wakes = job.MaxWakes - 1 },
			close: func(f *reportFixture, job Job, seen checkContext) {
				f.watcher.conclude(job, seen, wakeOutcome{Verdict: VerdictWorking, Text: "still going"}, nil)
			}},
		{name: "the time over", verdict: VerdictExpired, text: "the time it was given is over",
			prepare: func(f *reportFixture, job *Job) { job.ExpiresAt = f.clock.now().Add(-time.Minute) },
			close:   func(f *reportFixture, _ Job, _ checkContext) { f.watcher.SweepExpired() }},
		{name: "a coder that is gone", verdict: VerdictExpired, text: "the coder it was steering is gone",
			prepare: func(f *reportFixture, _ *Job) {
				f.sessions.gone["term-1"] = true
				f.clock.advance(2 * vanishGrace)
			},
			close: func(f *reportFixture, _ Job, _ checkContext) { f.watcher.Heartbeat() }},
		{name: "a deleted coder", verdict: VerdictExpired, text: "the coder was deleted",
			close: func(f *reportFixture, _ Job, _ checkContext) { f.watcher.TerminalDeleted("term-1") }},
	}
	for _, c := range closes {
		t.Run(c.name, func(t *testing.T) {
			at := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
			f := newReportFixture(t, t.TempDir(), Instance{}, at, 0)
			f.create(t)
			var triggers []Trigger
			for range 2 {
				triggers = append(triggers, f.trigger(t, TriggerSpec{Event: "job-closed", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true}))
			}
			job := f.steered(t)
			if c.prepare != nil {
				c.prepare(f, &job)
				f.reactor.jobs.Of(f.owner.ID).Save(job)
			}
			seen := check
			seen.JobCreatedAt = job.CreatedAt
			f.clock.advance(time.Minute)

			var deaths []reportDeath
			die := func(at string) {
				dir := t.TempDir()
				if err := os.CopyFS(dir, os.DirFS(f.dir)); err != nil {
					t.Fatalf("copy the state: %v", err)
				}
				deaths = append(deaths, reportDeath{at: at, dir: dir, rang: *f.rang})
			}
			f.watcher.now = func() time.Time { die("before the close"); return f.clock.now() }
			f.svc.now = func() time.Time { die("before the report"); return f.clock.now() }
			f.reactor.now = func() time.Time { die("before a trigger took"); return f.clock.now() }
			f.beforeRing = func() { die("before the ring") }
			c.close(f, job, seen)
			die("after it all")

			whole := f.reports(t)
			if len(whole) != 1 || *f.rang != 1 || whole[0].Note.Verdict != string(c.verdict) || !strings.Contains(whole[0].Content, c.text) {
				t.Fatalf("want one %s report naming %q and one ring from the process that lived, got %d rings and %+v", c.verdict, c.text, *f.rang, whole)
			}
			between, unrung, taking := 0, 0, 0
			for i, death := range deaths {
				next := newReportFixture(t, death.dir, f.owner, f.clock.now(), death.rang)
				left, ok := next.reactor.jobs.Find("term-1")
				if ok && left.State.Open() {
					next.start()
					if got := next.reports(t); len(got) != 0 || *next.rang != 0 {
						t.Fatalf("death %d, %s: the job never closed, want no report, got %d and %d rings", i, death.at, len(got), *next.rang)
					}
					continue
				}
				if death.at == "before a trigger took" {
					taking++
				}
				if death.at == "before the ring" {
					unrung++
					if left.Report == nil || len(next.reports(t)) != 1 || death.rang != 0 {
						t.Fatalf("death %d: want the report in the thread, still on the job and rung for by nobody, got %d rings and %+v", i, death.rang, left.Report)
					}
				}
				if death.at == "before the report" {
					between++
					if left.Report == nil || left.Report.Text != whole[0].Content || len(next.reports(t)) != 0 {
						t.Fatalf("death %d: want the closed job to carry its whole report and the thread none yet, got %+v", i, left.Report)
					}
				}
				for start := range 2 {
					next.start()
					got := next.reports(t)
					if len(got) != 1 || *next.rang != 1 {
						t.Fatalf("death %d, %s, start %d: want the report in the thread once and one ring, got %d and %d rings", i, death.at, start, len(got), *next.rang)
					}
					if got[0].ID != whole[0].ID || got[0].Content != whole[0].Content || got[0].Model != whole[0].Model || *got[0].Note != *whole[0].Note {
						t.Fatalf("death %d, %s: want the report the process that lived wrote, got %+v with %+v", i, death.at, got[0], got[0].Note)
					}
					if next.owes(t) {
						t.Fatalf("death %d, %s: the job still carries the report the thread holds", i, death.at)
					}
					for _, trigger := range triggers {
						ends := next.ends(t, trigger.ID)
						if len(ends) != 1 {
							t.Fatalf("death %d, %s, start %d: want the end once in the window of %s, got %d", i, death.at, start, trigger.ID, len(ends))
						}
						if death.at == "before the report" && ends[0].Body != whole[0].Content {
							t.Fatalf("death %d: want the end to carry the whole report, got %q", i, ends[0].Body)
						}
						if ends[0].Body != whole[0].Content && ends[0].Body != left.Note {
							t.Fatalf("death %d, %s: want the end to carry the report or the job's line, got %q", i, death.at, ends[0].Body)
						}
					}
				}
			}
			if between != 1 || unrung != 1 || taking < len(triggers) {
				t.Fatalf("want one death between the close and the report, one before the ring and one before each trigger took, got %+v", deaths)
			}
		})
	}
}

// A check carries the id its report is written as and the model that
// answered, and both survive the process that dies over the report.
func TestARecoveredReportKeepsTheChecksIdAndModel(t *testing.T) {
	f := newReportFixture(t, t.TempDir(), Instance{}, time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC), 0)
	f.create(t)
	job := f.steered(t)
	died := t.TempDir()
	f.svc.now = func() time.Time {
		if err := os.CopyFS(died, os.DirFS(f.dir)); err != nil {
			t.Fatalf("copy the state: %v", err)
		}
		return f.clock.now()
	}
	f.watcher.conclude(job, checkContext{MessageID: "reserved", Model: "opus", JobCreatedAt: job.CreatedAt}, wakeOutcome{Verdict: VerdictDone, Text: reportWhole}, nil)

	next := newReportFixture(t, died, f.owner, f.clock.now(), 0)
	next.start()
	got := next.reports(t)
	if len(got) != 1 || got[0].ID != "reserved" || got[0].Model != "opus" || got[0].Content != reportWhole {
		t.Fatalf("want the report under the check's id and model, got %+v", got)
	}
	if got[0].Note.Headline != "DONE: readme-task" || got[0].Wake == nil {
		t.Fatalf("want the report marked as the check's the way a live one is, got %+v", got[0].Note)
	}
}

// Must 2: a report the thread holds is not pushed again. The process died
// after the report landed and before the job let it go, so the job still
// carries it: the next start writes nothing, rings nobody, publishes nothing,
// and takes it off the job.
func TestAReportThatLandedIsNotPushedAgain(t *testing.T) {
	f := newReportFixture(t, t.TempDir(), Instance{}, time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC), 0)
	f.create(t)
	job := f.steered(t)
	f.watcher.conclude(job, checkContext{MessageID: "reserved", JobCreatedAt: job.CreatedAt}, wakeOutcome{Verdict: VerdictDone, Text: reportWhole}, nil)
	closed, _ := f.reactor.jobs.Find("term-1")
	if len(f.reports(t)) != 1 || closed.Report != nil {
		t.Fatalf("want the report in the thread and off the job, got %+v", closed.Report)
	}
	f.reactor.jobs.Of(f.owner.ID).Update("term-1", func(entry *Job) bool {
		entry.Report = &JobReport{MessageID: "reserved", Verdict: VerdictDone, Text: reportWhole}
		return true
	})

	next := newReportFixture(t, f.dir, f.owner, f.clock.now(), 0)
	published := next.publishes()
	next.start()
	if got := next.reports(t); len(got) != 1 || *next.rang != 0 || *published != 0 {
		t.Fatalf("want the one report to stay the only one and nobody told again, got %d reports, %d rings, %d events", len(got), *next.rang, *published)
	}
	if next.owes(t) {
		t.Fatal("the job still carries a report the thread holds")
	}
}

// The checks of several jobs run side by side, so more than one job can owe
// its report when a process dies. The next start writes them in the order the
// jobs closed, whatever order the file holds them in.
func TestOwedReportsLandInTheOrderTheirJobsClosed(t *testing.T) {
	f := newReportFixture(t, t.TempDir(), Instance{}, time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC), 0)
	f.create(t)
	closes := []struct {
		name  string
		after time.Duration
	}{{"second", 2 * time.Minute}, {"first", time.Minute}, {"third", 3 * time.Minute}}
	for _, c := range closes {
		closed := f.clock.now().Add(c.after)
		f.reactor.jobs.Of(f.owner.ID).Save(Job{
			Terminal: "term-" + c.name, Name: c.name, State: JobDone, CreatedAt: f.clock.now().Add(-time.Hour),
			UpdatedAt: closed, NoteAt: closed, ClosedAt: closed, Note: "the report of " + c.name,
			Report: &JobReport{MessageID: c.name, Verdict: VerdictDone, Text: "the report of " + c.name},
		})
	}
	f.clock.advance(time.Hour)
	f.start()
	var got []string
	for _, m := range f.reports(t) {
		got = append(got, m.Note.Name)
	}
	if strings.Join(got, " ") != "first second third" || *f.rang != 3 {
		t.Fatalf("want the three reports in the order their jobs closed and three rings, got %q and %d rings", got, *f.rang)
	}
}

// Must 2: a check that outlived the restart is adopted and concludes on a job
// that closed without it. The job ran out, the process died before its report
// landed and before it killed the check: the next start writes the report the
// job ran out with, and the check's DONE comes back to a closed job and
// reports nothing.
func TestAnAdoptedCheckReportsNothingOnAJobThatClosedWithoutIt(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "DONE: the job is finished"}}, block: make(chan struct{})}
	svc, store, _, runs := newTestServiceIn(t, dir, runner)
	c, err := svc.create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	jobs := NewJobs(store)
	closed := time.Now().UTC()
	jobs.Of(c.ID).Save(Job{
		Terminal: "term-1", Name: "readme-task", CoderID: "claude", State: JobExpired, MaxWakes: defaultMaxWakes,
		CreatedAt: closed.Add(-time.Hour), UpdatedAt: closed, NoteAt: closed, ClosedAt: closed,
		Note: "Stopped steering: the time it was given is over.", CheckingSince: closed.Add(-time.Minute),
		Report: &JobReport{MessageID: "ran-out", Verdict: VerdictExpired, Text: "I stopped steering **readme-task**: the time it was given is over."},
	})
	rec := orphanCheck(t, svc, c.ID, "term-1", runner)
	if !detach.Alive(rec.PID, rec.Lock) {
		t.Fatal("want the check to still be running")
	}

	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	rang := 0
	restarted.OnReport(func(string, string) { rang++ })
	watcher := NewWatcher(restarted, jobs, &fakeSessions{}, nil)
	adopted := restarted.Recover()
	if len(adopted) != 1 {
		t.Fatalf("want the running check handed to the watcher, got %+v", adopted)
	}
	watcher.Recover(adopted)
	close(runner.block)
	waitFor(t, "the check to come back", func() bool {
		job, _ := jobs.Find("term-1")
		return len(runs.List()) == 0 && !job.Checking()
	})
	quiesce(t, restarted, watcher)

	fresh, err := restarted.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(fresh.Messages) != 1 || fresh.Messages[0].ID != "ran-out" || fresh.Messages[0].Note.Verdict != string(VerdictExpired) || rang != 1 {
		t.Fatalf("want the one report the job ran out with and one ring, got %d rings and %+v", rang, fresh.Messages)
	}
	if job, _ := jobs.Find("term-1"); job.State != JobExpired || job.Report != nil {
		t.Fatalf("want the job as it closed, owing nothing, got %+v", job)
	}
}

// Must 5: a job the user called off gets no report, from the check that comes
// back to it or from the next start.
func TestAJobCalledOffOwesNoReport(t *testing.T) {
	f := newReportFixture(t, t.TempDir(), Instance{}, time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC), 0)
	f.create(t)
	job := f.steered(t)
	if err := f.watcher.Release("term-1", ""); err != nil {
		t.Fatalf("release: %v", err)
	}
	f.watcher.conclude(job, checkContext{MessageID: "late", JobCreatedAt: job.CreatedAt}, wakeOutcome{Verdict: VerdictDone, Text: reportWhole}, nil)
	f.watcher.SweepExpired()

	next := newReportFixture(t, f.dir, f.owner, f.clock.now(), *f.rang)
	next.start("term-1")
	off, _ := next.reactor.jobs.Find("term-1")
	if got := next.reports(t); len(got) != 0 || *next.rang != 0 || off.State != JobStopped || next.owes(t) {
		t.Fatalf("want a stopped job without a report, got %d reports, %d rings and %+v", len(got), *next.rang, off)
	}
}

// Must 5: whatever takes a job's entry away writes the report it still owes
// first. The process died between the close and the report, and before the
// next one's recovery got to it the terminal is steered again, which replaces
// the entry, or its coder is deleted, which removes it. The old job's report
// is in the thread once, and the job steered behind it reports for itself.
func TestAnEntryThatGoesLeavesItsOwedReportInTheThread(t *testing.T) {
	leaves := map[string]func(t *testing.T, f *reportFixture){
		"a terminal steered again": func(t *testing.T, f *reportFixture) {
			renewed := f.steered(t)
			if stands, _ := f.reactor.jobs.Find("term-1"); !stands.State.Open() || stands.Report != nil {
				t.Fatalf("want the new job open and owing nothing, got %+v", stands)
			}
			f.watcher.conclude(renewed, checkContext{JobCreatedAt: renewed.CreatedAt}, wakeOutcome{Verdict: VerdictBlocked, Text: "the second job needs a decision"}, nil)
			got := f.reports(t)
			if len(got) != 2 || got[1].Content != "the second job needs a decision" {
				t.Fatalf("want the report of the job steered behind it after the old one, got %+v", got)
			}
		},
		"a deleted coder": func(t *testing.T, f *reportFixture) {
			f.watcher.TerminalDeleted("term-1")
			if _, ok := f.reactor.jobs.Find("term-1"); ok {
				t.Fatal("the job of a deleted coder is still in the store")
			}
		},
	}
	for name, leave := range leaves {
		t.Run(name, func(t *testing.T) {
			f := newReportFixture(t, t.TempDir(), Instance{}, time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC), 0)
			f.create(t)
			job := f.steered(t)
			died := t.TempDir()
			f.svc.now = func() time.Time {
				if err := os.CopyFS(died, os.DirFS(f.dir)); err != nil {
					t.Fatalf("copy the state: %v", err)
				}
				return f.clock.now()
			}
			f.watcher.conclude(job, checkContext{JobCreatedAt: job.CreatedAt}, wakeOutcome{Verdict: VerdictDone, Text: reportWhole}, nil)

			next := newReportFixture(t, died, f.owner, f.clock.now().Add(time.Minute), 0)
			leave(t, next)
			before := len(next.reports(t))
			if before == 0 || next.reports(t)[0].Content != reportWhole || next.reports(t)[0].Note.Verdict != string(VerdictDone) {
				t.Fatalf("want the old job's whole report in the thread, got %+v", next.reports(t))
			}
			next.start()
			if got := next.reports(t); len(got) != before {
				t.Fatalf("want the start to write no report again, got %d, then %d", before, len(got))
			}
		})
	}
}

// The startup sweep runs before the watcher's recovery and drops the closed
// job of a terminal that is gone, and the cap drops the oldest closed jobs
// with every job that is steered. Neither takes a job that still owes its
// report, and both take it once the thread holds it.
func TestAJobThatOwesItsReportStaysInTheStore(t *testing.T) {
	store := &JobStore{path: t.TempDir() + "/jobs.json"}
	start := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	owed := &JobReport{MessageID: "owed", Verdict: VerdictDone, Text: reportWhole}
	store.Save(Job{Terminal: "owes", State: JobDone, CreatedAt: start, Report: owed})
	store.Save(Job{Terminal: "settled", State: JobDone, CreatedAt: start})
	if removed := store.PruneTerminals(nil); removed != 1 {
		t.Fatalf("want the sweep to take the settled job alone, got %d", removed)
	}
	for i := range maxClosedJobs + 5 {
		store.Save(Job{Terminal: fmt.Sprintf("term-%03d", i), State: JobDone, CreatedAt: start.Add(time.Duration(i+1) * time.Minute)})
	}
	if job, ok := store.Get("owes"); !ok || job.Report == nil || len(store.List()) != maxClosedJobs+1 {
		t.Fatalf("want the job that owes its report beside the %d the cap keeps, got %d entries", maxClosedJobs, len(store.List()))
	}

	store.Update("owes", func(entry *Job) bool {
		entry.Report = nil
		return true
	})
	store.Save(Job{Terminal: "one-more", State: JobDone, CreatedAt: start.Add(24 * time.Hour)})
	if _, ok := store.Get("owes"); ok || len(store.List()) != maxClosedJobs {
		t.Fatalf("want the cap to take the oldest job once it owes nothing, got %d entries", len(store.List()))
	}
	if removed := store.PruneTerminals(nil); removed != maxClosedJobs {
		t.Fatalf("want the sweep to take every settled job, got %d", removed)
	}
}

// Must 6: the jobs and the thread of the release before carry no report on a
// job, so the update owes nobody one: the jobs that release closed and
// reported are not reported again, and one whose report it lost stays lost
// rather than being made up. And a job this release closed reads the way that
// release wrote it once its report landed, which is what a rollback finds.
func TestTheJobsOfTheReleaseBeforeOweNoReport(t *testing.T) {
	f := newReportFixture(t, t.TempDir(), Instance{}, time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC), 0)
	f.create(t)
	before := f.clock.now().Add(-time.Hour).Format(time.RFC3339Nano)
	var jobs []map[string]any
	for i, state := range []JobState{JobDone, JobBlocked, JobExpired, JobStopped, JobSteering} {
		job := map[string]any{
			"terminal": fmt.Sprintf("term-before-%d", i), "name": fmt.Sprintf("before-%d", i), "project": "demo", "coderId": "claude",
			"task": "", "doneWhen": "it is done", "state": state, "wakes": 1, "maxWakes": defaultMaxWakes,
			"createdAt": before, "expiresAt": f.clock.now().Add(time.Hour).Format(time.RFC3339Nano), "updatedAt": before,
		}
		if state != JobSteering {
			job["note"], job["noteAt"] = "the report of "+job["name"].(string), before
		}
		if _, ok := jobEndVerdict(state); ok {
			job["closedAt"] = before
		}
		jobs = append(jobs, job)
	}
	f.oldRelease(t, []map[string]any{}, jobs)
	reported, _ := f.reactor.jobs.Find("term-before-0")
	f.svc.recordWake(reported, "reported-before", VerdictDone, "the whole report of before-0", "")
	*f.rang = 0

	next := newReportFixture(t, f.dir, f.owner, f.clock.now(), 0)
	next.start("term-before-0", "term-before-1", "term-before-2", "term-before-3", "term-before-4")
	if got := next.reports(t); len(got) != 1 || got[0].ID != "reported-before" || *next.rang != 0 {
		t.Fatalf("want the one report the release before wrote and nothing new, got %d rings and %+v", *next.rang, got)
	}
	if got := len(next.watcher.List()); got != len(jobs) {
		t.Fatalf("want every job of the release before kept, got %d of %d", got, len(jobs))
	}

	open, _ := next.reactor.jobs.Find("term-before-4")
	next.watcher.conclude(open, checkContext{JobCreatedAt: open.CreatedAt}, wakeOutcome{Verdict: VerdictDone, Text: reportWhole}, nil)
	if got := next.reports(t); len(got) != 2 || got[1].Content != reportWhole {
		t.Fatalf("want the job of the release before to close with its report, got %+v", got)
	}
	if next.owes(t) {
		t.Fatal("the job file carries a report after it landed, which the release before never wrote")
	}
}
