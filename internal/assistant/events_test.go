package assistant

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/detach"
	"github.com/marein/dev-cockpit/internal/statefile"
)

// fakeClock is the reactor's clock in these tests, so a batch window, a cap
// and an expiry are crossed by moving it instead of waiting.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// eventAnswer is the coder of these tests: a reaction answers under the
// contract, NOTHING when its task says so, and a chat turn answers as a chat
// turn.
func eventAnswer(req TurnRequest) []Event {
	if strings.Contains(req.Prompt, "This turn was started by the cockpit") {
		if strings.Contains(req.Prompt, "SAY_NOTHING") {
			return []Event{{Kind: EventDelta, Text: "NOTHING"}}
		}
		// The incident of 2026-09-22, word for word: the reaction decided
		// right and put its NOTHING behind the sentence that got it there.
		if strings.Contains(req.Prompt, "TALK_FIRST") {
			return []Event{{Kind: EventDelta, Text: "32 ist gerade, also nichts ausgeben.\n\nNOTHING"}}
		}
		return []Event{{Kind: EventDelta, Text: "event answer"}}
	}
	return []Event{{Kind: EventDelta, Text: "chat answer"}}
}

type eventFixture struct {
	svc     *Service
	store   *Store
	dir     string
	runner  *fakeRunner
	owner   Instance
	clock   *fakeClock
	reactor *Reactor
	news    chan string
}

func newEventFixture(t *testing.T) *eventFixture {
	t.Helper()
	return newEventFixtureIn(t, t.TempDir(), &fakeRunner{answer: eventAnswer})
}

// newEventFixtureIn builds the fixture over a state directory a test names,
// so a restart can be played over the same disk.
func newEventFixtureIn(t *testing.T, dir string, runner *fakeRunner) *eventFixture {
	t.Helper()
	svc, store, _, _ := newTestServiceIn(t, dir, runner)
	news := make(chan string, 16)
	svc.SetHooks(func() {}, func(id string) { news <- id })
	clock := &fakeClock{t: time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)}
	svc.events.now = clock.now
	return &eventFixture{svc: svc, store: store, dir: dir, runner: runner, clock: clock, reactor: svc.events, news: news}
}

func (f *eventFixture) create(t *testing.T) Instance {
	t.Helper()
	owner, err := f.svc.create("claude")
	if err != nil {
		t.Fatalf("create the assistant: %v", err)
	}
	f.owner = owner
	return owner
}

// steerJob writes an open job of the owner straight into the store, the way
// the watcher leaves one. A job target of a trigger has to carry one.
func (f *eventFixture) steerJob(t *testing.T, name string) string {
	t.Helper()
	NewJobs(f.store).Of(f.owner.ID).Save(Job{
		Terminal: "term-" + name, Name: name, Project: "demo", State: JobSteering,
		CreatedAt: f.clock.now(), UpdatedAt: f.clock.now(),
	})
	return "term-" + name
}

// closeJob is that job ending, what a check's report leaves behind.
func (f *eventFixture) closeJob(t *testing.T, name string) {
	t.Helper()
	NewJobs(f.store).Of(f.owner.ID).Save(Job{
		Terminal: "term-" + name, Name: name, Project: "demo", State: JobDone,
		Note: "the job " + name + " is finished", CreatedAt: f.clock.now(),
		UpdatedAt: f.clock.now().Add(time.Minute),
	})
}

// targets is the terminals a spec names, the way a caller hands them over.
func targets(terminals ...string) []TriggerTarget {
	out := make([]TriggerTarget, 0, len(terminals))
	for _, terminal := range terminals {
		out = append(out, TriggerTarget{Terminal: terminal})
	}
	return out
}

func (f *eventFixture) trigger(t *testing.T, spec TriggerSpec) Trigger {
	t.Helper()
	spec.Owner = f.owner.ID
	trigger, err := f.reactor.Add(spec)
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	return trigger
}

func (f *eventFixture) jobDone(name string) CockpitEvent {
	return CockpitEvent{
		Source:   EventJob,
		Kind:     string(VerdictDone),
		Target:   "term-" + name,
		Owner:    f.owner.ID,
		Time:     f.clock.now(),
		Headline: "Job done: " + name,
		Body:     "the job " + name + " is finished",
	}
}

// notes are the cockpit's messages in the owner's thread.
func (f *eventFixture) notes(t *testing.T) []Message {
	t.Helper()
	c, err := f.svc.Get(f.owner.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var out []Message
	for _, m := range c.Messages {
		if m.IsNote() {
			out = append(out, m)
		}
	}
	return out
}

func (f *eventFixture) waitNotes(t *testing.T, want int) []Message {
	t.Helper()
	var notes []Message
	waitFor(t, "the notes", func() bool {
		notes = f.notes(t)
		return len(notes) >= want
	})
	return notes
}

// pushed are the answers reactions pushed into the owner's thread.
func (f *eventFixture) pushed(t *testing.T) []Message {
	t.Helper()
	c, err := f.svc.Get(f.owner.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var out []Message
	for _, m := range c.Messages {
		if m.Role == RoleAssistant && m.Auto {
			out = append(out, m)
		}
	}
	return out
}

// waitPushed waits until a reaction pushed its answer and nothing reacts any
// more, and answers the last pushed message.
func (f *eventFixture) waitPushed(t *testing.T, want int) Message {
	t.Helper()
	var out []Message
	waitFor(t, "the reaction to push its answer", func() bool {
		out = f.pushed(t)
		return len(out) >= want && f.reacting() == 0
	})
	return out[len(out)-1]
}

// waitQuiet waits until no reaction runs any more.
func (f *eventFixture) waitQuiet(t *testing.T) {
	t.Helper()
	waitFor(t, "the reactions to end", func() bool { return f.reacting() == 0 })
}

// reacting is how many of the owner's triggers say a reaction runs.
func (f *eventFixture) reacting() int {
	n := 0
	for _, trigger := range f.reactor.ListOf(f.owner.ID) {
		if trigger.Reacting() {
			n++
		}
	}
	return n
}

// reactionTurns are the turns of the runner that were reactions.
func (f *eventFixture) reactionTurns() []TurnRequest {
	var out []TurnRequest
	for _, req := range f.runner.turns() {
		if strings.Contains(req.Prompt, "This turn was started by the cockpit") {
			out = append(out, req)
		}
	}
	return out
}

func TestAReactionRunsInASessionOfItsOwnAndPushesItsAnswer(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize what is done", BatchSet: true})
	// The provider holds the session the reaction created, so the drop at
	// its end is a real delete and not only a released reservation.
	f.runner.mu.Lock()
	f.runner.exists = true
	f.runner.mu.Unlock()

	f.reactor.Publish(f.jobDone("readme"))
	answer := f.waitPushed(t, 1)
	if !answer.Auto || answer.Content != "event answer" || answer.State != StateComplete || answer.Error != "" || answer.Role != RoleAssistant {
		t.Fatalf("want the answer pushed as one started without the user, got %+v", answer)
	}
	if answer.Origin == nil || answer.Origin.Source != NoteEvent || answer.Origin.Headline != "Job done: readme" || answer.Origin.Task != "summarize what is done" || answer.Origin.Trigger != trigger.ID || answer.Origin.Count != 1 {
		t.Fatalf("want the origin on the pushed answer, got %+v", answer.Origin)
	}
	// Nothing was written into the chat session for it: the thread holds the
	// pushed answer and nothing else, no user turn, no note.
	c, _ := f.svc.Get(f.owner.ID)
	if len(c.Messages) != 1 {
		t.Fatalf("want the pushed answer alone in the thread, got %+v", c.Messages)
	}
	turns := f.reactionTurns()
	if len(turns) != 1 {
		t.Fatalf("want one reaction turn, got %d", len(turns))
	}
	req := turns[0]
	// A session of its own, in the owner's workspace, named as a reaction,
	// and gone again once the turn ended.
	if req.SessionID == f.owner.NativeSessionID || req.Resume {
		t.Fatalf("want a session of its own, got %+v", req)
	}
	if req.Workdir != mustWorkdir(t, f.svc, f.owner.ID) || req.Instance != f.owner.ID {
		t.Fatalf("want the reaction in the owner's workspace, got %+v", req)
	}
	if !IsCheckSession(req.Title) || !strings.HasPrefix(req.Title, "cockpit reaction: ") {
		t.Fatalf("want the session named as a reaction, got %q", req.Title)
	}
	f.runner.mu.Lock()
	dropped := append([]string(nil), f.runner.deleted...)
	f.runner.mu.Unlock()
	if len(dropped) != 1 || dropped[0] != req.SessionID {
		t.Fatalf("want the reaction's session dropped when it ended, got %v", dropped)
	}
	if f.svc.Reserved("claude", req.SessionID) {
		t.Fatal("want the reservation released with the session")
	}
	for _, want := range []string{"This turn was started by the cockpit", "session of your own", "Event: Job done: readme", "the job readme is finished", "Your task for this event: summarize what is done", "answer NOTHING on the first line",
		// The instruction file no longer says any of this: it is read by
		// every turn of every kind, and only a reaction is bound by it.
		"your answer is pushed into your thread", "then nothing is pushed and nobody is notified",
		// The first line is the message the user gets, so the prompt carries
		// the rule the check's prompt carries, and it says that the verdicts
		// of a check are not this turn's: a reaction reads the same
		// instruction file a check reads.
		"Your whole answer starts with the result", "Do not announce what you are about to do",
		"DONE, BLOCKED and WORKING belong to a check", "Write in the language the task is written in",
		// A reaction has the two hours a check has and no next turn behind
		// it, so a reaction that works up to the deadline is killed and
		// reaches the thread as one that broke off.
		"This turn has two hours", "nothing carries on after it"} {
		if !strings.Contains(req.Prompt, want) {
			t.Fatalf("the prompt does not say %q:\n%s", want, req.Prompt)
		}
	}
	select {
	case id := <-f.news:
		if id != f.owner.ID {
			t.Fatalf("news for %s, want %s", id, f.owner.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a pushed answer with words in it is news")
	}
	fresh, _ := f.reactor.Get(trigger.ID)
	if fresh.Fired != 1 || !fresh.Open() || fresh.Reacting() || fresh.Note != "Answered for Job done: readme." {
		t.Fatalf("want the trigger standing, fired once and answered, got %+v", fresh)
	}
}

// A CLI that does not know the model a reaction runs on is a named refusal,
// and the sentence says where the model was set: on the trigger where the
// trigger picked its own, at the ring button where it followed the assistant's.
// It stands under the pushed message and on the trigger's own line alike.
// The name is longModel, forty runes the redaction of a quoted line would
// take for a token, standing whole because the sentence is the cockpit's.
func TestARefusedReactionSaysWhereToPickTheModel(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventError, Err: UnknownModel(longModel)}}}
	f := newEventFixtureIn(t, t.TempDir(), runner)
	f.create(t)
	own := f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize", BatchSet: true, Model: longModel, ModelSet: true})

	f.reactor.Publish(f.jobDone("readme"))
	answer := f.waitPushed(t, 1)
	want := "The coder does not know the model " + longModel + ". Pick another on the trigger."
	if answer.State != StateFailed || answer.Error != want {
		t.Fatalf("want the refusal under the pushed message, got %+v", answer)
	}
	fresh, _ := f.reactor.Get(own.ID)
	if !fresh.Broke || !strings.Contains(fresh.Note, want) {
		t.Fatalf("want the trigger marked broken with the refusal on its line, got %+v", fresh)
	}
	if err := f.reactor.Remove(own.ID, ""); err != nil {
		t.Fatalf("remove: %v", err)
	}

	following := f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize", BatchSet: true})
	f.reactor.Publish(f.jobDone("readme"))
	answer = f.waitPushed(t, 2)
	if answer.Error != "The coder does not know the model "+longModel+". Pick another at the ring button of this assistant." {
		t.Fatalf("want the ring named where the trigger followed the assistant's model, got %q", answer.Error)
	}
	if fresh, _ := f.reactor.Get(following.ID); !fresh.Broke {
		t.Fatalf("want the trigger marked broken, got %+v", fresh)
	}
}

// A reaction that stops before it is done goes the way a finished one goes:
// the thread, the news, the phone, only marked as the turn it was. What it
// had written is the message, the sentence under it says why it stopped, and
// the origin stays on it, so what the user gets still reads as a trigger's
// and not as an answer somebody asked for.
func TestAReactionThatBrokeOffIsPushedLikeAnAnswer(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "half an answer"}}, unfinished: true}
	f := newEventFixtureIn(t, t.TempDir(), runner)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize what is done", BatchSet: true})

	f.reactor.Publish(f.jobDone("readme"))
	answer := f.waitPushed(t, 1)
	if answer.State != StateFailed || answer.Content != "half an answer" || answer.Error == "" {
		t.Fatalf("want what the turn had written, under a failed state with the sentence that says why, got %+v", answer)
	}
	if !answer.Auto || answer.Origin == nil || answer.Origin.Source != NoteEvent || answer.Origin.Headline != "Job done: readme" || answer.Origin.Trigger != trigger.ID {
		t.Fatalf("want the origin kept, so the message still reads as a trigger's, got %+v", answer.Origin)
	}
	// This is the message a notification is written from, and the two facts
	// its "Trigger broke off" title stands on are exactly these two.
	last, ok := f.svc.LastAnswer(f.owner.ID)
	if !ok || last.ID != answer.ID || last.Origin == nil || (last.State != StateFailed && last.State != StateInterrupted) {
		t.Fatalf("want the turn that broke off to be what the notification reads, got %+v", last)
	}
	select {
	case id := <-f.news:
		if id != f.owner.ID {
			t.Fatalf("news for %s, want %s", id, f.owner.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a turn that broke off is news like any other")
	}
	fresh, _ := f.reactor.Get(trigger.ID)
	if fresh.Fired != 1 || fresh.Reacting() || !strings.HasPrefix(fresh.Note, "The reaction for Job done: readme broke off: ") {
		t.Fatalf("want the trigger counted as fired and its line saying it broke off, got %+v", fresh)
	}
}

func TestAReactionNeverTakesTheChatSlot(t *testing.T) {
	runner := &fakeRunner{answer: eventAnswer, hold: func(req TurnRequest) chan struct{} {
		if strings.Contains(req.Prompt, "This turn was started by the cockpit") {
			return nil
		}
		return make(chan struct{})
	}}
	f := newEventFixtureIn(t, t.TempDir(), runner)
	f.create(t)
	f.trigger(t, TriggerSpec{Event: "job-done", Task: "carry on", BatchSet: true})

	// The chat turn holds the assistant's slot and never lets go; the
	// reaction runs beside it and its answer is pushed while the chat turn
	// still streams.
	if _, err := f.svc.Send(f.owner.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, "the chat turn to start", func() bool { return len(runner.turns()) == 1 })
	f.reactor.Publish(f.jobDone("readme"))
	answer := f.waitPushed(t, 1)
	if !f.svc.Running(f.owner.ID) {
		t.Fatal("the chat turn must still be running")
	}
	c, _ := f.svc.Get(f.owner.ID)
	if len(queuedMessages(c)) != 0 {
		t.Fatalf("a reaction queues nothing behind the chat turn, got %+v", queuedMessages(c))
	}
	if !answer.Auto || answer.Content != "event answer" {
		t.Fatalf("want the answer pushed beside the running chat turn, got %+v", answer)
	}
	// In the transcript the pushed answer stands after the streaming chat
	// placeholder, where it arrived.
	if last, _ := c.Last(); last.ID != answer.ID {
		t.Fatalf("want the pushed answer last, got %+v", c.Messages)
	}
}

// One trigger reacts once at a time. What arrives while its reaction runs
// waits in the window, however short that window is, and the end of the
// reaction spends it as one turn: the answers stay in order and the thread
// hears about each event exactly once.
func TestATriggerReactsOnceAtATime(t *testing.T) {
	held := make(chan struct{})
	runner := &fakeRunner{answer: eventAnswer, hold: func(req TurnRequest) chan struct{} {
		if strings.Contains(req.Prompt, "the job first is finished") {
			return held
		}
		return nil
	}}
	f := newEventFixtureIn(t, t.TempDir(), runner)
	f.create(t)
	// No window at all: without the rule below every event would be its own
	// reaction the moment it arrives.
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "hand out the next one", BatchSet: true})

	f.reactor.Publish(f.jobDone("first"))
	waitFor(t, "the first reaction to run", func() bool {
		fresh, _ := f.reactor.Get(trigger.ID)
		return fresh.Reacting()
	})
	f.reactor.Publish(f.jobDone("second"))
	f.reactor.Publish(f.jobDone("third"))
	// Long enough for a second reaction to have started if the rule were not
	// there: the publish path fires without a window by itself.
	time.Sleep(50 * time.Millisecond)
	if turns := f.reactionTurns(); len(turns) != 1 {
		t.Fatalf("want one reaction while one runs, got %d", len(turns))
	}
	waiting, _ := f.reactor.Get(trigger.ID)
	if len(waiting.Pending) != 2 || waiting.Fired != 1 {
		t.Fatalf("want both events waiting under the running reaction, got %+v", waiting)
	}

	close(held)
	f.waitPushed(t, 2)
	if len(f.reactionTurns()) != 2 {
		t.Fatalf("want the two waiting events folded into one turn, got %d reactions", len(f.reactionTurns()))
	}
	answers := f.pushed(t)
	first, second := answers[0], answers[1]
	if first.Origin == nil || first.Origin.Headline != "Job done: first" || first.Origin.Count != 1 {
		t.Fatalf("want the first answer for the first event, got %+v", first.Origin)
	}
	if second.Origin == nil || second.Origin.Headline != "2 events arrived" || second.Origin.Count != 2 {
		t.Fatalf("want the held events as one origin, got %+v", second.Origin)
	}
	body := f.reactionTurns()[1].Prompt
	if !strings.Contains(body, "the job second is finished") || !strings.Contains(body, "the job third is finished") {
		t.Fatalf("want both held events in the one prompt, got %q", body)
	}
	if strings.Index(body, "the job second is finished") > strings.Index(body, "the job third is finished") {
		t.Fatal("want the held events in the order they arrived")
	}
	done, _ := f.reactor.Get(trigger.ID)
	if done.Fired != 2 || len(done.Pending) != 0 || done.Reacting() || !done.Open() {
		t.Fatalf("want the trigger standing, fired twice and empty, got %+v", done)
	}
}

// The window a running reaction holds open is on disk like everything else a
// trigger carries. A process that ends mid reaction leaves it there, and the
// next one spends it when the reaction it waited for is concluded.
func TestEventsHeldUnderARunningReactionSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "event answer"}}, block: make(chan struct{})}
	f := newEventFixtureIn(t, dir, runner)
	owner := f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "hand out the next one", BatchSet: true})

	// A reaction nobody follows, the way the first process leaves one behind,
	// and the trigger as its fire wrote it: reacting, spent once.
	ev := f.jobDone("first")
	rec := orphanReaction(t, f, trigger, ev)
	f.reactor.triggers.Of(owner.ID).Update(trigger.ID, func(s *Trigger) bool {
		s.Fired = 1
		s.LastFiredAt = f.clock.now()
		s.ReactingSince = f.clock.now()
		return true
	})
	f.reactor.Publish(f.jobDone("second"))
	f.reactor.Publish(f.jobDone("third"))
	if turns := f.reactionTurns(); len(turns) != 1 {
		t.Fatalf("want nothing to fire under the running reaction, got %d", len(turns))
	}

	// The next process over the same directory reads the window off the disk.
	second := newEventFixtureIn(t, dir, runner)
	second.owner = owner
	held, _ := second.reactor.Get(trigger.ID)
	if len(held.Pending) != 2 || !held.Reacting() {
		t.Fatalf("want the held events and the running reaction on disk, got %+v", held)
	}
	second.svc.Recover()
	close(runner.block)

	answer := second.waitPushed(t, 2)
	if answer.Origin == nil || answer.Origin.Headline != "2 events arrived" || answer.ID == rec.MessageID {
		t.Fatalf("want the held events answered after the recovery, got %+v", answer)
	}
	after, _ := second.reactor.Get(trigger.ID)
	if after.Fired != 2 || len(after.Pending) != 0 || after.Reacting() {
		t.Fatalf("want the window spent after the recovery, got %+v", after)
	}
}

func TestABatchWindowFoldsEventsIntoOneReaction(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.trigger(t, TriggerSpec{Event: "job-closed", Task: "hand out the next one", Batch: 30 * time.Second, BatchSet: true})

	f.reactor.Publish(f.jobDone("first"))
	f.clock.advance(5 * time.Second)
	f.reactor.Publish(f.jobDone("second"))
	f.reactor.Tick()
	if len(f.pushed(t)) != 0 || len(f.runner.turns()) != 0 {
		t.Fatal("the window is open, nothing may fire yet")
	}

	f.clock.advance(26 * time.Second)
	f.reactor.Tick()
	answer := f.waitPushed(t, 1)
	if answer.Origin.Count != 2 || answer.Origin.Headline != "2 events arrived" {
		t.Fatalf("want one answer for two events, got %+v", answer.Origin)
	}
	turns := f.reactionTurns()
	if len(turns) != 1 {
		t.Fatalf("want one reaction, got %d", len(turns))
	}
	for _, want := range []string{"2 events arrived inside one window", "**Job done: first**", "**Job done: second**"} {
		if !strings.Contains(turns[0].Prompt, want) {
			t.Fatalf("the prompt does not carry %q:\n%s", want, turns[0].Prompt)
		}
	}
}

// An expiry ends a trigger on the trigger's own line: the events it held are
// spent, the state says expired and the thread hears nothing of it, because
// nobody asked for a turn that was never bought.
func TestAnExpiryRefusesOnTheTriggerAlone(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "carry on", Until: time.Hour, BatchSet: true})

	f.clock.advance(61 * time.Minute)
	f.reactor.Publish(f.jobDone("one"))
	waitFor(t, "the refusal on the trigger", func() bool {
		fresh, _ := f.reactor.Get(trigger.ID)
		return strings.Contains(fresh.Note, "No turn for Job done: one")
	})
	fresh, _ := f.reactor.Get(trigger.ID)
	if !strings.Contains(fresh.Note, "the trigger expired") || fresh.State != TriggerExpired || fresh.Fired != 0 {
		t.Fatalf("want the expiry's refusal on the trigger's line, got %+v", fresh)
	}
	// The thread hears nothing of a refused turn.
	c, _ := f.svc.Get(f.owner.ID)
	if len(c.Messages) != 0 || len(f.reactionTurns()) != 0 {
		t.Fatalf("a refused turn shows in the trigger's state alone, got %+v", c.Messages)
	}
	select {
	case <-f.news:
		t.Fatal("a refused turn rings nobody")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAOnceTriggerEndsAfterItsFirstReaction(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "once", Once: true, BatchSet: true})

	f.reactor.Publish(f.jobDone("one"))
	f.waitPushed(t, 1)
	fresh, _ := f.reactor.Get(trigger.ID)
	if fresh.State != TriggerDone {
		t.Fatalf("want a one shot done after its reaction, got %q", fresh.State)
	}
	f.reactor.Publish(f.jobDone("two"))
	time.Sleep(50 * time.Millisecond)
	if len(f.pushed(t)) != 1 || len(f.runner.turns()) != 1 {
		t.Fatal("a one shot that fired takes nothing more")
	}
}

func TestAnExpiredTriggerEndsOnItsLineAlone(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "later", Until: time.Hour, BatchSet: true})

	f.clock.advance(2 * time.Hour)
	f.reactor.Tick()
	fresh, _ := f.reactor.Get(trigger.ID)
	if fresh.State != TriggerExpired || !strings.Contains(fresh.Note, "Expired") {
		t.Fatalf("want the trigger expired on its line, got %+v", fresh)
	}
	c, _ := f.svc.Get(f.owner.ID)
	if len(c.Messages) != 0 {
		t.Fatalf("an expiry writes nothing into the thread, got %+v", c.Messages)
	}
	f.reactor.Publish(f.jobDone("late"))
	time.Sleep(50 * time.Millisecond)
	if len(f.runner.turns()) != 0 {
		t.Fatal("an expired trigger buys no turn")
	}
}

func TestANothingAnswerPushesNothingAndRingsNobody(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "SAY_NOTHING when there is nothing", BatchSet: true})

	f.reactor.Publish(f.jobDone("quiet"))
	waitFor(t, "the reaction to answer", func() bool { return len(f.reactionTurns()) == 1 })
	f.waitQuiet(t)
	c, _ := f.svc.Get(f.owner.ID)
	if len(c.Messages) != 0 {
		t.Fatalf("a NOTHING answer pushes nothing, got %+v", c.Messages)
	}
	select {
	case <-f.news:
		t.Fatal("a NOTHING answer rings nobody")
	case <-time.After(200 * time.Millisecond):
	}
	fresh, _ := f.reactor.Get(trigger.ID)
	if fresh.Fired != 1 || fresh.Note != "Fired for Job done: quiet, nothing to do." {
		t.Fatalf("want the reaction counted as fired and its line saying nothing to do, got %+v", fresh)
	}
}

// The quiet contract is NOTHING and nothing else, and it is read with the
// check's own parseVerdict, so a reaction gets the tolerance a check has had
// all along: a model that talks first and writes its NOTHING behind the
// preamble decided the same thing as one that answers it bare, and the one
// that rang the user anyway is the incident this reading ends. The tolerance
// stops where an answer is left over: a reaction that carries the word into
// something it wrote for the user is pushed with it.
// The whole way through, not the reading alone: a reaction that thinks out
// loud and answers NOTHING in its last line is one nobody hears about. It
// used to be pushed into the thread and rung on the user's phone, which is
// the incident this pins.
func TestAPreambleInFrontOfNothingPushesNothingAndRingsNobody(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "TALK_FIRST: say the time on odd minutes only", BatchSet: true})

	f.reactor.Publish(f.jobDone("quiet"))
	waitFor(t, "the reaction to answer", func() bool { return len(f.reactionTurns()) == 1 })
	f.waitQuiet(t)
	c, _ := f.svc.Get(f.owner.ID)
	if len(c.Messages) != 0 {
		t.Fatalf("a NOTHING behind a preamble pushes nothing, got %+v", c.Messages)
	}
	select {
	case <-f.news:
		t.Fatal("a NOTHING behind a preamble rings nobody")
	case <-time.After(200 * time.Millisecond):
	}
	fresh, _ := f.reactor.Get(trigger.ID)
	if fresh.Fired != 1 || fresh.Note != "Fired for Job done: quiet, nothing to do." {
		t.Fatalf("want the reaction counted as fired and its line saying nothing to do, got %+v", fresh)
	}
}

func TestQuietAnswerTakesAPreambleAndNoRealAnswer(t *testing.T) {
	for text, want := range map[string]bool{
		// The incident, word for word: the reaction decided right and only
		// said so in its third line.
		"32 ist gerade, also nichts ausgeben.\n\nNOTHING": true,
		"NOTHING":            true,
		"nothing.":           true,
		"**NOTHING**":        true,
		"\n\nNOTHING:\n":     true,
		"All done.\nNOTHING": true,
		// A real answer that happens to carry the word is no contract: what
		// stands behind it is what the user is owed.
		"NOTHING to do here, all is well.":                              false,
		"The check ran and found NOTHING wrong with the release build.": false,
		// A verdict of a check is not this contract either, and a bare
		// NOTHING beside one never cuts a report in half.
		"The build failed. BLOCKED on the token, NOTHING else moves it": false,
		// A turn that wrote nothing at all said nothing, which is not the
		// same as saying NOTHING.
		"": false,
	} {
		if got := quietAnswer(text); got != want {
			t.Fatalf("quietAnswer(%q) = %v, want %v", text, got, want)
		}
	}
}

// The summary in front of a chat prompt names what the cockpit wrote since
// the last chat answer, notes and pushed answers alike: the count, where to
// read it, and one headline per line, newest first. The walk stops only at a
// chat answer, never at a pushed one.
func TestTheNextChatPromptNamesWhatTheCockpitWrote(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	c, _ := f.svc.Get(f.owner.ID)
	now := time.Now().UTC()
	c.Messages = append(c.Messages,
		Message{ID: "u1", Role: RoleUser, Content: "earlier", CreatedAt: now, State: StateComplete},
		Message{ID: "a1", Role: RoleAssistant, Content: "the chat answer", CreatedAt: now, State: StateComplete},
		Message{ID: "n1", Role: RoleCockpit, Content: "the README is written", CreatedAt: now, State: StateComplete, Note: &Note{Source: NoteCheck, Headline: "DONE: readme-task", Verdict: "done"}},
		Message{ID: "p1", Role: RoleAssistant, Content: "the pushed answer", CreatedAt: now, State: StateComplete, Auto: true, Origin: &Note{Source: NoteEvent, Headline: "Job done: readme-task", Task: "summarize"}},
		Message{ID: "n2", Role: RoleCockpit, Content: "it needs a decision", CreatedAt: now, State: StateComplete, Note: &Note{Source: NoteCheck, Headline: "BLOCKED: tests-task", Verdict: "blocked"}},
	)
	f.store.Save(c)
	if _, err := f.svc.Send(f.owner.ID, "how are the jobs", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, f.svc, f.owner.ID)
	prompt := f.runner.turns()[0].Prompt
	want := "Since your last answer the cockpit wrote 3 notes into this thread, newest first. job-list and assistant-show on yourself say what, look only when the user's message needs it.\n" +
		"- BLOCKED: tests-task\n" +
		"- Job done: readme-task\n" +
		"- DONE: readme-task\n" +
		"\nhow are the jobs"
	if prompt != want {
		t.Fatalf("want the one line and the message, got:\n%s", prompt)
	}

	// The next turn starts after that chat answer, so nothing is counted twice.
	if _, err := f.svc.Send(f.owner.ID, "and now", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, f.svc, f.owner.ID)
	if prompt := f.runner.turns()[1].Prompt; prompt != "and now" {
		t.Fatalf("want the plain prompt once the notes were counted, got %q", prompt)
	}
	// One note reads as one.
	one := []noteLine{{headline: "DONE: readme-task", count: 1}}
	if got := withNotes(one, "x"); !strings.HasPrefix(got, "Since your last answer the cockpit wrote 1 note into this thread, newest first.") {
		t.Fatalf("want the singular, got %q", got)
	}
	// Nothing to say puts no line in front of the prompt at all.
	if got := withNotes(nil, "x"); got != "x" {
		t.Fatalf("want the bare prompt without notes, got %q", got)
	}
}

// A schedule that fires every half hour writes the same headline over and
// over, so equal ones stand as one line with a count in front of it. A
// reaction that broke off is a line of its own, however often the same
// headline stands beside it: it is the one an assistant must not read past.
func TestTheNoteSummaryFoldsEqualHeadlines(t *testing.T) {
	now := time.Now().UTC()
	c := Instance{}
	c.Messages = append(c.Messages,
		Message{ID: "a1", Role: RoleAssistant, Content: "the chat answer", CreatedAt: now, State: StateComplete},
		Message{ID: "p1", Role: RoleAssistant, CreatedAt: now, State: StateComplete, Auto: true, Origin: &Note{Source: NoteEvent, Headline: "Schedule */30 * * * * ticked at 09:00"}},
		Message{ID: "p2", Role: RoleAssistant, CreatedAt: now, State: StateFailed, Auto: true, Origin: &Note{Source: NoteEvent, Headline: "Schedule */30 * * * * ticked at 09:30"}},
		Message{ID: "n1", Role: RoleCockpit, CreatedAt: now, State: StateComplete, Note: &Note{Source: NoteCheck, Headline: "DONE: readme-task"}},
		Message{ID: "p3", Role: RoleAssistant, CreatedAt: now, State: StateComplete, Auto: true, Origin: &Note{Source: NoteEvent, Headline: "Schedule */30 * * * * ticked at 09:00"}},
		Message{ID: "p4", Role: RoleAssistant, CreatedAt: now, State: StateInterrupted, Auto: true, Origin: &Note{Source: NoteEvent, Headline: "Schedule */30 * * * * ticked at 09:00"}},
	)
	want := "Since your last answer the cockpit wrote 5 notes into this thread, newest first. job-list and assistant-show on yourself say what, look only when the user's message needs it.\n" +
		"- Schedule */30 * * * * ticked at 09:00 (broke off)\n" +
		"- 2x Schedule */30 * * * * ticked at 09:00\n" +
		"- DONE: readme-task\n" +
		"- Schedule */30 * * * * ticked at 09:30 (broke off)\n" +
		"\nand now"
	if got := withNotes(notesSince(c), "and now"); got != want {
		t.Fatalf("want the folded summary:\n%s\ngot:\n%s", want, got)
	}
	// The second schedule stands under the first one, so the fold is over the
	// whole stretch and not over neighbours alone.
	lines := notesSince(c)
	if len(lines) != 4 || lines[1].count != 2 {
		t.Fatalf("want four lines with the plain schedule folded twice, got %+v", lines)
	}
}

// Fifty lines is the guard against an outlier. What is cut is the oldest, and
// the last line says how much, the way status says it about its coders.
func TestTheNoteSummaryStopsAtFiftyLines(t *testing.T) {
	now := time.Now().UTC()
	c := Instance{}
	c.Messages = append(c.Messages, Message{ID: "a1", Role: RoleAssistant, CreatedAt: now, State: StateComplete})
	for i := 0; i < 62; i++ {
		c.Messages = append(c.Messages, Message{
			ID:        fmt.Sprintf("n%d", i),
			Role:      RoleCockpit,
			CreatedAt: now,
			State:     StateComplete,
			Note:      &Note{Source: NoteCheck, Headline: fmt.Sprintf("DONE: task-%d", i)},
		})
	}
	got := withNotes(notesSince(c), "and now")
	if !strings.HasPrefix(got, "Since your last answer the cockpit wrote 62 notes into this thread, newest first.") {
		t.Fatalf("want every note counted, got:\n%s", got)
	}
	if n := strings.Count(got, "\n- "); n != maxNoteLines {
		t.Fatalf("want %d headlines, got %d:\n%s", maxNoteLines, n, got)
	}
	// The newest survive the cut, the oldest are what the last line stands for.
	for _, want := range []string{"- DONE: task-61\n", "- DONE: task-12\n", "  and 12 older\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("want %q in the summary, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "- DONE: task-11\n") {
		t.Fatalf("want the oldest cut, got:\n%s", got)
	}
}

func TestTriggersAndTheirStateSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	f := newEventFixtureIn(t, dir, &fakeRunner{answer: eventAnswer})
	owner := f.create(t)
	cron := f.trigger(t, TriggerSpec{Event: "cron", Spec: "* * * * *", Timezone: "Europe/Berlin", Task: "tick"})
	batched := f.trigger(t, TriggerSpec{Event: "job-done", Task: "batched", Batch: 30 * time.Second, BatchSet: true})
	f.reactor.Publish(f.jobDone("windowed"))
	if pending, _ := f.reactor.Get(batched.ID); len(pending.Pending) != 1 || pending.BatchDue.IsZero() {
		t.Fatalf("want the open window on disk, got %+v", pending)
	}
	// A job that closed while the cockpit was down is in jobs.json and
	// nowhere else: written straight into the store, the way the watcher
	// leaves it.
	closedAt := f.clock.now().Add(2 * time.Minute)
	NewJobs(f.store).Of(owner.ID).Save(Job{
		Terminal:  "term-gone",
		Name:      "closed-while-down",
		Project:   "demo",
		State:     JobDone,
		Note:      "the job is finished",
		CreatedAt: f.clock.now(),
		UpdatedAt: closedAt,
	})
	quiesce(t, f.svc, nil)

	// The next process over the same directory.
	second := newEventFixtureIn(t, dir, &fakeRunner{answer: eventAnswer})
	second.owner = owner
	second.clock.t = closedAt.Add(time.Minute)
	listed := second.reactor.ListOf(owner.ID)
	if len(listed) != 2 {
		t.Fatalf("want both triggers read back from disk, got %+v", listed)
	}
	second.reactor.Recover()
	// The tick is a reaction of its own; the event that waited in the window
	// and the job that closed in the gap were both taken by the one trigger
	// and its window folds them into one reaction.
	second.waitPushed(t, 2)
	all := make([]string, 0, 2)
	for _, m := range second.pushed(t) {
		all = append(all, m.Origin.Headline)
	}
	joined := strings.Join(all, "\n")
	for _, want := range []string{"Schedule * * * * * ticked at", "2 events arrived"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("after the restart %q is missing, got:\n%s", want, joined)
		}
	}
	prompts := ""
	for _, req := range second.reactionTurns() {
		prompts += req.Prompt
	}
	for _, want := range []string{"Job done: windowed", "Job done: closed-while-down in demo"} {
		if !strings.Contains(prompts, want) {
			t.Fatalf("the folded reaction does not carry %q", want)
		}
	}
	fresh, _ := second.reactor.Get(cron.ID)
	if !fresh.NextAt.After(second.clock.now()) || fresh.Fired != 1 {
		t.Fatalf("want the schedule fired once and moved past now, got %+v", fresh)
	}
	if fresh, _ := second.reactor.Get(batched.ID); len(fresh.Pending) != 0 || fresh.Fired != 1 {
		t.Fatalf("want the window fired once with both events, got %+v", fresh)
	}
	// A second recovery finds nothing left to catch up.
	second.reactor.Recover()
	time.Sleep(50 * time.Millisecond)
	if len(second.pushed(t)) != 2 {
		t.Fatal("a recovery must not fire what was already taken")
	}
}

func TestDeletingAnAssistantDropsItsTriggers(t *testing.T) {
	f := newEventFixture(t)
	owner := f.create(t)
	f.trigger(t, TriggerSpec{Event: "job-done", Task: "gone with me"})
	path := filepath.Join(f.store.InstanceDir(owner.ID), triggersFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("want the triggers next to the jobs, got %v", err)
	}
	if err := f.svc.Delete(owner.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(f.reactor.List()) != 0 {
		t.Fatal("a deleted assistant's triggers are gone")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("want the file gone with the directory, got %v", err)
	}
}

func TestATriggerRefusesWhatCannotFire(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	for _, spec := range []TriggerSpec{
		{Event: "job-done"},
		{Event: "nothing-known", Task: "x"},
		{Event: "cron", Task: "x"},
		{Event: "cron", Spec: "60 * * * *", Timezone: "Europe/Berlin", Task: "x"},
		// A schedule without a zone, and one whose zone is no zone: the five
		// fields say nine o'clock and nothing says whose nine o'clock.
		{Event: "cron", Spec: "* * * * *", Task: "x"},
		{Event: "cron", Spec: "* * * * *", Timezone: "Mars/Olympus", Task: "x"},
		{Event: "cron", Spec: "* * * * *", Timezone: "+02:00", Task: "x"},
		// A zone on an event that happens when it happens.
		{Event: "job-done", Timezone: "Europe/Berlin", Task: "x"},
		{Event: "job-done", Targets: targets("term-nobody"), Task: "x"},
		{Event: "job-closed", All: true, Task: "x"},
		{Event: "job-done", Spec: "* * * * *", Task: "x"},
	} {
		spec.Owner = f.owner.ID
		if _, err := f.reactor.Add(spec); err == nil {
			t.Fatalf("want %+v refused", spec)
		}
	}
	// A trigger nobody bounded carries the batch window and no expiry at all:
	// an expiry nobody asked for had taken an alarm for the next morning away
	// before it could ring.
	trigger := f.trigger(t, TriggerSpec{Event: "coder-news", Targets: targets("term-any"), Task: "answer it"})
	if trigger.Batch() != DefaultTriggerBatch || !trigger.ExpiresAt.IsZero() {
		t.Fatalf("want the batch window and no expiry, got %+v", trigger)
	}
	if err := f.reactor.Remove(trigger.ID, "somebody-else"); err == nil {
		t.Fatal("another assistant must not remove it")
	}
	if err := f.reactor.Remove(trigger.ID, f.owner.ID); err != nil {
		t.Fatalf("the owner removes it: %v", err)
	}
}

// coder-news is the one coder event a trigger may name, and it is the umbrella
// over both readings of a signal, the way job-closed stands over the three job
// ends: every signal fires it, whichever one the classification read it as. A
// trigger cannot pick one of the two, because the reading is a hook name and
// copilot has none, but nothing of the difference is lost to a reaction: the
// event carries the kind it was read as and the headline says which it was,
// with the coder named the way a notification names it.
func TestACoderNewsTriggerTakesEveryKind(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.reactor.SetCoderNamer(func(terminal string) (string, string) { return "either-way", "demo" })
	f.trigger(t, TriggerSpec{Event: "coder-news", Task: "look at it", BatchSet: true})
	f.reactor.Coder("term-1", CoderKindEnded)
	ended := f.waitPushed(t, 1)
	if ended.Origin.Headline != "Coder ended its turn: either-way in demo" {
		t.Fatalf("want the end named as an end, got %q", ended.Origin.Headline)
	}
	f.reactor.Coder("term-1", CoderKindAsks)
	asked := f.waitPushed(t, 2)
	if asked.Origin.Headline != "Coder asks a question: either-way in demo" {
		t.Fatalf("want the question named as a question, got %q", asked.Origin.Headline)
	}
}

// The narrow kinds stay narrow: the umbrella takes every kind of its own
// source and nothing of another one, so a job's end never fires a coder
// trigger and a tick fires neither.
func TestAnUmbrellaStaysWithinItsSource(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	news := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "x", BatchSet: true})
	closed := f.trigger(t, TriggerSpec{Event: "job-closed", Task: "x", BatchSet: true})
	for _, ev := range []CockpitEvent{
		{Source: EventJob, Kind: string(JobDone), Target: "term-1", Owner: f.owner.ID},
		{Source: EventCoder, Kind: CoderKindAsks, Target: "term-1"},
		{Source: EventCron, Kind: CronKindTick, Target: news.ID},
	} {
		for _, trigger := range []Trigger{news, closed} {
			want := trigger.Source == ev.Source
			if trigger.matches(ev) != want {
				t.Fatalf("%s-%s against %s-%s: want %v", trigger.Source, trigger.Kind, ev.Source, ev.Kind, want)
			}
		}
	}
}

// A barrier over three terminals is one turn at the end, not three: the events
// of the ones that arrived wait in the window, and the last one spends them
// together. Standing, it waits for every target again afterwards.
func TestABarrierFiresOnceEveryTargetArrived(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	for _, name := range []string{"one", "two", "three"} {
		f.steerJob(t, name)
	}
	trigger := f.trigger(t, TriggerSpec{
		Event: "job-closed", All: true, Task: "MAGIC summarize all three",
		Targets: targets("term-one", "term-two", "term-three"), BatchSet: true,
	})
	f.reactor.Publish(f.jobDone("one"))
	f.reactor.Publish(f.jobDone("two"))
	time.Sleep(100 * time.Millisecond)
	if pushed := f.pushed(t); len(pushed) != 0 {
		t.Fatalf("a barrier fired before its last target: %+v", pushed)
	}
	held, _ := f.reactor.Get(trigger.ID)
	if len(held.Pending) != 2 || !held.Waiting() || held.Fired != 0 {
		t.Fatalf("want two events held and the barrier still waiting, got %+v", held)
	}

	f.reactor.Publish(f.jobDone("three"))
	answer := f.waitPushed(t, 1)
	if answer.Origin.Count != 3 || answer.Origin.Headline != "3 events arrived" {
		t.Fatalf("want one turn for all three, got %+v", answer.Origin)
	}
	prompt := ""
	for _, req := range f.reactionTurns() {
		prompt += req.Prompt
	}
	for _, want := range []string{"Job done: one", "Job done: two", "Job done: three"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the one turn does not carry %q:\n%s", want, prompt)
		}
	}
	fresh, _ := f.reactor.Get(trigger.ID)
	if fresh.Fired != 1 || len(fresh.Pending) != 0 {
		t.Fatalf("want it fired exactly once, got %+v", fresh)
	}
	if !fresh.Waiting() {
		t.Fatalf("a standing barrier waits for every target again, got %+v", fresh.Targets)
	}
	f.reactor.Publish(f.jobDone("one"))
	time.Sleep(100 * time.Millisecond)
	if pushed := f.pushed(t); len(pushed) != 1 {
		t.Fatalf("one target is no barrier: %+v", pushed)
	}
}

// Three coders are started one after another, so the first can be finished
// before the third exists. A barrier made afterwards counts a job that is
// already closed as arrived, with its report in the window, instead of waiting
// for an end that will never be reported again.
func TestABarrierTakesAJobThatClosedBeforeItWasMade(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.steerJob(t, "first")
	f.closeJob(t, "first")
	f.steerJob(t, "second")

	trigger := f.trigger(t, TriggerSpec{
		Event: "job-closed", All: true, Task: "MAGIC summarize both",
		Targets: targets("term-first", "term-second"), BatchSet: true,
	})
	seeded, _ := f.reactor.Get(trigger.ID)
	if !seeded.Targets[0].Met || len(seeded.Pending) != 1 || !seeded.Waiting() {
		t.Fatalf("want the closed job counted and held, got %+v", seeded)
	}

	f.reactor.Publish(f.jobDone("second"))
	answer := f.waitPushed(t, 1)
	if answer.Origin.Count != 2 {
		t.Fatalf("want one turn about both, got %+v", answer.Origin)
	}
	prompt := ""
	for _, req := range f.reactionTurns() {
		prompt += req.Prompt
	}
	for _, want := range []string{"Job done: first in demo", "Job done: second"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the turn does not carry %q:\n%s", want, prompt)
		}
	}
}

// A deleted coder is the last thing that terminal ever does. Its open job
// closes with that reason, a trigger waiting for that terminal fires once for
// the deletion, a barrier counts it as arrived, and a trigger with no terminal
// left is removed and said out loud.
func TestADeletedCoderClosesItsJobAndDropsWhatOnlyItCouldFire(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.steerJob(t, "doomed")
	f.steerJob(t, "other")
	onDoomed := f.trigger(t, TriggerSpec{
		Event: "coder-news", Task: "MAGIC answer it",
		Targets: targets("term-doomed"), BatchSet: true,
	})
	barrier := f.trigger(t, TriggerSpec{
		Event: "job-closed", All: true, Task: "MAGIC summarize both",
		Targets: targets("term-doomed", "term-other"), BatchSet: true,
	})
	anybody := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "MAGIC any coder", BatchSet: true})

	watcher := NewWatcher(f.svc, NewJobs(f.store), nil, nil)
	dropped := watcher.TerminalDeleted("term-doomed")
	if got := DroppedNote(dropped); got != "1 trigger dropped" {
		t.Fatalf("want the one sentence about what fell, got %q, %+v", got, dropped)
	}
	if _, ok := f.reactor.Get(onDoomed.ID); ok {
		t.Fatal("a trigger whose only terminal is gone still stands")
	}
	if _, ok := f.reactor.Get(anybody.ID); !ok {
		t.Fatal("a trigger without targets must not be touched")
	}

	// It fired once for the deletion, in the words of the deletion.
	answer := f.waitPushed(t, 1)
	if !strings.Contains(answer.Origin.Headline, "Coder deleted: doomed in demo") {
		t.Fatalf("want the deletion as the event, got %+v", answer.Origin)
	}

	// The barrier keeps standing on its live terminal, with the deleted one
	// counted and its end in the window.
	held, ok := f.reactor.Get(barrier.ID)
	if !ok || !held.Open() {
		t.Fatalf("want the barrier standing, got %+v", held)
	}
	if !held.Targets[0].Gone || !held.Waiting() || len(held.Pending) != 1 {
		t.Fatalf("want the deleted target gone and the job's end held, got %+v", held)
	}
	if !strings.Contains(held.Pending[0].Body, "the coder was deleted") {
		t.Fatalf("the event does not say the coder was deleted, got %+v", held.Pending[0])
	}

	f.reactor.Publish(f.jobDone("other"))
	last := f.waitPushed(t, 2)
	if last.Origin.Count != 2 {
		t.Fatalf("want one turn about the deleted one and the finished one, got %+v", last.Origin)
	}
	if _, ok := NewJobs(f.store).Of(f.owner.ID).Get("term-doomed"); ok {
		t.Fatal("the job of a deleted coder is still in the store")
	}
}

// A trigger stored before several targets were possible carries one target and
// its name. It reads as a list of one and still fires on that terminal.
// TODO(v2.0.0)
func TestAStoredSingleTargetReadsAsAListOfOne(t *testing.T) {
	f := newEventFixture(t)
	owner := f.create(t)
	raw := []map[string]any{{
		"id": "aaaabbbbccccdddd", "source": EventCoder, "kind": CoderKindAsks,
		"target": "term-old", "targetName": "old one", "task": "MAGIC answer it",
		"state": string(TriggerStanding), "batchSeconds": 0,
		"createdAt": f.clock.now(), "updatedAt": f.clock.now(),
	}}
	statefile.Save(filepath.Join(f.store.InstanceDir(owner.ID), triggersFileName), 0o600, raw)

	read := f.reactor.ListOf(owner.ID)
	if len(read) != 1 || len(read[0].Targets) != 1 || read[0].Targets[0].Terminal != "term-old" || read[0].Targets[0].Name != "old one" {
		t.Fatalf("want the one target read as a list of one, got %+v", read)
	}
	if read[0].Target != "" || read[0].TargetName != "" {
		t.Fatalf("the old fields must not stay behind: %+v", read[0])
	}
	f.reactor.Coder("term-elsewhere", CoderKindAsks)
	time.Sleep(100 * time.Millisecond)
	if turns := len(f.reactionTurns()); turns != 0 {
		t.Fatalf("it must still be about its own terminal, got %d turns", turns)
	}
	f.reactor.Coder("term-old", CoderKindAsks)
	f.waitPushed(t, 1)
}

// A report stored before notes existed carried its check on the wake key and
// the assistant's role. It reads as a note now.
func TestOldWakeReportsReadAsNotes(t *testing.T) {
	f := newEventFixture(t)
	owner := f.create(t)
	raw := map[string]any{
		"id":              owner.ID,
		"title":           "old",
		"coderId":         "claude",
		"nativeSessionId": owner.ID,
		"messages": []map[string]any{{
			"id": "m1", "role": "assistant", "content": "the job is finished", "state": "complete",
			"wake": map[string]any{"terminal": "term-1", "name": "readme-task", "project": "demo", "verdict": "done"},
		}},
	}
	data, _ := json.Marshal(raw)
	statefile.Save(f.store.transcriptPath(owner.ID), 0o600, json.RawMessage(data))
	c, ok := f.store.Load(owner.ID)
	if !ok {
		t.Fatal("load")
	}
	m := c.Messages[0]
	if !m.IsNote() || m.Note.Source != NoteCheck || m.Note.Headline != "DONE: readme-task" || m.Note.Project != "demo" {
		t.Fatalf("want the old report read as a check's note, got %+v", m)
	}
}

// The instructions list every event kind with an example, say that a
// reaction runs in a session of its own with only the task, the instruction
// file, the memory and the workspace files, where its answer lands and what
// NOTHING means, so an assistant can set up any listener the cockpit offers
// when the user asks for it. And they do not claim that a report lands "here"
// as if the session knew: a report lands in the thread and the assistant
// hears about it with the user's next message.
//
// What a flag does and what a bound defaults to is not in here, it is in the
// command's own `--help`, which TestTheHelpCarriesWhatTheInstructionsDelegate
// pins: this file rides in every turn, every check and every reaction, so it
// carries the decisions and not the reference. The one default that rides
// along is the one a rule hangs on: `--until` is passed for a deadline the
// user named, and that holds only because nothing runs out without one.
func TestTheInstructionsListEveryEventKind(t *testing.T) {
	for _, k := range EventOptions {
		pinned(t, "trigger-new "+k.Name())
	}
	pinned(t,
		// The prose below the block asks for a name on every trigger, so the
		// line that gets copied carries one: a block without it writes a
		// nameless trigger at the one place a model copies from.
		"trigger-new job-done --name \"<two or three words>\"",
		"trigger-new cron --cron \"*/30 9-17 * * 1-5\"",
		// A schedule that fires once has a line of its own, because a block
		// is what gets copied: an alarm for the next morning was written by
		// taking the recurring line above and patching an expiry onto it
		// afterwards, the one shot case having nothing to copy.
		"trigger-new cron --cron \"45 7 * * *\" --once",
		"trigger-new job-closed --terminal <t1> --terminal <t2> --all",
		// Without --terminal a coder event is about every coder, which the
		// line with a --terminal on it has to say without reading as "every
		// coder but this one".
		"without --terminal, any coder's",
		"Put a barrier (`--all`) on `job-closed` and not on `job-done`",
		"a job that is already closed when you make the trigger counts as arrived right away",
		// An expiry is the user's own deadline and never a default: nothing
		// runs out on its own, so a trigger meant to fire once is spent by
		// `--once` and not by a span somebody guessed.
		"Pass `--until` only when the user names a deadline",
		"without one a trigger stands until it is deleted",
		"a schedule meant to fire once takes `--once` and not a deadline",
		"trigger-list",
		// What happened yesterday is a question about this list, and a turn
		// that does not know the flag reads the whole of it or gives up.
		"trigger-list --since 24h",
		"trigger-edit <id> --task",
		"trigger-delete <id>",
		// The pointer at the reference, and the decisions no reference can
		// make: every trigger carries a name, and the coder umbrella is the
		// safe one.
		"trigger-new --help",
		"trigger-edit --help",
		"give every trigger a `--name`, two or three words for what it is for",
		"the user's own word where they gave one and one you write from its purpose where they did not",
		"named after the coder it waits for, without you writing anything",
		"`coder-news` is the one coder event",
		"the event's headline says which",
		"keep the bounds tight",
		"runs in a session of its own",
		"only the task, this instruction file, the memory and the files in your workspace",
		"self contained or point to a file in",
		"Two events inside the batch window become one turn",
		// A task that wants a trigger to keep quiet has to say the word: the
		// contract stood in the reaction's own prompt alone, so an assistant
		// writing such a task had to guess it, and guessed a blank answer.
		"answers `NOTHING` on the first line when there is nothing to report",
		"Make a trigger when the user asks for a standing reaction",
		// A schedule is a wall clock somewhere: which verb says which of the
		// two things, that neither a zone nor a name is ever derived, and
		// that the zone and the tick are quoted and never computed. What one
		// verb does to the other is `timezone-set --help`'s.
		"trigger-new cron --cron \"0 9 * * *\" --tz America/New_York",
		"timezone-set Europe/Berlin",
		"Pass `--tz` only when the user names a place for that one schedule",
		"Run `timezone-set` only when they say where they are",
		"Never invent a zone and never work one out from something they wrote",
		// Which clock is in force is one command and not a listing to read.
		"timezone-get",
		"**quoted from what the command answered**, never a time you worked out",
		"its checks report into your thread, and you hear about them with the user's next message",
		// The handover is one call at the start, and the pattern that calls
		// for it is the relation and not a word the user used.
		"[--then \"<what happens once it is done>\"]",
		// The usage line carries --then as an option, which teaches neither
		// that it rides in the call that starts the coder nor how its task
		// has to read. A written out line teaches both without prose: the
		// missing --done-when is refused and corrects itself, a sequel task
		// that names no project and no destination is accepted and breaks
		// hours later, in a session that cannot make sense of it.
		"--then \"Start a reviewer on <project>, report what it finds\"",
		"is a handover: wire it with `--then` in the very call that starts the coder",
		"The relation is the trigger, the user does not have to ask for one",
		"what `--then` needs and how its task has to read is in",
		"coder-new --help",
		"The sequel of one job you start is the same arrangement made in one call",
	)
	// A check is a narrow turn with a verdict: it never reads as something
	// that waits for a job on its own or carries the work on. And what a flag
	// does belongs to `--help`: a default spelled out here is one that goes
	// stale in the one file every turn pays for.
	for _, gone := range []string{
		"its check already wakes you",
		"the DONE of the first is the handover",
		"a check by you",
		"`--until 8h` is the expiry",
		"`--batch 30s` is the window",
		"`--once` ends it after its first turn",
		// A zone nobody stored is a fact the list and `timezone-get` both
		// say; it is not a reason to stop and ask, the answer of a schedule
		// names the zone it was made with and the user reads it there.
		"that is the one time to ask instead of guessing",
		"at most 32 runes",
		"Changeable are the name",
		// What a reaction's answer has to look like is the reaction prompt's,
		// which only a reaction pays for; every turn pays for this file.
		"Its answer is pushed into your thread",
		"then nothing is pushed and nobody is notified",
		// The mechanics of --then are `coder-new --help`'s, and why a coder
		// has one event and no narrower one is `trigger-new --help`'s.
		"`--then` fires once, when that job closes done",
		"copilot has none",
		// What a deletion takes with it is `coder-delete --help`'s and
		// `coder-stop --help`'s, which help_test.go pins: it is read when
		// somebody deletes, and the answer of the command says what went.
		"Deleting a coder ends what was arranged about that terminal",
		"Stopping a coder ends its job but keeps its triggers",
		// What a changeover does to a schedule is `trigger-new --help`'s: the
		// sentence above it forbids working a time out, so this is a lookup
		// for a question and never a step in one.
		"the schedule fires twice",
	} {
		if text := instructionsText(t); strings.Contains(text, gone) {
			t.Fatalf("the instructions still say %q", gone)
		}
	}
	if text := instructionsText(t); strings.Contains(text, "their reports land here") {
		t.Fatal("the instructions must not claim a report lands here, the session does not see the thread")
	}
}

// orphanReaction launches a reaction the way orphanTurn launches a chat turn:
// the register entry, the files and the process, and nobody following it.
// What is left is exactly what a process that died right after the launch
// leaves behind.
func orphanReaction(t *testing.T, f *eventFixture, trigger Trigger, ev CockpitEvent) RunRecord {
	t.Helper()
	c, err := f.svc.Get(f.owner.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	origin := Note{Source: NoteEvent, Headline: ev.Headline, Verdict: ev.Kind, Terminal: ev.Target, Trigger: trigger.ID, Task: trigger.Task, Count: 1}
	rec := RunRecord{
		ID:        statefile.NewID(),
		Kind:      RunReaction,
		Instance:  c.ID,
		MessageID: statefile.NewID(),
		CoderID:   "claude",
		SessionID: "reaction-session-" + statefile.NewID(),
		Origin:    &origin,
	}
	if _, err := f.svc.launch(&rec, f.runner, TurnRequest{Instance: c.ID, SessionID: rec.SessionID, Title: reactionSessionName(c.Title), Workdir: mustWorkdir(t, f.svc, c.ID), Prompt: reactionPrompt(origin, ev.Body)}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	return rec
}

// A reaction outlives the server the way a check does: it is in the register
// with its origin. Here the trigger fires, its reaction is running when the
// first process ends, and the next process over the same state directory
// recovers it: the answer is pushed marked as started without the user with
// its origin, a NOTHING answer pushes nothing and rings nobody, and the
// trigger stands as the fire left it, fired once and, a one shot, done.
func TestARunningReactionSurvivesARestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
		pushed bool
	}{
		{"with words", "event answer", true},
		{"with NOTHING", "NOTHING", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: tc.answer}}, block: make(chan struct{})}
			f := newEventFixtureIn(t, dir, runner)
			owner := f.create(t)
			trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize", Once: true, BatchSet: true})

			// The fire is the reactor's own, bookkeeping and all; the reaction
			// it starts is launched without a follower, see orphanReaction,
			// so nothing of the first process outlives it but the register
			// and the files. The coder is away for the moment of the fire so
			// the reactor's own start does not begin a second, followed
			// reaction.
			f.svc.coders = fakeCoders{}
			ev := f.jobDone("readme")
			f.reactor.Publish(ev)
			waitFor(t, "the fire to give up on the missing coder", func() bool {
				fresh, _ := f.reactor.Get(trigger.ID)
				return fresh.Fired == 1 && !fresh.Reacting()
			})
			f.svc.coders = fakeCoders{runner: runner}
			fired, _ := f.reactor.Get(trigger.ID)
			if fired.State != TriggerDone {
				t.Fatalf("want the one shot done before the restart, got %+v", fired)
			}
			rec := orphanReaction(t, f, trigger, ev)
			if !detach.Alive(rec.PID, rec.Lock) {
				t.Fatal("want the reaction to still be running")
			}

			// The next process over the same directory.
			second := newEventFixtureIn(t, dir, runner)
			second.owner = owner
			if adopted := second.svc.Recover(); len(adopted) != 0 {
				t.Fatalf("want no adopted check for a reaction, got %d", len(adopted))
			}
			second.svc.mu.Lock()
			_, running := second.svc.running[rec.ID]
			second.svc.mu.Unlock()
			if !running || !second.svc.Reserved("claude", rec.SessionID) {
				t.Fatal("want the restarted server to follow the reaction and keep its session reserved")
			}

			// The reactor's own start above found no coder, which is a turn
			// that broke off and is pushed as one. What the restart answers
			// stands behind it.
			broken := len(f.pushed(t))
			if broken != 1 {
				t.Fatalf("want the failed start pushed once, got %d", broken)
			}

			close(runner.block)
			if tc.pushed {
				answer := second.waitPushed(t, broken+1)
				if answer.ID != rec.MessageID || answer.Content != tc.answer || !answer.Auto || answer.Origin == nil || answer.Origin.Trigger != trigger.ID {
					t.Fatalf("want the answer pushed under the register's id with its origin, got %+v", answer)
				}
				select {
				case id := <-second.news:
					if id != owner.ID {
						t.Fatalf("news for %s, want %s", id, owner.ID)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("a pushed answer is news after a restart too")
				}
			} else {
				waitFor(t, "the reaction to end", func() bool {
					second.svc.mu.Lock()
					defer second.svc.mu.Unlock()
					return len(second.svc.running) == 0
				})
				time.Sleep(50 * time.Millisecond)
				if len(second.pushed(t)) != broken {
					t.Fatal("a NOTHING answer pushes nothing after a restart either")
				}
				select {
				case <-second.news:
					t.Fatal("a NOTHING answer rings nobody")
				case <-time.After(200 * time.Millisecond):
				}
			}
			after, _ := second.reactor.Get(trigger.ID)
			if after.Fired != 1 || after.State != TriggerDone || after.Reacting() {
				t.Fatalf("want the trigger untouched by the recovery, got %+v", after)
			}
			if second.svc.Reserved("claude", rec.SessionID) {
				t.Fatal("want the reaction's session released once it ended")
			}
		})
	}
}

// A reaction whose output the next process cannot read any more is
// interrupted, not failed: nothing about it went wrong, it lost its server.
// It is pushed all the same, under that state, so a reaction does not vanish
// in the restart it was running through.
func TestAReactionLostInARestartIsPushedAsInterrupted(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "event answer"}}}
	f := newEventFixtureIn(t, dir, runner)
	owner := f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize", BatchSet: true})

	ev := f.jobDone("readme")
	rec := orphanReaction(t, f, trigger, ev)
	f.reactor.triggers.Of(owner.ID).Update(trigger.ID, func(s *Trigger) bool {
		s.Fired = 1
		s.ReactingSince = f.clock.now()
		return true
	})
	// The words this turn was writing are not in that file any more, which is
	// exactly what a restart must not read past.
	waitFor(t, "the reaction to end", func() bool { return !detach.Alive(rec.PID, rec.Lock) })
	if err := os.Remove(rec.Output); err != nil {
		t.Fatalf("remove the output: %v", err)
	}

	second := newEventFixtureIn(t, dir, runner)
	second.owner = owner
	second.svc.Recover()

	answer := second.waitPushed(t, 1)
	if answer.ID != rec.MessageID || answer.State != StateInterrupted || answer.Error == "" {
		t.Fatalf("want the lost reaction pushed as interrupted, got %+v", answer)
	}
	if answer.Origin == nil || answer.Origin.Trigger != trigger.ID {
		t.Fatalf("want the origin on it, so it still reads as a trigger's, got %+v", answer.Origin)
	}
}

// edit is Reactor.Edit by the owner, the way the page and the command line
// reach it.
func (f *eventFixture) edit(t *testing.T, id string, spec TriggerSpec) (Trigger, string) {
	t.Helper()
	trigger, changed, err := f.reactor.Edit(id, f.owner.ID, spec)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	return trigger, changed
}

// A typo in the task is one change away, and the change touches nothing else:
// the bounds nobody named stand, and so does everything the trigger already
// did, which is what tells a change from making it again.
func TestAnEditMovesOnlyWhatItNames(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.steerJob(t, "one")
	trigger := f.trigger(t, TriggerSpec{
		Event: "job-done", Targets: targets("term-one"), Task: "sumarize it",
		Batch: 45 * time.Second, BatchSet: true, Until: 2 * time.Hour,
	})
	f.reactor.Publish(f.jobDone("one"))
	f.clock.advance(time.Minute)
	f.reactor.Tick()
	f.waitPushed(t, 1)
	fired, _ := f.reactor.Get(trigger.ID)

	f.clock.advance(time.Minute)
	next, changed := f.edit(t, trigger.ID, TriggerSpec{Task: "summarize it"})
	if next.Task != "summarize it" || changed != "task" {
		t.Fatalf("want the task changed and said so, got %q and %q", next.Task, changed)
	}
	if next.BatchSeconds != 45 || !next.ExpiresAt.Equal(fired.ExpiresAt) || next.Once != fired.Once {
		t.Fatalf("a bound nobody named moved: %+v", next)
	}
	if len(next.Targets) != 1 || next.Targets[0].Terminal != "term-one" {
		t.Fatalf("the terminals moved: %+v", next.Targets)
	}
	if next.Fired != fired.Fired || !next.CreatedAt.Equal(fired.CreatedAt) || next.State != fired.State {
		t.Fatalf("want the record untouched, got %+v against %+v", next, fired)
	}
	if !next.UpdatedAt.After(fired.UpdatedAt) {
		t.Fatalf("want UpdatedAt moved, got %s against %s", next.UpdatedAt, fired.UpdatedAt)
	}
	// And it is on disk, not only in the answer.
	stored, ok := f.reactor.Get(trigger.ID)
	if !ok || stored.Task != "summarize it" {
		t.Fatalf("the change was not stored: %+v", stored)
	}
}

// The name is the user's own word for a trigger and it is optional: one
// nobody named has none and its row falls back to the event, one that is
// named carries it as one collapsed line, and a name past the row's measure
// is refused rather than cut. An edit that does not name it leaves it, an
// empty one takes it away, and the line an edit answers with says which.
func TestATriggerNameIsOptionalAndNeverInvented(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	bare := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "MAGIC watch it"})
	if bare.Name != "" {
		t.Fatalf("a trigger nobody named has no name, got %q", bare.Name)
	}
	named := f.trigger(t, TriggerSpec{Event: "cron", Spec: "0 4 * * *", Timezone: "Europe/Berlin", Name: " Nightly \n summary ", NameSet: true, Task: "MAGIC nightly"})
	if named.Name != "Nightly summary" {
		t.Fatalf("want the name as one collapsed line, got %q", named.Name)
	}
	long := strings.Repeat("a", MaxTriggerNameRunes+1)
	if _, err := f.reactor.Add(TriggerSpec{Owner: f.owner.ID, Event: "coder-news", Name: long, NameSet: true, Task: "x"}); err == nil {
		t.Fatal("a name past the row's measure is refused, not cut")
	}
	// A label the cockpit picks for itself is the other way round: the sequel
	// of coder-new --then is named after its coder, and a session name past
	// the cap leaves that trigger without a name instead of refusing it.
	if got := FitTriggerName(" release  check "); got != "release check" {
		t.Fatalf("want the label collapsed to one line, got %q", got)
	}
	if got := FitTriggerName(long); got != "" {
		t.Fatalf("want a label past the cap dropped, got %q", got)
	}

	// An edit that names everything but the name leaves it standing.
	kept, changed := f.edit(t, named.ID, TriggerSpec{Task: "MAGIC nightly, shorter"})
	if kept.Name != "Nightly summary" || changed != "task" {
		t.Fatalf("an unnamed field moved: %q, %q", kept.Name, changed)
	}
	renamed, changed := f.edit(t, named.ID, TriggerSpec{Name: "Morning summary", NameSet: true})
	if renamed.Name != "Morning summary" || changed != "name Morning summary" {
		t.Fatalf("want the rename said, got %q and %q", renamed.Name, changed)
	}
	cleared, changed := f.edit(t, named.ID, TriggerSpec{Name: "", NameSet: true})
	if cleared.Name != "" || changed != "no name" {
		t.Fatalf("an empty name takes it away and says so, got %q and %q", cleared.Name, changed)
	}
}

// A name is what the trigger is read by wherever one line is all there is,
// and one place decides it: the headline of the origin the fire builds. The
// notification, the header over the pushed answer and the line in front of
// the next chat prompt all read that headline, so all three take the name
// without knowing names exist. What fired it moves into the origin's Event,
// where the readers with room for both find it: the prompt the reaction is
// asked, which has to say what happened and not what the trigger is called,
// and the trigger's own note, which stands under the name in its row.
func TestANamedTriggerIsReadByItsNameWhereverOneLineFits(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Name: "nightly readme", NameSet: true, Task: "summarize what is done", BatchSet: true})

	f.reactor.Publish(f.jobDone("readme"))
	answer := f.waitPushed(t, 1)
	origin := answer.Origin
	if origin == nil || origin.Headline != "nightly readme" {
		t.Fatalf("want the name as the line the answer is read by, got %+v", origin)
	}
	if origin.Event != "Job done: readme" || origin.Occasion() != "Job done: readme" {
		t.Fatalf("want what fired it beside the name, got %+v", origin)
	}
	// The turn the reaction runs is told what happened, never the name: a
	// name says which trigger this is, not what it is about.
	prompt := f.reactionTurns()[0].Prompt
	if !strings.Contains(prompt, "Event: Job done: readme") || strings.Contains(prompt, "nightly readme") {
		t.Fatalf("want the prompt on the event alone:\n%s", prompt)
	}
	// The trigger's own note stands under its heading, which is the name, so
	// it says what happened and agrees with the line the fire wrote.
	fresh, _ := f.reactor.Get(trigger.ID)
	if fresh.Note != "Answered for Job done: readme." {
		t.Fatalf("want the trigger's note on the event, got %q", fresh.Note)
	}
	// And the summary in front of the next chat prompt reads by the name.
	if _, err := f.svc.Send(f.owner.ID, "how did it go", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, f.svc, f.owner.ID)
	turns := f.runner.turns()
	chat := turns[len(turns)-1].Prompt
	if !strings.Contains(chat, "\n- nightly readme\n") {
		t.Fatalf("want the name on the note line:\n%s", chat)
	}
}

// Without a name nothing moves: the headline is what fired the trigger, the
// origin carries no event beside it, and every surface reads exactly what it
// read before names existed.
func TestAnUnnamedTriggerReadsExactlyAsItAlwaysDid(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize what is done", BatchSet: true})

	f.reactor.Publish(f.jobDone("readme"))
	answer := f.waitPushed(t, 1)
	origin := answer.Origin
	if origin == nil || origin.Headline != "Job done: readme" || origin.Event != "" {
		t.Fatalf("want the event as the headline and nothing beside it, got %+v", origin)
	}
	if origin.Occasion() != "Job done: readme" {
		t.Fatalf("want the headline to be the occasion, got %q", origin.Occasion())
	}
	fresh, _ := f.reactor.Get(trigger.ID)
	if fresh.Note != "Answered for Job done: readme." {
		t.Fatalf("want the trigger's note unchanged, got %q", fresh.Note)
	}
}

// The next reaction is asked the new task. A reaction that already runs keeps
// the old one, it was given it, so nothing is rewritten under a turn.
func TestTheNextReactionTakesTheNewTask(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "the first task", BatchSet: true})
	f.edit(t, trigger.ID, TriggerSpec{Task: "the second task"})
	f.reactor.Coder("term-1", CoderKindEnded)
	f.waitPushed(t, 1)
	prompt := ""
	for _, req := range f.reactionTurns() {
		prompt += req.Prompt
	}
	if !strings.Contains(prompt, "the second task") || strings.Contains(prompt, "the first task") {
		t.Fatalf("the reaction did not take the new task:\n%s", prompt)
	}
}

// The terminals are replaced as a list, and what a target reached rides along:
// a barrier that already holds an arrival keeps waiting for the rest, a target
// named for the first time counts as not arrived unless its job is closed
// already, and a removed one takes its state with it.
func TestAnEditOfTheTargetsKeepsWhatArrived(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	for _, name := range []string{"one", "two", "three"} {
		f.steerJob(t, name)
	}
	trigger := f.trigger(t, TriggerSpec{
		Event: "job-closed", All: true, Task: "summarize them",
		Targets: targets("term-one", "term-two"), BatchSet: true,
	})
	f.reactor.Publish(f.jobDone("one"))
	held, _ := f.reactor.Get(trigger.ID)
	if !held.Targets[0].Met || len(held.Pending) != 1 {
		t.Fatalf("want the first arrival held, got %+v", held)
	}

	next, changed := f.edit(t, trigger.ID, TriggerSpec{
		Targets: targets("term-one", "term-two", "term-three"), TargetsSet: true,
	})
	if changed != "3 terminals" {
		t.Fatalf("want the terminals named in the answer, got %q", changed)
	}
	if !next.Targets[0].Met || next.Targets[1].Met || next.Targets[2].Met {
		t.Fatalf("want the arrival kept and the new target waiting, got %+v", next.Targets)
	}
	if len(next.Pending) != 1 || next.Fired != 0 {
		t.Fatalf("the change spent the window: %+v", next)
	}
	if !next.Waiting() {
		t.Fatal("a barrier with a new target waits for it")
	}
	f.reactor.Publish(f.jobDone("two"))
	f.reactor.Publish(f.jobDone("three"))
	answer := f.waitPushed(t, 1)
	if answer.Origin.Count != 3 {
		t.Fatalf("want one turn holding all three, got %+v", answer.Origin)
	}

	// A target that is taken out takes its arrival with it: the barrier that is
	// left waits for what it names now and nothing else.
	f.reactor.Publish(f.jobDone("one"))
	shrunk, _ := f.edit(t, trigger.ID, TriggerSpec{Targets: targets("term-two", "term-three"), TargetsSet: true})
	if len(shrunk.Targets) != 2 || shrunk.holds("term-one") {
		t.Fatalf("want the target gone, got %+v", shrunk.Targets)
	}
}

// A target named for the first time whose job is already closed counts as
// arrived at once, the rule a fresh barrier follows: the end it missed is not
// reported a second time.
func TestAnEditSeedsATargetWhoseJobIsAlreadyClosed(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.steerJob(t, "first")
	f.steerJob(t, "other")
	f.steerJob(t, "second")
	f.closeJob(t, "second")
	trigger := f.trigger(t, TriggerSpec{
		Event: "job-closed", All: true, Task: "summarize both",
		Targets: targets("term-first", "term-other"), BatchSet: true,
	})

	f.edit(t, trigger.ID, TriggerSpec{Targets: targets("term-first", "term-second"), TargetsSet: true})
	next, _ := f.reactor.Get(trigger.ID)
	if !next.Targets[1].Met || len(next.Pending) != 1 {
		t.Fatalf("want the closed job counted right away, got %+v", next)
	}
	// And only once: a second edit that names it again does not take it twice.
	f.edit(t, trigger.ID, TriggerSpec{Task: "summarize both, briefly"})
	again, _ := f.reactor.Get(trigger.ID)
	if len(again.Pending) != 1 {
		t.Fatalf("the end was taken twice: %+v", again.Pending)
	}
}

// A changed schedule works its next tick out again; one nobody touched keeps
// the tick it is waiting for.
func TestAnEditOfTheScheduleWorksOutTheNextTick(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "cron", Spec: "0 3 * * *", Timezone: "Europe/Berlin", Task: "the nightly summary"})
	stood := trigger.NextAt

	same, _ := f.edit(t, trigger.ID, TriggerSpec{Task: "the nightly summary, briefly"})
	if !same.NextAt.Equal(stood) {
		t.Fatalf("a tick moved without the schedule: %s against %s", same.NextAt, stood)
	}
	next, changed := f.edit(t, trigger.ID, TriggerSpec{Spec: "*/15  9-17 * * 1-5"})
	if next.Spec != "*/15 9-17 * * 1-5" || changed != "schedule */15 9-17 * * 1-5" {
		t.Fatalf("want the schedule read and said, got %q and %q", next.Spec, changed)
	}
	if !next.NextAt.After(f.clock.now()) || next.NextAt.Equal(stood) {
		t.Fatalf("want the next tick worked out again, got %s", next.NextAt)
	}
	// The zone moves the tick the same way the fields do: the same five fields
	// in another zone are other minutes, so the tick is worked out again, and
	// the line says which zone it is now read in.
	moved, changed := f.edit(t, trigger.ID, TriggerSpec{Timezone: "America/New_York"})
	if moved.Timezone != "America/New_York" || changed != "zone America/New_York" {
		t.Fatalf("want the zone read and said, got %q and %q", moved.Timezone, changed)
	}
	if !moved.NextAt.After(f.clock.now()) || moved.NextAt.Equal(next.NextAt) {
		t.Fatalf("want the next tick worked out again for the new zone, got %s against %s", moved.NextAt, next.NextAt)
	}
	if _, _, err := f.reactor.Edit(trigger.ID, f.owner.ID, TriggerSpec{Spec: "60 * * * *"}); err == nil {
		t.Fatal("a schedule that cannot be read must be refused")
	}
	if _, _, err := f.reactor.Edit(trigger.ID, f.owner.ID, TriggerSpec{Timezone: "Mars/Olympus"}); err == nil {
		t.Fatal("a zone that cannot be read must be refused")
	}
	if fresh, _ := f.reactor.Get(trigger.ID); fresh.Spec != "*/15 9-17 * * 1-5" || fresh.Timezone != "America/New_York" {
		t.Fatalf("a refused change wrote anyway: %+v", fresh)
	}
}

// What an edit refuses: a trigger that is spent, the event, somebody else's,
// and a bound that does not read.
func TestAnEditRefusesWhatItCannotChange(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.steerJob(t, "one")
	trigger := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "answer it", BatchSet: true})

	for _, spec := range []TriggerSpec{
		{Event: "job-done"},
		{Event: "nothing-known"},
		{Task: strings.Repeat("x", maxTriggerTaskRunes+1)},
		{Until: -time.Hour},
		{Batch: -time.Second, BatchSet: true},
		{Spec: "* * * * *"},
		{Timezone: "Europe/Berlin"},
		{All: true, AllSet: true, Targets: nil, TargetsSet: true},
	} {
		if _, _, err := f.reactor.Edit(trigger.ID, f.owner.ID, spec); err == nil {
			t.Fatalf("want %+v refused", spec)
		}
	}
	if _, _, err := f.reactor.Edit(trigger.ID, "somebody-else", TriggerSpec{Task: "mine now"}); err == nil {
		t.Fatal("another assistant must not change it")
	}
	if _, _, err := f.reactor.Edit("nothing-like-this", "", TriggerSpec{Task: "x"}); err == nil {
		t.Fatal("an id nothing answers to must be refused")
	}
	if fresh, _ := f.reactor.Get(trigger.ID); fresh.Task != "answer it" || fresh.Kind != CoderKindNews {
		t.Fatalf("a refused change wrote anyway: %+v", fresh)
	}

	// A one shot that fired is spent, and so is one that expired: they are
	// refused with a sentence instead of quietly coming back to life.
	once := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "once only", Once: true, BatchSet: true})
	f.reactor.Coder("term-1", CoderKindEnded)
	f.waitPushed(t, 1)
	if _, _, err := f.reactor.Edit(once.ID, f.owner.ID, TriggerSpec{Task: "again"}); err == nil {
		t.Fatal("a trigger that is done must not be changed")
	}
}

// Every bound moves, and the answer names what moved, so the user reads what
// their change did instead of reading the row back.
func TestAnEditMovesEveryBoundAndSaysWhich(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "answer it", Until: 2 * time.Hour})

	next, changed := f.edit(t, trigger.ID, TriggerSpec{
		Once: true, OnceSet: true, Never: true, Batch: 90 * time.Second, BatchSet: true,
	})
	if !next.Once || !next.ExpiresAt.IsZero() || next.BatchSeconds != 90 {
		t.Fatalf("want every bound moved, got %+v", next)
	}
	if changed != "once, no expiry, batch 90s" {
		t.Fatalf("the answer does not name what moved: %q", changed)
	}
	back, changed := f.edit(t, trigger.ID, TriggerSpec{Once: false, OnceSet: true, Until: time.Hour})
	if back.Once || back.ExpiresAt.IsZero() {
		t.Fatalf("want the one shot and the expiry back, got %+v", back)
	}
	if !strings.HasPrefix(changed, "standing, until ") {
		t.Fatalf("the answer does not name what moved: %q", changed)
	}
	if _, nothing := f.edit(t, trigger.ID, TriggerSpec{}); nothing != "" {
		t.Fatalf("a change that changes nothing says so: %q", nothing)
	}
}

// A change is on disk like everything else: a restart reads the new task, and
// a window the change did not touch is still there.
func TestAnEditSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	f := newEventFixtureIn(t, dir, &fakeRunner{answer: eventAnswer})
	owner := f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "coder-news", Task: "the old task"})
	f.reactor.Coder("term-1", CoderKindEnded)
	waitFor(t, "the event in the window", func() bool {
		held, _ := f.reactor.Get(trigger.ID)
		return len(held.Pending) == 1
	})
	f.edit(t, trigger.ID, TriggerSpec{Task: "the new task", Batch: 90 * time.Second, BatchSet: true})

	again := newEventFixtureIn(t, dir, &fakeRunner{answer: eventAnswer})
	again.owner = owner
	back, ok := again.reactor.Get(trigger.ID)
	if !ok || back.Task != "the new task" || back.BatchSeconds != 90 {
		t.Fatalf("the change did not survive: %+v", back)
	}
	if len(back.Pending) != 1 {
		t.Fatalf("the window did not survive the change: %+v", back)
	}
}
