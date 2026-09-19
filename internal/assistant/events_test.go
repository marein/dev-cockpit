package assistant

import (
	"encoding/json"
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

// steerJob writes an open job of the owner straight into the store, the way the
// watcher leaves one. A job target of a subscription has to carry one.
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
func targets(terminals ...string) []SubscriptionTarget {
	out := make([]SubscriptionTarget, 0, len(terminals))
	for _, terminal := range terminals {
		out = append(out, SubscriptionTarget{Terminal: terminal})
	}
	return out
}

func (f *eventFixture) subscribe(t *testing.T, spec SubscriptionSpec) Subscription {
	t.Helper()
	spec.Owner = f.owner.ID
	sub, err := f.reactor.Subscribe(spec)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return sub
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

// reacting is how many of the owner's subscriptions say a reaction runs.
func (f *eventFixture) reacting() int {
	n := 0
	for _, sub := range f.reactor.ListOf(f.owner.ID) {
		if sub.Reacting() {
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
	sub := f.subscribe(t, SubscriptionSpec{Event: "job-done", Task: "summarize what is done", BatchSet: true})
	// The provider holds the session the reaction created, so the drop at
	// its end is a real delete and not only a released reservation.
	f.runner.mu.Lock()
	f.runner.exists = true
	f.runner.mu.Unlock()

	f.reactor.Publish(f.jobDone("readme"))
	answer := f.waitPushed(t, 1)
	if !answer.Auto || answer.Content != "event answer" || answer.State != StateComplete || answer.Role != RoleAssistant {
		t.Fatalf("want the answer pushed as one started without the user, got %+v", answer)
	}
	if answer.Origin == nil || answer.Origin.Source != NoteEvent || answer.Origin.Headline != "Job done: readme" || answer.Origin.Task != "summarize what is done" || answer.Origin.Subscription != sub.ID || answer.Origin.Count != 1 {
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
	for _, want := range []string{"This turn was started by the cockpit", "session of your own", "Event: Job done: readme", "the job readme is finished", "Your task for this event: summarize what is done", "answer NOTHING on the first line"} {
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
	fresh, _ := f.reactor.Get(sub.ID)
	if fresh.Fired != 1 || !fresh.Open() || fresh.Reacting() || fresh.Note != "Answered for Job done: readme." {
		t.Fatalf("want the subscription standing, fired once and answered, got %+v", fresh)
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
	f.subscribe(t, SubscriptionSpec{Event: "job-done", Task: "carry on", BatchSet: true})

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

// One subscription reacts once at a time. What arrives while its reaction
// runs waits in the window, however short that window is, and the end of the
// reaction spends it as one turn: the answers stay in order and the thread
// hears about each event exactly once.
func TestASubscriptionReactsOnceAtATime(t *testing.T) {
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
	sub := f.subscribe(t, SubscriptionSpec{Event: "job-done", Task: "hand out the next one", BatchSet: true})

	f.reactor.Publish(f.jobDone("first"))
	waitFor(t, "the first reaction to run", func() bool {
		fresh, _ := f.reactor.Get(sub.ID)
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
	waiting, _ := f.reactor.Get(sub.ID)
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
	done, _ := f.reactor.Get(sub.ID)
	if done.Fired != 2 || len(done.Pending) != 0 || done.Reacting() || !done.Open() {
		t.Fatalf("want the subscription standing, fired twice and empty, got %+v", done)
	}
}

// The window a running reaction holds open is on disk like everything else a
// subscription carries. A process that ends mid reaction leaves it there, and
// the next one spends it when the reaction it waited for is concluded.
func TestEventsHeldUnderARunningReactionSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "event answer"}}, block: make(chan struct{})}
	f := newEventFixtureIn(t, dir, runner)
	owner := f.create(t)
	sub := f.subscribe(t, SubscriptionSpec{Event: "job-done", Task: "hand out the next one", BatchSet: true})

	// A reaction nobody follows, the way the first process leaves one behind,
	// and the subscription as its fire wrote it: reacting, spent once.
	ev := f.jobDone("first")
	rec := orphanReaction(t, f, sub, ev)
	f.reactor.subs.Of(owner.ID).Update(sub.ID, func(s *Subscription) bool {
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
	held, _ := second.reactor.Get(sub.ID)
	if len(held.Pending) != 2 || !held.Reacting() {
		t.Fatalf("want the held events and the running reaction on disk, got %+v", held)
	}
	second.svc.Recover()
	close(runner.block)

	answer := second.waitPushed(t, 2)
	if answer.Origin == nil || answer.Origin.Headline != "2 events arrived" || answer.ID == rec.MessageID {
		t.Fatalf("want the held events answered after the recovery, got %+v", answer)
	}
	after, _ := second.reactor.Get(sub.ID)
	if after.Fired != 2 || len(after.Pending) != 0 || after.Reacting() {
		t.Fatalf("want the window spent after the recovery, got %+v", after)
	}
}

func TestABatchWindowFoldsEventsIntoOneReaction(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.subscribe(t, SubscriptionSpec{Event: "job-closed", Task: "hand out the next one", Batch: 30 * time.Second, BatchSet: true})

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

func TestTheCapPerHourRefusesOnTheSubscriptionAlone(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	sub := f.subscribe(t, SubscriptionSpec{Event: "job-done", Task: "carry on", MaxPerHour: 1, BatchSet: true})

	f.reactor.Publish(f.jobDone("one"))
	f.waitPushed(t, 1)
	<-f.news

	f.clock.advance(time.Minute)
	f.reactor.Publish(f.jobDone("two"))
	waitFor(t, "the refusal on the subscription", func() bool {
		fresh, _ := f.reactor.Get(sub.ID)
		return strings.Contains(fresh.Note, "No turn for Job done: two")
	})
	fresh, _ := f.reactor.Get(sub.ID)
	if !strings.Contains(fresh.Note, "cap of 1 turn per hour") || fresh.Fired != 1 {
		t.Fatalf("want the cap's refusal on the subscription's line, got %+v", fresh)
	}
	// The thread hears nothing of a refused turn.
	c, _ := f.svc.Get(f.owner.ID)
	if len(c.Messages) != 1 || len(f.reactionTurns()) != 1 {
		t.Fatalf("a refused turn shows in the subscription's state alone, got %+v", c.Messages)
	}
	select {
	case <-f.news:
		t.Fatal("a refused turn rings nobody")
	case <-time.After(200 * time.Millisecond):
	}

	f.clock.advance(61 * time.Minute)
	f.reactor.Publish(f.jobDone("three"))
	f.waitPushed(t, 2)
	if len(f.reactionTurns()) != 2 {
		t.Fatalf("want the cap to open again after an hour, got %d turns", len(f.reactionTurns()))
	}
}

func TestAOnceSubscriptionEndsAfterItsFirstReaction(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	sub := f.subscribe(t, SubscriptionSpec{Event: "job-done", Task: "once", Once: true, BatchSet: true})

	f.reactor.Publish(f.jobDone("one"))
	f.waitPushed(t, 1)
	fresh, _ := f.reactor.Get(sub.ID)
	if fresh.State != SubscriptionDone {
		t.Fatalf("want a one shot done after its reaction, got %q", fresh.State)
	}
	f.reactor.Publish(f.jobDone("two"))
	time.Sleep(50 * time.Millisecond)
	if len(f.pushed(t)) != 1 || len(f.runner.turns()) != 1 {
		t.Fatal("a one shot that fired takes nothing more")
	}
}

func TestAnExpiredSubscriptionEndsOnItsLineAlone(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	sub := f.subscribe(t, SubscriptionSpec{Event: "job-done", Task: "later", Until: time.Hour, BatchSet: true})

	f.clock.advance(2 * time.Hour)
	f.reactor.Tick()
	fresh, _ := f.reactor.Get(sub.ID)
	if fresh.State != SubscriptionExpired || !strings.Contains(fresh.Note, "Expired") {
		t.Fatalf("want the subscription expired on its line, got %+v", fresh)
	}
	c, _ := f.svc.Get(f.owner.ID)
	if len(c.Messages) != 0 {
		t.Fatalf("an expiry writes nothing into the thread, got %+v", c.Messages)
	}
	f.reactor.Publish(f.jobDone("late"))
	time.Sleep(50 * time.Millisecond)
	if len(f.runner.turns()) != 0 {
		t.Fatal("an expired subscription buys no turn")
	}
}

func TestANothingAnswerPushesNothingAndRingsNobody(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	sub := f.subscribe(t, SubscriptionSpec{Event: "job-done", Task: "SAY_NOTHING when there is nothing", BatchSet: true})

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
	fresh, _ := f.reactor.Get(sub.ID)
	if fresh.Fired != 1 || fresh.Note != "Fired for Job done: quiet, nothing to do." {
		t.Fatalf("want the reaction counted as fired and its line saying nothing to do, got %+v", fresh)
	}
}

func TestQuietAnswerReadsOnlyTheFirstLine(t *testing.T) {
	for text, want := range map[string]bool{
		"NOTHING":                          true,
		"nothing.":                         true,
		"**NOTHING**":                      true,
		"\n\nNOTHING:\n":                   true,
		"NOTHING to do here, all is well.": false,
		"All done.\nNOTHING":               false,
		"":                                 false,
	} {
		if got := quietAnswer(text); got != want {
			t.Fatalf("quietAnswer(%q) = %v, want %v", text, got, want)
		}
	}
}

// The count in front of a chat prompt is one line: how much the cockpit
// wrote since the last chat answer, notes and pushed answers alike, and
// where to read it. The walk stops only at a chat answer, never at a pushed
// one.
func TestTheNextChatPromptCountsWhatTheCockpitWrote(t *testing.T) {
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
	want := "Since your last answer the cockpit wrote 3 notes into this thread. job-list and assistant-show on yourself say what, look only when the user's message needs it.\n\nhow are the jobs"
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
	if got := withNotes(1, "x"); !strings.HasPrefix(got, "Since your last answer the cockpit wrote 1 note into this thread.") {
		t.Fatalf("want the singular, got %q", got)
	}
}

func TestSubscriptionsAndTheirStateSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	f := newEventFixtureIn(t, dir, &fakeRunner{answer: eventAnswer})
	owner := f.create(t)
	cron := f.subscribe(t, SubscriptionSpec{Event: "cron", Spec: "* * * * *", Task: "tick"})
	batched := f.subscribe(t, SubscriptionSpec{Event: "job-done", Task: "batched", Batch: 30 * time.Second, BatchSet: true})
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
		t.Fatalf("want both subscriptions read back from disk, got %+v", listed)
	}
	second.reactor.Recover()
	// The tick is a reaction of its own; the event that waited in the window
	// and the job that closed in the gap were both taken by the one
	// subscription and its window folds them into one reaction.
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

func TestDeletingAnAssistantDropsItsSubscriptions(t *testing.T) {
	f := newEventFixture(t)
	owner := f.create(t)
	f.subscribe(t, SubscriptionSpec{Event: "job-done", Task: "gone with me"})
	path := filepath.Join(f.store.InstanceDir(owner.ID), subscriptionsFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("want the subscriptions next to the jobs, got %v", err)
	}
	if err := f.svc.Delete(owner.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(f.reactor.List()) != 0 {
		t.Fatal("a deleted assistant's subscriptions are gone")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("want the file gone with the directory, got %v", err)
	}
}

func TestSubscribeRefusesWhatCannotFire(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	for _, spec := range []SubscriptionSpec{
		{Event: "job-done"},
		{Event: "nothing-known", Task: "x"},
		{Event: "cron", Task: "x"},
		{Event: "cron", Spec: "60 * * * *", Task: "x"},
		{Event: "job-done", Targets: targets("term-nobody"), Task: "x"},
		{Event: "job-closed", All: true, Task: "x"},
		{Event: "job-done", Spec: "* * * * *", Task: "x"},
	} {
		spec.Owner = f.owner.ID
		if _, err := f.reactor.Subscribe(spec); err == nil {
			t.Fatalf("want %+v refused", spec)
		}
	}
	sub := f.subscribe(t, SubscriptionSpec{Event: "coder-asks", Targets: targets("term-any"), Task: "answer it", Never: true})
	if sub.MaxPerHour != DefaultSubscriptionPerHour || sub.Batch() != DefaultSubscriptionBatch || !sub.ExpiresAt.IsZero() {
		t.Fatalf("want the defaults and no expiry, got %+v", sub)
	}
	if err := f.reactor.Unsubscribe(sub.ID, "somebody-else"); err == nil {
		t.Fatal("another assistant must not remove it")
	}
	if err := f.reactor.Unsubscribe(sub.ID, f.owner.ID); err != nil {
		t.Fatalf("the owner removes it: %v", err)
	}
}

func TestACoderEventNamesTheCoder(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.reactor.SetCoderNamer(func(terminal string) (string, string) { return "wake-ask", "demo" })
	f.subscribe(t, SubscriptionSpec{Event: "coder-asks", Task: "answer it", BatchSet: true})
	f.reactor.Coder("term-1", CoderKindEnded)
	time.Sleep(50 * time.Millisecond)
	if len(f.runner.turns()) != 0 {
		t.Fatal("an ended turn is not a question")
	}
	f.reactor.Coder("term-1", CoderKindAsks)
	answer := f.waitPushed(t, 1)
	if answer.Origin.Headline != "Coder asks a question: wake-ask in demo" {
		t.Fatalf("want the coder named, got %q", answer.Origin.Headline)
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
	sub := f.subscribe(t, SubscriptionSpec{
		Event: "job-closed", All: true, Task: "MAGIC summarize all three",
		Targets: targets("term-one", "term-two", "term-three"), BatchSet: true,
	})
	f.reactor.Publish(f.jobDone("one"))
	f.reactor.Publish(f.jobDone("two"))
	time.Sleep(100 * time.Millisecond)
	if pushed := f.pushed(t); len(pushed) != 0 {
		t.Fatalf("a barrier fired before its last target: %+v", pushed)
	}
	held, _ := f.reactor.Get(sub.ID)
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
	fresh, _ := f.reactor.Get(sub.ID)
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

	sub := f.subscribe(t, SubscriptionSpec{
		Event: "job-closed", All: true, Task: "MAGIC summarize both",
		Targets: targets("term-first", "term-second"), BatchSet: true,
	})
	seeded, _ := f.reactor.Get(sub.ID)
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
// closes with that reason, a subscription waiting for that terminal fires once
// for the deletion, a barrier counts it as arrived, and a subscription with no
// terminal left is removed and said out loud.
func TestADeletedCoderClosesItsJobAndDropsWhatOnlyItCouldFire(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.steerJob(t, "doomed")
	f.steerJob(t, "other")
	asks := f.subscribe(t, SubscriptionSpec{
		Event: "coder-asks", Task: "MAGIC answer it",
		Targets: targets("term-doomed"), BatchSet: true,
	})
	barrier := f.subscribe(t, SubscriptionSpec{
		Event: "job-closed", All: true, Task: "MAGIC summarize both",
		Targets: targets("term-doomed", "term-other"), BatchSet: true,
	})
	anybody := f.subscribe(t, SubscriptionSpec{Event: "coder-asks", Task: "MAGIC any coder", BatchSet: true})

	watcher := NewWatcher(f.svc, NewJobs(f.store), nil, nil)
	dropped := watcher.TerminalDeleted("term-doomed")
	if got := DroppedNote(dropped); got != "1 subscription dropped" {
		t.Fatalf("want the one sentence about what fell, got %q, %+v", got, dropped)
	}
	if _, ok := f.reactor.Get(asks.ID); ok {
		t.Fatal("a subscription whose only terminal is gone still stands")
	}
	if _, ok := f.reactor.Get(anybody.ID); !ok {
		t.Fatal("a subscription without targets must not be touched")
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

// A subscription stored before several targets were possible carries one
// target and its name. It reads as a list of one and still fires on that
// terminal. TODO(v2.0.0)
func TestAStoredSingleTargetReadsAsAListOfOne(t *testing.T) {
	f := newEventFixture(t)
	owner := f.create(t)
	raw := []map[string]any{{
		"id": "aaaabbbbccccdddd", "source": EventCoder, "kind": CoderKindAsks,
		"target": "term-old", "targetName": "old one", "task": "MAGIC answer it",
		"state": string(SubscriptionStanding), "maxPerHour": 6, "batchSeconds": 0,
		"createdAt": f.clock.now(), "updatedAt": f.clock.now(),
	}}
	statefile.Save(filepath.Join(f.store.InstanceDir(owner.ID), subscriptionsFileName), 0o600, raw)

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
// file, the memory and the workspace files, where its answer lands, what
// NOTHING means, and the bounds, so an assistant can set up any listener the
// cockpit offers when the user asks for it. And they do not claim that a
// report lands "here" as if the session knew: a report lands in the thread
// and the assistant hears about it with the user's next message.
func TestTheInstructionsListEveryEventKind(t *testing.T) {
	for _, k := range EventOptions {
		pinned(t, "subscription-new "+k.Name())
	}
	pinned(t,
		"subscription-new cron --cron \"*/30 9-17 * * 1-5\"",
		"subscription-new job-closed --terminal <t1> --terminal <t2> --all",
		"`--terminal` may be repeated",
		"`--all` makes them a barrier instead",
		"Put a barrier on `job-closed` and not on `job-done`",
		"A job that is already closed when you subscribe counts as arrived right away",
		"Deleting a coder ends what was arranged about that terminal",
		"a subscription with no terminal left is removed",
		"Stopping a coder ends its job but keeps its subscriptions",
		"subscription-list",
		"subscription-edit <id> --task",
		"subscription-delete <id>",
		// A standing subscription is changed rather than made again, and what
		// an edit may not touch is said where the change is offered.
		"only the flags you name change anything, the rest stands",
		"Not changeable is the event: another event is another subscription",
		"A subscription that is done or expired is spent and is refused",
		"a target that stays keeps what it reached",
		"a reaction that runs right now, which keeps the task it was given",
		"runs in a session of its own",
		"only the task, this instruction file, the memory and the files in your workspace",
		"self contained or point to a file in",
		"Its answer is pushed into your thread",
		"`NOTHING` on the first line and nothing else, then nothing is pushed",
		"Two events inside the batch window become one turn",
		"`--once` ends it after its first turn",
		"`--until 8h` is the expiry",
		"`--max-per-hour 6` caps the turns it buys",
		"`--batch 30s` is the window",
		"Subscribe when the user asks for a standing reaction",
		"their reports land in your thread, and you hear about them with the user's next message",
		// The handover is one call at the start, and the pattern that calls
		// for it is the relation and not a word the user used.
		"[--then \"<what happens once it is done>\"]",
		"is a handover: wire it with `--then` in the very call that starts the coder",
		"The relation is the trigger, the user does not have to ask for a subscription",
		"`--then` fires once, when that job closes done, and it needs `--done-when`",
		"write it self contained: the project, what to start there, and where the report goes",
		"The sequel of one job you start is the same arrangement made in one call",
	)
	// A check is a narrow turn with a verdict: it never reads as something
	// that waits for a job on its own or carries the work on.
	for _, gone := range []string{
		"its check already wakes you",
		"the DONE of the first is the handover",
		"a check by you",
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
func orphanReaction(t *testing.T, f *eventFixture, sub Subscription, ev CockpitEvent) RunRecord {
	t.Helper()
	c, err := f.svc.Get(f.owner.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	origin := Note{Source: NoteEvent, Headline: ev.Headline, Verdict: ev.Kind, Terminal: ev.Target, Subscription: sub.ID, Task: sub.Task, Count: 1}
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
// with its origin. Here the subscription fires, its reaction is running when
// the first process ends, and the next process over the same state directory
// recovers it: the answer is pushed marked as started without the user with
// its origin, a NOTHING answer pushes nothing and rings nobody, and the
// subscription stands as the fire left it, fired once and, a one shot, done.
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
			sub := f.subscribe(t, SubscriptionSpec{Event: "job-done", Task: "summarize", Once: true, BatchSet: true})

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
				fresh, _ := f.reactor.Get(sub.ID)
				return fresh.Fired == 1 && !fresh.Reacting()
			})
			f.svc.coders = fakeCoders{runner: runner}
			fired, _ := f.reactor.Get(sub.ID)
			if fired.State != SubscriptionDone {
				t.Fatalf("want the one shot done before the restart, got %+v", fired)
			}
			rec := orphanReaction(t, f, sub, ev)
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

			close(runner.block)
			if tc.pushed {
				answer := second.waitPushed(t, 1)
				if answer.ID != rec.MessageID || answer.Content != tc.answer || !answer.Auto || answer.Origin == nil || answer.Origin.Subscription != sub.ID {
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
				if len(second.pushed(t)) != 0 {
					t.Fatal("a NOTHING answer pushes nothing after a restart either")
				}
				select {
				case <-second.news:
					t.Fatal("a NOTHING answer rings nobody")
				case <-time.After(200 * time.Millisecond):
				}
			}
			after, _ := second.reactor.Get(sub.ID)
			if after.Fired != 1 || after.State != SubscriptionDone || after.Reacting() {
				t.Fatalf("want the subscription untouched by the recovery, got %+v", after)
			}
			if second.svc.Reserved("claude", rec.SessionID) {
				t.Fatal("want the reaction's session released once it ended")
			}
		})
	}
}

// edit is Reactor.Edit by the owner, the way the page and the command line
// reach it.
func (f *eventFixture) edit(t *testing.T, id string, spec SubscriptionSpec) (Subscription, string) {
	t.Helper()
	sub, changed, err := f.reactor.Edit(id, f.owner.ID, spec)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	return sub, changed
}

// A typo in the task is one change away, and the change touches nothing else:
// the bounds nobody named stand, and so does everything the subscription
// already did, which is what tells a change from making it again.
func TestAnEditMovesOnlyWhatItNames(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.steerJob(t, "one")
	sub := f.subscribe(t, SubscriptionSpec{
		Event: "job-done", Targets: targets("term-one"), Task: "sumarize it",
		MaxPerHour: 3, Batch: 45 * time.Second, BatchSet: true, Until: 2 * time.Hour,
	})
	f.reactor.Publish(f.jobDone("one"))
	f.clock.advance(time.Minute)
	f.reactor.Tick()
	f.waitPushed(t, 1)
	fired, _ := f.reactor.Get(sub.ID)

	f.clock.advance(time.Minute)
	next, changed := f.edit(t, sub.ID, SubscriptionSpec{Task: "summarize it"})
	if next.Task != "summarize it" || changed != "task" {
		t.Fatalf("want the task changed and said so, got %q and %q", next.Task, changed)
	}
	if next.MaxPerHour != 3 || next.BatchSeconds != 45 || !next.ExpiresAt.Equal(fired.ExpiresAt) || next.Once != fired.Once {
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
	stored, ok := f.reactor.Get(sub.ID)
	if !ok || stored.Task != "summarize it" {
		t.Fatalf("the change was not stored: %+v", stored)
	}
}

// The next reaction is asked the new task. A reaction that already runs keeps
// the old one, it was given it, so nothing is rewritten under a turn.
func TestTheNextReactionTakesTheNewTask(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	sub := f.subscribe(t, SubscriptionSpec{Event: "coder-ended", Task: "the first task", BatchSet: true})
	f.edit(t, sub.ID, SubscriptionSpec{Task: "the second task"})
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
	sub := f.subscribe(t, SubscriptionSpec{
		Event: "job-closed", All: true, Task: "summarize them",
		Targets: targets("term-one", "term-two"), BatchSet: true,
	})
	f.reactor.Publish(f.jobDone("one"))
	held, _ := f.reactor.Get(sub.ID)
	if !held.Targets[0].Met || len(held.Pending) != 1 {
		t.Fatalf("want the first arrival held, got %+v", held)
	}

	next, changed := f.edit(t, sub.ID, SubscriptionSpec{
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
	shrunk, _ := f.edit(t, sub.ID, SubscriptionSpec{Targets: targets("term-two", "term-three"), TargetsSet: true})
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
	sub := f.subscribe(t, SubscriptionSpec{
		Event: "job-closed", All: true, Task: "summarize both",
		Targets: targets("term-first", "term-other"), BatchSet: true,
	})

	f.edit(t, sub.ID, SubscriptionSpec{Targets: targets("term-first", "term-second"), TargetsSet: true})
	next, _ := f.reactor.Get(sub.ID)
	if !next.Targets[1].Met || len(next.Pending) != 1 {
		t.Fatalf("want the closed job counted right away, got %+v", next)
	}
	// And only once: a second edit that names it again does not take it twice.
	f.edit(t, sub.ID, SubscriptionSpec{Task: "summarize both, briefly"})
	again, _ := f.reactor.Get(sub.ID)
	if len(again.Pending) != 1 {
		t.Fatalf("the end was taken twice: %+v", again.Pending)
	}
}

// A changed schedule works its next tick out again; one nobody touched keeps
// the tick it is waiting for.
func TestAnEditOfTheScheduleWorksOutTheNextTick(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	sub := f.subscribe(t, SubscriptionSpec{Event: "cron", Spec: "0 3 * * *", Task: "the nightly summary"})
	stood := sub.NextAt

	same, _ := f.edit(t, sub.ID, SubscriptionSpec{Task: "the nightly summary, briefly"})
	if !same.NextAt.Equal(stood) {
		t.Fatalf("a tick moved without the schedule: %s against %s", same.NextAt, stood)
	}
	next, changed := f.edit(t, sub.ID, SubscriptionSpec{Spec: "*/15  9-17 * * 1-5"})
	if next.Spec != "*/15 9-17 * * 1-5" || changed != "schedule */15 9-17 * * 1-5" {
		t.Fatalf("want the schedule read and said, got %q and %q", next.Spec, changed)
	}
	if !next.NextAt.After(f.clock.now()) || next.NextAt.Equal(stood) {
		t.Fatalf("want the next tick worked out again, got %s", next.NextAt)
	}
	if _, _, err := f.reactor.Edit(sub.ID, f.owner.ID, SubscriptionSpec{Spec: "60 * * * *"}); err == nil {
		t.Fatal("a schedule that cannot be read must be refused")
	}
	if fresh, _ := f.reactor.Get(sub.ID); fresh.Spec != "*/15 9-17 * * 1-5" {
		t.Fatalf("a refused change wrote anyway: %+v", fresh)
	}
}

// What an edit refuses: a subscription that is spent, the event, somebody
// else's, and a bound that does not read.
func TestAnEditRefusesWhatItCannotChange(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	f.steerJob(t, "one")
	sub := f.subscribe(t, SubscriptionSpec{Event: "coder-ended", Task: "answer it", BatchSet: true})

	for _, spec := range []SubscriptionSpec{
		{Event: "coder-asks"},
		{Event: "job-done"},
		{Event: "nothing-known"},
		{Task: strings.Repeat("x", maxSubscriptionTaskRunes+1)},
		{Until: -time.Hour},
		{Batch: -time.Second, BatchSet: true},
		{Spec: "* * * * *"},
		{All: true, AllSet: true, Targets: nil, TargetsSet: true},
	} {
		if _, _, err := f.reactor.Edit(sub.ID, f.owner.ID, spec); err == nil {
			t.Fatalf("want %+v refused", spec)
		}
	}
	if _, _, err := f.reactor.Edit(sub.ID, "somebody-else", SubscriptionSpec{Task: "mine now"}); err == nil {
		t.Fatal("another assistant must not change it")
	}
	if _, _, err := f.reactor.Edit("nothing-like-this", "", SubscriptionSpec{Task: "x"}); err == nil {
		t.Fatal("an id nothing answers to must be refused")
	}
	if fresh, _ := f.reactor.Get(sub.ID); fresh.Task != "answer it" || fresh.Kind != CoderKindEnded {
		t.Fatalf("a refused change wrote anyway: %+v", fresh)
	}

	// A one shot that fired is spent, and so is one that expired: they are
	// refused with a sentence instead of quietly coming back to life.
	once := f.subscribe(t, SubscriptionSpec{Event: "coder-ended", Task: "once only", Once: true, BatchSet: true})
	f.reactor.Coder("term-1", CoderKindEnded)
	f.waitPushed(t, 1)
	if _, _, err := f.reactor.Edit(once.ID, f.owner.ID, SubscriptionSpec{Task: "again"}); err == nil {
		t.Fatal("a subscription that is done must not be changed")
	}
}

// Every bound moves, and the answer names what moved, so the user reads what
// their change did instead of reading the row back.
func TestAnEditMovesEveryBoundAndSaysWhich(t *testing.T) {
	f := newEventFixture(t)
	f.create(t)
	sub := f.subscribe(t, SubscriptionSpec{Event: "coder-asks", Task: "answer it"})

	next, changed := f.edit(t, sub.ID, SubscriptionSpec{
		Once: true, OnceSet: true, Never: true, MaxPerHour: 2, Batch: 90 * time.Second, BatchSet: true,
	})
	if !next.Once || !next.ExpiresAt.IsZero() || next.MaxPerHour != 2 || next.BatchSeconds != 90 {
		t.Fatalf("want every bound moved, got %+v", next)
	}
	if changed != "once, no expiry, 2 turns per hour, batch 90s" {
		t.Fatalf("the answer does not name what moved: %q", changed)
	}
	back, changed := f.edit(t, sub.ID, SubscriptionSpec{Once: false, OnceSet: true, Until: time.Hour})
	if back.Once || back.ExpiresAt.IsZero() {
		t.Fatalf("want the one shot and the expiry back, got %+v", back)
	}
	if !strings.HasPrefix(changed, "standing, until ") {
		t.Fatalf("the answer does not name what moved: %q", changed)
	}
	if _, nothing := f.edit(t, sub.ID, SubscriptionSpec{}); nothing != "" {
		t.Fatalf("a change that changes nothing says so: %q", nothing)
	}
}

// A change is on disk like everything else: a restart reads the new task, and
// a window the change did not touch is still there.
func TestAnEditSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	f := newEventFixtureIn(t, dir, &fakeRunner{answer: eventAnswer})
	owner := f.create(t)
	sub := f.subscribe(t, SubscriptionSpec{Event: "coder-ended", Task: "the old task"})
	f.reactor.Coder("term-1", CoderKindEnded)
	waitFor(t, "the event in the window", func() bool {
		held, _ := f.reactor.Get(sub.ID)
		return len(held.Pending) == 1
	})
	f.edit(t, sub.ID, SubscriptionSpec{Task: "the new task", MaxPerHour: 9})

	again := newEventFixtureIn(t, dir, &fakeRunner{answer: eventAnswer})
	again.owner = owner
	back, ok := again.reactor.Get(sub.ID)
	if !ok || back.Task != "the new task" || back.MaxPerHour != 9 {
		t.Fatalf("the change did not survive: %+v", back)
	}
	if len(back.Pending) != 1 {
		t.Fatalf("the window did not survive the change: %+v", back)
	}
}
