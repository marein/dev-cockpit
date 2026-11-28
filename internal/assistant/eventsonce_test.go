package assistant

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/approval"
)

// eventSource is one source of events but a job's and a schedule's, handed
// the way that source hands it. hand hands event n and answers the line a
// turn reads it by: the same n is the same event, named the same.
type eventSource struct {
	name string
	spec TriggerSpec
	hand func(f *eventFixture, n int) string
}

var eventSources = []eventSource{
	{"coder", TriggerSpec{Event: "coder-news"}, func(f *eventFixture, n int) string {
		terminal := fmt.Sprintf("term-%03d", n)
		f.reactor.Coder(terminal, CoderKindEnded, fmt.Sprintf("claude/17920713150171%03d-11", n))
		return "Coder ended its turn: " + terminal
	}},
	{"compose", TriggerSpec{Event: "compose-ended"}, func(f *eventFixture, n int) string {
		action := fmt.Sprintf("Compose up %03d", n)
		f.svc.RecordCompose(ComposeReport{Owner: f.owner.ID, Run: fmt.Sprintf("run-%03d", n), Project: "shop", Action: action})
		return "Compose done: " + action + " on shop"
	}},
	{"approval", TriggerSpec{Event: "compose-ended"}, func(f *eventFixture, n int) string {
		what := fmt.Sprintf("Compose up %03d on shop", n)
		f.svc.RecordApproval(ApprovalReport{
			ID: fmt.Sprintf("approval-%03d", n), Owner: f.owner.ID, What: what, Event: ComposeApprovalEvent,
			Outcome: approval.Outcome{Verdict: approval.Declined, Text: "declined by the user"},
		})
		return "Declined: " + what
	}},
	{"pipeline", TriggerSpec{Event: "forge-pipeline-ended", Project: "shop"}, func(f *eventFixture, n int) string {
		branch := fmt.Sprintf("branch-%03d", n)
		f.reactor.Pipeline(PipelineReport{
			Kind: PipelineKindFailed, Project: "shop", Branch: branch, Head: "a1b2c3", Forge: "GitLab",
			Address: "gitlab.example.com/team/shop", ID: fmt.Sprint(1000 + n),
		})
		return "Pipeline failed: " + branch + " in shop"
	}},
	{"review", TriggerSpec{Event: "forge-review-comment", Project: "shop"}, func(f *eventFixture, n int) string {
		author := fmt.Sprintf("author-%03d", n)
		f.reactor.ReviewComment(ReviewReport{
			Note: fmt.Sprint(2000 + n), Project: "shop", Branch: "feature", Forge: "GitLab", Address: "gitlab.example.com/team/shop",
			Change: ChangeRequest{Noun: "merge request", Ref: "!4"}, Author: author, Body: "a note",
		})
		return "Review comment: " + author + " on feature in shop, merge request !4"
	}},
}

// schedule is the source that cannot be handed by anybody: a tick falls due
// with the clock. It answers like the others, and no tick comes twice.
var schedule = eventSource{"cron", TriggerSpec{Event: "cron", Spec: "* * * * *", Timezone: "UTC"}, func(f *eventFixture, n int) string {
	f.clock.advance(time.Minute)
	f.reactor.Tick()
	return "ticked at " + f.clock.now().Format("15:04") + " UTC"
}}

// handed hands event n of a source and takes the news it rang off the
// fixture's channel, which holds a handful and would hold the next one up.
func (f *eventFixture) handed(source eventSource, n int) string {
	line := source.hand(f, n)
	for {
		select {
		case <-f.news:
		default:
			return line
		}
	}
}

// sourceTrigger makes a trigger on a source's events.
func (f *eventFixture) sourceTrigger(t *testing.T, source eventSource, task string, batch time.Duration) Trigger {
	t.Helper()
	spec := source.spec
	spec.Task, spec.Batch, spec.BatchSet = task, batch, true
	return f.trigger(t, spec)
}

// wantReadOnce holds that every line stands in exactly one of the prompts the
// reactions of this fixture were given.
func (f *eventFixture) wantReadOnce(t *testing.T, lines []string) {
	t.Helper()
	prompts := ""
	for _, req := range f.reactionTurns() {
		prompts += req.Prompt + "\n"
	}
	for _, line := range lines {
		if got := strings.Count(prompts, line); got != 1 {
			t.Fatalf("want %q read by exactly one turn, got %d", line, got)
		}
	}
}

// Must 1: a window that is full drops nothing, whatever the source. The
// reaction of the first event is held open, so everything after it waits: the
// window keeps maxTriggerWindowNotes events whole and counts the rest under
// their headlines. Then the turns read every event once, a full window of
// them whole with the fold beside it, and the rest in the turn after.
func TestAFullWindowDropsNoEventOfAnySource(t *testing.T) {
	for _, source := range append(slices.Clone(eventSources), schedule) {
		t.Run(source.name, func(t *testing.T) {
			held := make(chan struct{})
			f := newEventFixtureIn(t, t.TempDir(), &fakeRunner{answer: eventAnswer, hold: func(TurnRequest) chan struct{} { return held }})
			f.create(t)
			trigger := f.sourceTrigger(t, source, "SAY_NOTHING", 0)
			lines := []string{f.handed(source, 0)}
			waitFor(t, "the first reaction", func() bool {
				fresh, _ := f.reactor.Get(trigger.ID)
				return fresh.Reacting()
			})
			const folded = 5
			for n := 1; n <= maxTriggerWindowNotes+folded; n++ {
				lines = append(lines, f.handed(source, n))
			}

			fresh, _ := f.reactor.Get(trigger.ID)
			if len(fresh.Pending) != maxTriggerWindowNotes+1 || fresh.Held() != maxTriggerWindowNotes+folded {
				t.Fatalf("want %d events whole and %d counted, got %d entries holding %d", maxTriggerWindowNotes, folded, len(fresh.Pending), fresh.Held())
			}
			fold := fresh.Pending[len(fresh.Pending)-1]
			if !fold.folds() || fold.count() != folded || !strings.HasPrefix(fold.Headline, "5 more events arrived than there was room for") {
				t.Fatalf("want the last entry to count the %d events past the window, got %+v", folded, fold)
			}
			for _, line := range lines[1+maxTriggerWindowNotes:] {
				if !strings.Contains(fold.Body, line) {
					t.Fatalf("the fold does not name %q:\n%s", line, fold.Body)
				}
			}

			close(held)
			waitFor(t, "the turns about the window", func() bool {
				fresh, _ := f.reactor.Get(trigger.ID)
				return fresh.Fired == 3 && len(fresh.Pending) == 0 && !fresh.Reacting()
			})
			f.wantReadOnce(t, lines)
			turns := f.reactionTurns()
			if len(turns) != 3 {
				t.Fatalf("want three turns, got %d", len(turns))
			}
			if !strings.Contains(turns[1].Prompt, fmt.Sprintf("%d events arrived inside one window", maxTriggerPendingNotes+folded)) ||
				!strings.Contains(turns[1].Prompt, "**5 more events arrived than there was room for, counted here without their text**") ||
				!strings.Contains(turns[2].Prompt, fmt.Sprintf("%d events arrived inside one window", maxTriggerWindowNotes-maxTriggerPendingNotes)) {
				t.Fatalf("want a full window with the fold, then the rest, got:\n%s\n---\n%s", turns[1].Prompt, turns[2].Prompt)
			}
		})
	}
}

// Must 1 and 5: the fold is bounded like everything a trigger keeps. Events
// of one headline are one line that counts them, a line each for as many
// headlines as a turn reads events, and past that an event is counted under
// what its kind is called. However many arrive, the window is no longer on
// disk and the trigger's row counts every one of them.
func TestAFoldCountsUnderHeadlinesAndStaysBounded(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	entry := 0
	signal := func(terminal, kind string) {
		entry++
		f.reactor.Coder(terminal, kind, fmt.Sprintf("claude/%d-11", entry))
	}
	for range maxTriggerWindowNotes {
		signal("term-first", CoderKindEnded)
	}
	const loud, others = 40, maxTriggerPendingNotes + 5
	for range loud {
		signal("term-loud", CoderKindEnded)
	}
	for i := range others {
		signal(fmt.Sprintf("term-%03d", i), CoderKindEnded)
	}
	signal("term-asking", CoderKindAsks)

	fresh, _ := f.reactor.Get(trigger.ID)
	total := maxTriggerWindowNotes + loud + others + 1
	if len(fresh.Pending) != maxTriggerWindowNotes+1 || fresh.Held() != total {
		t.Fatalf("want %d entries holding %d events, got %d holding %d", maxTriggerWindowNotes+1, total, len(fresh.Pending), fresh.Held())
	}
	fold := fresh.Pending[maxTriggerWindowNotes]
	if len(fold.Folded) != maxTriggerPendingNotes+2 {
		t.Fatalf("want %d headlines and a line for either kind, got %d lines:\n%s", maxTriggerPendingNotes, len(fold.Folded), fold.Body)
	}
	for _, want := range []string{
		"- 40 times: Coder ended its turn: term-loud",
		"- Coder ended its turn: term-018",
		"- 6 times: coder ended",
		"- coder asks",
	} {
		if !strings.Contains(fold.Body+"\n", want+"\n") {
			t.Fatalf("the fold does not say %q:\n%s", want, fold.Body)
		}
	}
	if strings.Contains(fold.Body, "term-019") {
		t.Fatalf("the fold names a headline past its bound:\n%s", fold.Body)
	}
	if len(fresh.Taken) != maxTriggerTakenKeys {
		t.Fatalf("want the account at its bound of %d, got %d", maxTriggerTakenKeys, len(fresh.Taken))
	}
}

// Must 1 and 4: a job end past the window is folded like any other event and
// stands on the account like any other end, so a restart hands it to nobody
// again.
func TestAJobEndPastTheWindowIsFoldedAndTakenOnce(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	total := maxTriggerWindowNotes + 3
	for i := range total {
		f.report(t, f.endJob(t, fmt.Sprintf("job-%03d", i), JobDone))
	}
	for round := 0; round <= 2; round++ {
		fresh, _ := f.reactor.Get(trigger.ID)
		if len(fresh.Pending) != maxTriggerWindowNotes+1 || fresh.Held() != total || len(fresh.Ends) != total {
			t.Fatalf("restart %d: want %d ends held and on the account, got %d entries holding %d, %d on the account", round, total, len(fresh.Pending), fresh.Held(), len(fresh.Ends))
		}
		if fold := fresh.Pending[maxTriggerWindowNotes]; !strings.Contains(fold.Body, "- Job done: job-042 in demo") {
			t.Fatalf("the fold does not name the end past the window:\n%s", fold.Body)
		}
		f = f.restart(t)
		f.reactor.Recover()
	}
}

// Must 1 and 4: a one shot gets one turn, so that turn accounts for every
// event the trigger took, whatever the source. It reads as many whole as any
// turn and counts the ones behind them on its fold, under their headline:
// what a standing trigger leaves to its next turn has no turn to wait for
// here. Past what a window holds whole they are counted next to what the
// window folded itself, and the fold stays as bounded as any: the last of
// them go under what their kind is called. The trigger's line and the answer
// in the thread count every event, and the account names every one.
func TestAOneShotReadsEveryEventItTookInItsOneTurn(t *testing.T) {
	jobs := eventSource{name: "job", spec: TriggerSpec{Event: "job-done"}}
	for _, source := range append(slices.Clone(eventSources), jobs) {
		for _, total := range []int{maxTriggerPendingNotes + 5, maxTriggerWindowNotes + 5} {
			t.Run(fmt.Sprintf("%s %d", source.name, total), func(t *testing.T) {
				held := make(chan struct{})
				f := newEventFixtureIn(t, t.TempDir(), &fakeRunner{answer: eventAnswer, hold: func(TurnRequest) chan struct{} { return held }})
				f.create(t)
				spec := source.spec
				spec.Task, spec.Once, spec.Batch, spec.BatchSet = "sum it up", true, 2*time.Hour, true
				trigger := f.trigger(t, spec)
				var lines []string
				for n := range total {
					if source.hand == nil {
						name := fmt.Sprintf("job-%03d", n)
						f.report(t, f.endJob(t, name, JobDone))
						lines = append(lines, "Job done: "+name+" in demo")
						continue
					}
					lines = append(lines, f.handed(source, n))
				}
				if got := f.mustGet(t, trigger.ID); got.Held() != total || len(got.Taken)+len(got.Ends) != total {
					t.Fatalf("want %d events held and on the account, got %d held, %d on the account", total, got.Held(), len(got.Taken)+len(got.Ends))
				}

				f.clock.advance(2 * time.Hour)
				f.reactor.Tick()
				fired := f.mustGet(t, trigger.ID)
				if fired.State != TriggerDone || fired.Fired != 1 || len(fired.Pending) != 0 || fired.Turn == nil {
					t.Fatalf("want the one shot spent on one turn and nothing left in its window, got %+v", fired)
				}
				turn := fired.Turn.Events
				behind := total - maxTriggerPendingNotes
				if fold := turn[len(turn)-1]; len(turn) != maxTriggerPendingNotes+1 || fold.count() != behind || countEvents(turn) != total {
					t.Fatalf("want %d events whole and %d counted behind them, got %d entries holding %d, the last counting %d", maxTriggerPendingNotes, behind, len(turn), countEvents(turn), fold.count())
				}

				close(held)
				var answer Message
				waitFor(t, "the answer of the one turn", func() bool {
					for _, m := range f.pushed(t) {
						if m.Origin != nil && m.Origin.Trigger == trigger.ID {
							answer = m
						}
					}
					return answer.ID != "" && f.reacting() == 0
				})
				after := f.mustGet(t, trigger.ID)
				if answer.Origin.Count != total || after.Note != fmt.Sprintf("Answered for %d events arrived.", total) {
					t.Fatalf("want the answer and the trigger's line to count %d events, got %d and %q", total, answer.Origin.Count, after.Note)
				}
				turns := f.reactionTurns()
				if len(turns) != 1 || after.Fired != 1 || after.State != TriggerDone || len(after.Pending) != 0 {
					t.Fatalf("want one turn and the one shot done, got %d turns and %+v", len(turns), after)
				}
				prompt := turns[0].Prompt
				for _, want := range []string{
					fmt.Sprintf("%d events arrived inside one window", total),
					fmt.Sprintf("**%d more events arrived than there was room for, counted here without their text**", behind),
				} {
					if !strings.Contains(prompt, want) {
						t.Fatalf("the prompt does not say %q:\n%s", want, prompt)
					}
				}
				named := lines
				if over := total - maxTriggerWindowNotes; over > 0 {
					// The fold has a line for as many headlines as a turn reads
					// events: the ones the window folded itself, then the first
					// of the ones behind the turn.
					last := maxTriggerWindowNotes - over
					named = slices.Concat(lines[:last], lines[maxTriggerWindowNotes:])
					if !strings.Contains(prompt, fmt.Sprintf("\n- %d times: ", over)) {
						t.Fatalf("want the %d events past the fold's lines counted under their kind:\n%s", over, prompt)
					}
				}
				f.wantReadOnce(t, named)
			})
		}
	}
}

// Must 4: once is a bound an edit moves, also while a window waits. A trigger
// that is made a one shot with more waiting than one turn reads whole spends
// all of it on the one turn that is left to it.
func TestATriggerMadeAOneShotSpendsItsWholeWindow(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	source := eventSources[0]
	trigger := f.sourceTrigger(t, source, "sum it up", time.Hour)
	const total = maxTriggerPendingNotes + 10
	var lines []string
	for n := range total {
		lines = append(lines, f.handed(source, n))
	}
	f.edit(t, trigger.ID, TriggerSpec{Once: true, OnceSet: true})

	f.clock.advance(time.Hour)
	f.reactor.Tick()
	answer := f.waitPushed(t, 1)
	after := f.mustGet(t, trigger.ID)
	if answer.Origin.Count != total || after.State != TriggerDone || after.Fired != 1 || len(after.Pending) != 0 {
		t.Fatalf("want one answer for all %d events and the trigger done, got %d and %+v", total, answer.Origin.Count, after)
	}
	if turns := f.reactionTurns(); len(turns) != 1 {
		t.Fatalf("want one turn, got %d", len(turns))
	}
	f.wantReadOnce(t, lines)
}

// Must 2: an event its source hands twice is taken once per trigger, while it
// waits in a window, after a turn read it, and by the next process. A trigger
// that never took it takes it when it is handed again: that is the trigger a
// crash in the middle of the first hand over left out.
func TestAnEventHandedTwiceIsTakenOncePerTrigger(t *testing.T) {
	for _, source := range eventSources {
		t.Run(source.name, func(t *testing.T) {
			f := newEventFixture(t)
			f.create(t)
			waits := f.sourceTrigger(t, source, "SAY_NOTHING", time.Hour)
			fires := f.sourceTrigger(t, source, "SAY_NOTHING", 0)
			want := func(when string, held, fired int) {
				t.Helper()
				f.waitQuiet(t)
				window, spent := f.mustGet(t, waits.ID), f.mustGet(t, fires.ID)
				if window.Held() != held || spent.Fired != fired || spent.Held() != 0 {
					t.Fatalf("%s: want %d waiting and %d fired, got %d waiting, %d fired and %d left behind", when, held, fired, window.Held(), spent.Fired, spent.Held())
				}
			}
			f.handed(source, 1)
			want("handed once", 1, 1)
			f.handed(source, 1)
			want("handed again", 1, 1)

			f = f.restart(t)
			f.handed(source, 1)
			want("handed again to the next process", 1, 1)
			f.handed(source, 2)
			want("another event", 2, 2)

			late := f.sourceTrigger(t, source, "SAY_NOTHING", time.Hour)
			f.handed(source, 2)
			want("handed again past a new trigger", 2, 2)
			if got := f.mustGet(t, late.ID).Held(); got != 1 {
				t.Fatalf("want the trigger that never took the event to take it, got %d", got)
			}
		})
	}
}

func (f *eventFixture) mustGet(t *testing.T, id string) Trigger {
	t.Helper()
	trigger, ok := f.reactor.Get(id)
	if !ok {
		t.Fatalf("the trigger %s is gone", id)
	}
	return trigger
}

// Must 2: the time an event arrives is no identity. Two events of one moment
// are two events, and a signal no record stands behind, a bell, has no name
// at all: it is taken every time it rings.
func TestTheArrivalTimeNamesNoEvent(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	f.reactor.Coder("term-1", CoderKindEnded, "claude/1-11")
	f.reactor.Coder("term-1", CoderKindEnded, "claude/2-11")
	f.reactor.Coder("term-1", CoderKindEnded, "opencode/1-11")
	f.clock.advance(time.Minute)
	f.reactor.Coder("term-1", CoderKindEnded, "claude/1-11")
	if fresh := f.mustGet(t, trigger.ID); fresh.Held() != 3 || len(fresh.Taken) != 3 {
		t.Fatalf("want three events of one moment and none of them again later, got %d waiting, %d on the account", fresh.Held(), len(fresh.Taken))
	}
	f.reactor.Coder("term-1", CoderKindEnded, "")
	f.reactor.Coder("term-1", CoderKindEnded, "")
	if fresh := f.mustGet(t, trigger.ID); fresh.Held() != 5 || len(fresh.Taken) != 3 {
		t.Fatalf("want both bells taken and neither on the account, got %d waiting, %d on the account", fresh.Held(), len(fresh.Taken))
	}
}

// Must 2: what every source names its events by. A name is what a source that
// hands an event again after an update is recognized by, so these stand.
func TestEverySourceNamesItsEvents(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	var got []string
	f.reactor.OnPublish(func(ev CockpitEvent) { got = append(got, ev.ID) })
	for _, source := range eventSources {
		f.handed(source, 7)
	}
	f.reactor.Pipeline(PipelineReport{Kind: PipelineKindSucceeded, Project: "shop", Branch: "main", Head: "a1b2c3", Address: "github.com/team/shop"})
	f.reactor.Pipeline(PipelineReport{Kind: PipelineKindSucceeded, Project: "shop", Branch: "main"})
	f.reactor.ReviewComment(ReviewReport{Project: "shop", Branch: "main", Change: ChangeRequest{Ref: "#4"}})
	f.reactor.Coder("term-1", CoderKindAsks, "")
	want := []string{
		"coder claude/17920713150171007-11",
		"compose run-007 done",
		"approval approval-007 declined",
		"pipeline forge-pipeline-failed #1007 gitlab.example.com/team/shop shop",
		"review 2007 !4 gitlab.example.com/team/shop shop",
		"pipeline forge-pipeline-succeeded @a1b2c3 github.com/team/shop shop",
		"", "", "",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("want the events named\n%q\ngot\n%q", want, got)
	}

	cron := f.sourceTrigger(t, schedule, "SAY_NOTHING", 0)
	f.svc.slots = newCheckSlots(func() int { return 1 })
	f.svc.slots.take()
	f.handed(schedule, 0)
	if turn := f.mustGet(t, cron.ID).Turn; turn == nil || turn.Events[0].ID != "cron "+cron.ID+" 2026-09-18T10:01:00Z" {
		t.Fatalf("want the tick named by its schedule and when it fell due, got %+v", turn)
	}
	f.svc.slots.release()
	f.waitQuiet(t)
}

// Must 3: a tick that fell due is taken once, whenever the process dies. The
// state directory is copied at every look the reactor takes at its clock
// while it ticks, which is every moment between two of its writes, and the
// next process is started over each copy: the tick is read by one turn, the
// one the dead process had bought or the one the next process buys, and the
// schedule never stands past a tick nobody took.
func TestACronTickIsTakenOnceWhereverTheProcessDies(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.svc.slots = newCheckSlots(func() int { return 1 })
	f.svc.slots.take()
	trigger := f.sourceTrigger(t, schedule, "SAY_NOTHING", 0)
	due := trigger.NextAt
	f.clock.advance(time.Minute)

	var deaths []string
	die := func() {
		dir := t.TempDir()
		if err := os.CopyFS(dir, os.DirFS(f.dir)); err != nil {
			t.Fatalf("copy the state: %v", err)
		}
		deaths = append(deaths, dir)
	}
	clock := f.reactor.now
	f.reactor.now = func() time.Time {
		die()
		return clock()
	}
	f.reactor.Tick()
	f.reactor.now = clock
	die()
	if spent := f.mustGet(t, trigger.ID); spent.Fired != 1 || spent.Turn == nil || !spent.NextAt.After(due) {
		t.Fatalf("want the tick fired and the schedule moved on, got %+v", spent)
	}
	if len(deaths) < 3 {
		t.Fatalf("want a death before the tick, one after it and one after the fire, got %d", len(deaths))
	}

	for i, dir := range deaths {
		next := newEventFixtureIn(t, dir, &fakeRunner{answer: eventAnswer})
		next.owner = f.owner
		next.clock.t = f.clock.now()
		died := next.mustGet(t, trigger.ID)
		if moved, holds := died.NextAt.After(due), died.Held() == 1 || died.Turn != nil; moved != holds {
			t.Fatalf("death %d: the schedule moved on is %t, the tick is held is %t", i, moved, holds)
		}
		next.svc.Recover()
		next.reactor.Recover()
		waitFor(t, "the turn about the tick", func() bool {
			fresh := next.mustGet(t, trigger.ID)
			return fresh.Fired == 1 && fresh.Held() == 0 && !fresh.Reacting()
		})
		turns := next.reactionTurns()
		if len(turns) != 1 || !strings.Contains(turns[0].Prompt, "Schedule * * * * * ticked at 10:01 UTC") {
			t.Fatalf("death %d: want one turn about the tick, got %d", i, len(turns))
		}
		if fresh := next.mustGet(t, trigger.ID); !fresh.NextAt.After(next.clock.now()) {
			t.Fatalf("death %d: want the schedule past now, got %s", i, fresh.NextAt)
		}
	}
	f.svc.slots.release()
	f.waitQuiet(t)
}

// Must 4: the bounds a user set refuse as they did, also an event handed
// again: a one shot that is spent, a trigger past its expiry, a trigger that
// was removed. And an edit moves nothing on the account.
func TestTheBoundsOfATriggerRefuseAnEventHandedAgain(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	source := eventSources[0]
	once := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "SAY_NOTHING", BatchSet: true, Once: true})
	expiring := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true, Until: time.Minute})
	removed := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	edited := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	f.handed(source, 1)
	f.waitQuiet(t)
	if err := f.reactor.Remove(removed.ID, ""); err != nil {
		t.Fatalf("remove: %v", err)
	}
	f.edit(t, edited.ID, TriggerSpec{Task: "SAY_NOTHING, edited", Batch: 2 * time.Hour, BatchSet: true})
	f.clock.advance(2 * time.Minute)
	f.reactor.Tick()

	for round := 1; round <= 2; round++ {
		f.handed(source, 1)
		f.waitQuiet(t)
		if fresh := f.mustGet(t, once.ID); fresh.State != TriggerDone || fresh.Fired != 1 || fresh.Held() != 0 {
			t.Fatalf("round %d: want the one shot spent after its one turn, got %+v", round, fresh)
		}
		if fresh := f.mustGet(t, expiring.ID); fresh.State != TriggerExpired || fresh.Fired != 0 || fresh.Held() != 0 {
			t.Fatalf("round %d: want the expired trigger to hold nothing, got %+v", round, fresh)
		}
		if _, ok := f.reactor.Get(removed.ID); ok {
			t.Fatalf("round %d: the removed trigger stands again", round)
		}
		if fresh := f.mustGet(t, edited.ID); fresh.Held() != 1 || fresh.Fired != 0 {
			t.Fatalf("round %d: want the edited trigger to hold its one event, got %d waiting, %d fired", round, fresh.Held(), fresh.Fired)
		}
		f = f.restart(t)
	}
	if len(f.reactionTurns()) != 0 {
		t.Fatal("an event handed again bought a turn")
	}
}

// Must 4: a barrier counts a target once however often its signal is handed,
// and fires one turn when every target arrived.
func TestABarrierCountsASignalHandedAgainOnce(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	barrier := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "SAY_NOTHING", BatchSet: true, Targets: targets("term-a", "term-b"), All: true})
	for range 3 {
		f.reactor.Coder("term-a", CoderKindEnded, "claude/1-11")
	}
	if fresh := f.mustGet(t, barrier.ID); !fresh.Waiting() || fresh.Held() != 1 || fresh.Fired != 0 {
		t.Fatalf("want the barrier waiting for the other target with one event, got %+v", fresh)
	}
	f.reactor.Coder("term-b", CoderKindEnded, "claude/2-11")
	f.reactor.Coder("term-b", CoderKindEnded, "claude/2-11")
	f.reactor.Coder("term-a", CoderKindEnded, "claude/1-11")
	f.waitQuiet(t)
	if fresh := f.mustGet(t, barrier.ID); fresh.Fired != 1 || fresh.Held() != 0 || !fresh.Waiting() {
		t.Fatalf("want one turn for both targets and the barrier waiting again, got %+v", fresh)
	}
	if turns := f.reactionTurns(); len(turns) != 1 || !strings.Contains(turns[0].Prompt, "2 events arrived inside one window") {
		t.Fatalf("want one turn about two events, got %d", len(turns))
	}
}

// A barrier named behind a full window: the trigger took more ends than its
// window holds whole and is then told to wait for two of those terminals,
// one whose end stands whole in the window and one the fold counts. The edit
// hands both ends again, each waits in the window, so neither is missed and
// neither is taken twice, across a restart too. Once a turn spent them they
// are no arrival any more: the same edit again leaves the barrier waiting.
func TestABarrierNamedBehindAFullWindowMissesNoEndThatWaitsThere(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-closed", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	const total = maxTriggerWindowNotes + 3
	for i := range total {
		f.report(t, f.endJob(t, fmt.Sprintf("job-%03d", i), JobDone))
	}
	whole, folded := "term-job-001", fmt.Sprintf("term-job-%03d", total-1)
	barrier := TriggerSpec{Targets: targets(whole, folded), TargetsSet: true, All: true, AllSet: true}
	f.edit(t, trigger.ID, barrier)
	for round := range 2 {
		fresh := f.mustGet(t, trigger.ID)
		if fresh.Waiting() || fresh.Held() != total || len(fresh.Ends) != total {
			t.Fatalf("round %d: want both targets arrived and %d ends held once, got %+v holding %d, %d on the account", round, total, fresh.Targets, fresh.Held(), len(fresh.Ends))
		}
		f = f.restart(t)
		f.reactor.Recover()
	}

	f.clock.advance(2 * time.Hour)
	f.reactor.Tick()
	f.waitQuiet(t)
	fresh := f.mustGet(t, trigger.ID)
	if fresh.Fired != 1 || !fresh.Waiting() || fresh.Held() != maxTriggerWindowNotes-maxTriggerPendingNotes {
		t.Fatalf("want one turn that spent both ends and the barrier waiting again, got fired %d, %+v holding %d", fresh.Fired, fresh.Targets, fresh.Held())
	}
	f.edit(t, trigger.ID, barrier)
	f.edit(t, trigger.ID, TriggerSpec{Targets: targets(folded), TargetsSet: true, All: true, AllSet: true})
	f.edit(t, trigger.ID, barrier)
	if fresh := f.mustGet(t, trigger.ID); !fresh.Waiting() || fresh.Fired != 1 || len(fresh.Ends) != total {
		t.Fatalf("want an end a turn spent to be no arrival, got fired %d and %+v", fresh.Fired, fresh.Targets)
	}
}

// The same for a source that hands an event again on its own: a coder's
// signal whose first copy the fold counts marks its terminal as arrived, and
// so does one that waits whole, each once. A signal a turn spent marks none.
func TestASignalHandedAgainMarksItsTargetWhereverItWaits(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	const total = maxTriggerWindowNotes + 3
	signal := func(n int) {
		terminal := "term-c"
		switch n {
		case 1:
			terminal = "term-a"
		case total - 1:
			terminal = "term-b"
		}
		f.reactor.Coder(terminal, CoderKindEnded, fmt.Sprintf("claude/%d-11", n))
	}
	for n := range total {
		signal(n)
	}
	f.edit(t, trigger.ID, TriggerSpec{Targets: targets("term-a", "term-b"), TargetsSet: true, All: true, AllSet: true})
	met := func() string {
		var out []string
		for _, target := range f.mustGet(t, trigger.ID).Targets {
			if target.Met {
				out = append(out, target.Terminal)
			}
		}
		return strings.Join(out, " ")
	}
	if got := met(); got != "" {
		t.Fatalf("want a target named behind its signal to wait until the signal is handed again, got %q", got)
	}
	signal(total - 1)
	if got := met(); got != "term-b" {
		t.Fatalf("want the signal the fold counts to mark its terminal, got %q", got)
	}
	signal(1)
	fresh := f.mustGet(t, trigger.ID)
	if got := met(); got != "term-a term-b" || fresh.Waiting() || fresh.Held() != total || len(fresh.Taken) != total || fresh.Fired != 0 {
		t.Fatalf("want both terminals arrived and every signal held once, got %q, %d held, %d on the account, fired %d", got, fresh.Held(), len(fresh.Taken), fresh.Fired)
	}

	f.clock.advance(2 * time.Hour)
	f.reactor.Tick()
	f.waitQuiet(t)
	signal(total - 1)
	signal(1)
	if fresh := f.mustGet(t, trigger.ID); fresh.Fired != 1 || met() != "" || fresh.TakenFolded != 0 {
		t.Fatalf("want a signal a turn spent to mark no terminal, got fired %d, %q met, %d of the account in a fold", fresh.Fired, met(), fresh.TakenFolded)
	}
}

// Must 5: the account of the events a trigger took is bounded by a number.
// The newest maxTriggerTakenKeys are remembered, in the same few bytes each
// whatever a source names its events by; one older than that is no longer
// recognized, which is the price of the bound.
func TestTheAccountOfTakenEventsIsBounded(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "forge-pipeline-ended", Project: "shop", Task: "SAY_NOTHING", Batch: time.Hour, BatchSet: true})
	pipeline := func(n int) {
		f.reactor.Pipeline(PipelineReport{
			Kind: PipelineKindFailed, Project: "shop", Branch: "main", Address: "gitlab.example.com/" + strings.Repeat("group/", 40) + "shop", ID: fmt.Sprint(n),
		})
	}
	const total = maxTriggerTakenKeys + 30
	for n := range total {
		pipeline(n)
	}
	fresh := f.mustGet(t, trigger.ID)
	if len(fresh.Taken) != maxTriggerTakenKeys || fresh.Held() != total {
		t.Fatalf("want %d events held and the newest %d on the account, got %d and %d", total, maxTriggerTakenKeys, fresh.Held(), len(fresh.Taken))
	}
	for _, key := range fresh.Taken {
		if len(key) != 16 {
			t.Fatalf("want every entry of the account 16 bytes, got %q", key)
		}
	}
	for n := total - maxTriggerTakenKeys; n < total; n++ {
		pipeline(n)
	}
	if got := f.mustGet(t, trigger.ID).Held(); got != total {
		t.Fatalf("want none of the newest %d taken again, got %d more", maxTriggerTakenKeys, got-total)
	}
	pipeline(0)
	if got := f.mustGet(t, trigger.ID); got.Held() != total+1 || len(got.Taken) != maxTriggerTakenKeys {
		t.Fatalf("want the event the account let go taken again and the account still at its bound, got %d held, %d on the account", got.Held(), len(got.Taken))
	}
}

// writeTriggers puts the triggers file of the fixture's assistant on disk as
// given, the way another binary left it.
func (f *eventFixture) writeTriggers(t *testing.T, triggers any) {
	t.Helper()
	raw, err := json.Marshal(triggers)
	if err != nil {
		t.Fatalf("marshal the triggers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.store.InstanceDir(f.owner.ID), triggersFileName), raw, 0o600); err != nil {
		t.Fatalf("write the triggers: %v", err)
	}
}

// Must 6, the update: the triggers of the releases before carry events
// without a name, a window that stopped at maxTriggerPendingNotes, a schedule
// whose tick fell due while nothing ran, and, written by the last release, a
// turn that waited for a slot. This binary reads them without a step, every
// event they held is read by one turn, and a second start finds nothing left.
func TestTheWindowsOfTheReleasesBeforeAreSpentOnceAfterTheUpdate(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	at := func(minute int) string {
		return f.clock.now().Add(time.Duration(minute) * time.Minute).Format(time.RFC3339Nano)
	}
	event := func(source, kind, headline string) map[string]any {
		return map[string]any{"source": source, "kind": kind, "time": at(1), "headline": headline, "body": "what it said"}
	}
	trigger := func(id, source, kind string, more map[string]any) map[string]any {
		entry := map[string]any{
			"id": id, "source": source, "kind": kind, "task": "SAY_NOTHING", "batchSeconds": 30,
			"state": TriggerStanding, "createdAt": at(0), "updatedAt": at(1), "seenAt": at(1),
		}
		for key, value := range more {
			entry[key] = value
		}
		return entry
	}
	var lines []string
	window := func(source, kind, what string, n int) []map[string]any {
		var out []map[string]any
		for i := range n {
			line := fmt.Sprintf("%s %03d", what, i)
			lines = append(lines, line)
			out = append(out, event(source, kind, line))
		}
		return out
	}
	f.writeTriggers(t, []map[string]any{
		trigger("coder-window", EventCoder, CoderKindNews, map[string]any{
			"pending": window(EventCoder, CoderKindEnded, "Coder ended its turn: old", maxTriggerPendingNotes), "batchDue": at(2),
		}),
		trigger("forge-window", EventForge, PipelineKindEnded, map[string]any{
			"project": "shop", "pending": window(EventForge, PipelineKindFailed, "Pipeline failed: old", 3), "batchDue": at(2),
		}),
		trigger("compose-turn", EventCompose, ComposeKindEnded, map[string]any{
			"batchSeconds": 0, "fired": 1, "reactingSince": at(1),
			"turn": map[string]any{"messageId": "0123456789abcdef0123456789abcdef", "events": window(EventCompose, ComposeKindDone, "Compose done: old", 2)},
		}),
		trigger("schedule", EventCron, CronKindTick, map[string]any{
			"spec": "* * * * *", "timezone": "UTC", "batchSeconds": 0, "nextAt": at(5),
		}),
	})
	lines = append(lines, "Schedule * * * * * ticked at 10:05 UTC")
	f.clock.advance(10 * time.Minute)

	for round := 1; round <= 2; round++ {
		next := newEventFixtureIn(t, f.dir, &fakeRunner{answer: eventAnswer})
		next.owner = f.owner
		next.clock.t = f.clock.now()
		next.svc.Recover()
		next.reactor.Recover()
		next.waitQuiet(t)
		if round == 1 {
			next.wantReadOnce(t, lines)
		} else if turns := next.reactionTurns(); len(turns) != 0 {
			t.Fatalf("the second start bought %d turns", len(turns))
		}
		for _, trigger := range next.reactor.ListOf(f.owner.ID) {
			if trigger.Fired != 1 || trigger.Held() != 0 || trigger.Reacting() {
				t.Fatalf("start %d: want %s at rest after its one turn, got %+v", round, trigger.ID, trigger)
			}
		}
	}
}

// Must 6, the rollback: the release before knows neither the fold nor the
// names of events. What this binary writes is read by it as a list of events
// like any other, the fold as one more with a headline that counts and a
// text that names, and its rule spends a window of any length in turns of
// maxTriggerPendingNotes. Its first write drops the fields it does not know;
// this binary then reads the fold as the event it was written out as, and
// every event is still read by a turn.
func TestTheReleaseBeforeReadsAFullWindowAsEvents(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	source := eventSources[0]
	trigger := f.sourceTrigger(t, source, "SAY_NOTHING", time.Hour)
	var lines []string
	for n := range maxTriggerWindowNotes + 4 {
		lines = append(lines, f.handed(source, n))
	}

	// The shapes of the release before, every field it reads of a window.
	type oldEvent struct {
		Source   string    `json:"source"`
		Kind     string    `json:"kind"`
		Target   string    `json:"target,omitempty"`
		Time     time.Time `json:"time"`
		Headline string    `json:"headline"`
		Body     string    `json:"body,omitempty"`
	}
	path := filepath.Join(f.store.InstanceDir(f.owner.ID), triggersFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the triggers: %v", err)
	}
	var written []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &written); err != nil || len(written) != 1 {
		t.Fatalf("parse the triggers: %v", err)
	}
	var window []oldEvent
	if err := json.Unmarshal(written[0]["pending"], &window); err != nil {
		t.Fatalf("the release before cannot read the window: %v", err)
	}
	if len(window) != maxTriggerWindowNotes+1 {
		t.Fatalf("want the release before to read %d events, got %d", maxTriggerWindowNotes+1, len(window))
	}
	read := ""
	for _, ev := range window {
		read += ev.Headline + "\n" + ev.Body + "\n"
	}
	for _, line := range lines {
		if strings.Count(read, line) != 1 {
			t.Fatalf("the release before does not read %q once", line)
		}
	}
	if fold := window[maxTriggerWindowNotes]; !strings.HasPrefix(fold.Headline, "4 more events arrived") {
		t.Fatalf("want the fold to count in its headline, got %q", fold.Headline)
	}

	// Its first write of the entry, and this binary over that.
	rewritten, err := json.Marshal(window)
	if err != nil {
		t.Fatalf("marshal the window: %v", err)
	}
	written[0]["pending"] = rewritten
	delete(written[0], "taken")
	f.writeTriggers(t, written)
	f = f.restart(t)
	f.clock.advance(2 * time.Hour)
	f.reactor.Recover()
	waitFor(t, "the turns about the window", func() bool {
		fresh := f.mustGet(t, trigger.ID)
		return fresh.Fired == 3 && fresh.Held() == 0 && !fresh.Reacting()
	})
	f.wantReadOnce(t, lines)
}
