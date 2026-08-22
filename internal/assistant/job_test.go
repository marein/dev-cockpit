package assistant

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/detach"
	"github.com/marein/dev-cockpit/internal/statefile"
	"github.com/marein/dev-cockpit/internal/terminal"
)

// fakeSessions is the coder a check asks about the session its job steers: what it
// last did, whether its turn is over, and whether that reading is a screen.
type fakeSessions struct {
	mu       sync.Mutex
	activity Activity
	err      error
	asked    []string
	// hold blocks the reading of a session, which is where a check spends its
	// time before it asks for a credential. A test that wants to change the job
	// underneath a running check waits here.
	hold chan struct{}
	// gone marks the terminals that are not there any more, so a test can take
	// a session away the way a lost tmux server does.
	gone map[string]bool
}

// Running reports whether the terminal still exists. Everything is there unless
// a test says otherwise, which is the normal case in these checks.
func (s *fakeSessions) Running(coderID, terminal string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.gone[terminal]
}

func (s *fakeSessions) Activity(coderID, terminal string) (Activity, error) {
	s.mu.Lock()
	s.asked = append(s.asked, coderID+"/"+terminal)
	block := s.hold
	s.mu.Unlock()
	if block != nil {
		<-block
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return Activity{}, s.err
	}
	return s.activity, nil
}

func (s *fakeSessions) set(activity Activity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activity = activity
}

func (s *fakeSessions) asks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.asked...)
}

// answering returns a runner that replies to a check with one line and to
// anything else with a plain answer, so a test can tell the two apart.
func answering(verdict string) func(TurnRequest) []Event {
	return func(req TurnRequest) []Event {
		if strings.Contains(req.Prompt, "you are steering") {
			return []Event{{Kind: EventDelta, Text: verdict}}
		}
		return []Event{{Kind: EventDelta, Text: "chat answer"}}
	}
}

type jobFixture struct {
	svc      *Service
	sessions *fakeSessions
	watcher  *Watcher
	store    *Store
	jobs     *JobStore
	runner   *fakeRunner
	news     chan string
}

func newJobFixture(t *testing.T, verdict string) *jobFixture {
	t.Helper()
	runner := &fakeRunner{answer: answering(verdict)}
	svc, store, _ := newTestService(t, runner)
	news := make(chan string, 8)
	svc.SetHooks(func() {}, func(conversationID string) { news <- conversationID })
	fixture := &jobFixture{
		svc: svc, store: store, runner: runner,
		news: news,
	}
	fixture.jobs = &JobStore{path: t.TempDir() + "/jobs.json"}
	// A working coder by default: most checks are about what the answer does,
	// not about a standing job.
	fixture.sessions = &fakeSessions{activity: Activity{Text: "the coder said something"}}
	fixture.watcher = NewWatcher(svc, fixture.jobs, fixture.sessions)
	// A check is not over when the job says so: the verdict lands on the job
	// first and the report is written after it, so the wait of a test can end
	// while the check still writes. This runs before the directories of this
	// fixture are removed and waits it out, see quiesce. The news nobody reads
	// any more is drained while it does, otherwise a report written during the
	// wait blocks on a full channel and nothing ever finishes.
	t.Cleanup(func() {
		read := make(chan struct{})
		go func() {
			for {
				select {
				case <-news:
				case <-read:
					return
				}
			}
		}()
		quiesce(t, fixture.svc, fixture.watcher)
		close(read)
	})
	return fixture
}

// steered creates a conversation with a job on a terminal, the normal starting
// point of every check.
func (f *jobFixture) steered(t *testing.T) (Conversation, Job) {
	t.Helper()
	c, err := f.svc.create("claude", "/projects/demo")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	job, err := f.watcher.Steer(Job{
		Terminal: "term-1",
		Name:     "readme-task",
		Project:  "demo",
		CoderID:  "claude",
		Task:     "Write the README",
		DoneWhen: "README.md exists and mentions the project",
	})
	if err != nil {
		t.Fatalf("steer: %v", err)
	}
	return c, job
}

// waitJobState waits until the job reached a state, which is what a verdict
// produces.
func (f *jobFixture) waitJobState(t *testing.T, terminal string, want JobState) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if w, ok := f.jobs.Get(terminal); ok && w.State == want {
			return w
		}
		time.Sleep(10 * time.Millisecond)
	}
	w, _ := f.jobs.Get(terminal)
	t.Fatalf("want the job %q, got %q", want, w.State)
	return w
}

// waitNote waits until a check left its line on the job.
func (f *jobFixture) waitNote(t *testing.T, terminal string) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if w, ok := f.jobs.Get(terminal); ok && w.Note != "" {
			return w
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("want a line from the check on the job")
	return Job{}
}

// waitWakes waits until the job was checked the given number of times, so no
// test sleeps on a fixed duration.
func (f *jobFixture) waitWakes(t *testing.T, terminal string, want int) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if w, ok := f.jobs.Get(terminal); ok && w.Wakes >= want {
			return w
		}
		time.Sleep(10 * time.Millisecond)
	}
	w, _ := f.jobs.Get(terminal)
	t.Fatalf("want %d checks, got %d", want, w.Wakes)
	return w
}

func TestSteerNeedsATerminalAndGetsItsBudget(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	if _, err := f.watcher.Steer(Job{DoneWhen: "the tests pass"}); err == nil {
		t.Fatal("want a job without a terminal refused")
	}
	job, err := f.watcher.Steer(Job{Terminal: "term-1", DoneWhen: "the tests pass"})
	if err != nil {
		t.Fatalf("steer: %v", err)
	}
	if job.MaxWakes != defaultMaxWakes || job.ExpiresAt.IsZero() {
		t.Fatalf("want a budget and an expiry, got %+v", job)
	}
	if job.State != JobSteering {
		t.Fatalf("want a steering job, got %q", job.State)
	}
	// The empty criterion stays empty in the store, whitespace and all: the
	// requirement lives at the jobs handler's door, for the assistant's own
	// command, and never here.
	bare, err := f.watcher.Steer(Job{Terminal: "term-2", DoneWhen: "  "})
	if err != nil {
		t.Fatalf("a page steer without a criterion has to be allowed: %v", err)
	}
	if bare.DoneWhen != "" {
		t.Fatalf("want an empty criterion stored, got %q", bare.DoneWhen)
	}
}

// A criterion over the bound is refused whole, never stored cut: a check
// judges a job done against this sentence, and half of it is a different
// criterion. The refusal has to say what is wrong, and one that just fits
// still has to pass.
func TestSteerRefusesADoneWhenItWouldHaveToCut(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	long := strings.Repeat("y", maxDoneWhenRunes+1)
	_, err := f.watcher.Steer(Job{Terminal: "term-1", DoneWhen: long})
	if err == nil {
		t.Fatal("want a criterion over the bound refused")
	}
	if !strings.Contains(err.Error(), "too long") || !strings.Contains(err.Error(), "Shorten") {
		t.Fatalf("the refusal has to say what is wrong and what to do, got %q", err.Error())
	}
	if _, ok := f.jobs.Get("term-1"); ok {
		t.Fatal("a refused job must not be stored")
	}

	exact := strings.Repeat("y", maxDoneWhenRunes)
	job, err := f.watcher.Steer(Job{Terminal: "term-1", DoneWhen: exact})
	if err != nil {
		t.Fatalf("a criterion at the bound has to pass: %v", err)
	}
	if job.DoneWhen != exact {
		t.Fatalf("the stored criterion has to stand word for word, got %d runes", len([]rune(job.DoneWhen)))
	}

	// A criterion may be a list, one check per line. The lines survive the
	// store word for word, only the edges and the line endings are normalized;
	// folding is a display concern of the places that need one line.
	listed := "  the tests pass\r\nthe report is written\nnothing was committed\n"
	job, err = f.watcher.Steer(Job{Terminal: "term-2", DoneWhen: listed})
	if err != nil {
		t.Fatalf("steer: %v", err)
	}
	if job.DoneWhen != "the tests pass\nthe report is written\nnothing was committed" {
		t.Fatalf("want the criterion stored with its lines intact, got %q", job.DoneWhen)
	}
}

// A task over its bound is stored cut and the caller hears about it: the
// notice comes from the same rule that cuts, so every route says the same
// sentence and none carries a copy of the bound.
func TestATaskOverItsBoundIsCutWithANotice(t *testing.T) {
	task, notice := TruncateTask("  Write the README  ")
	if task != "Write the README" || notice != "" {
		t.Fatalf("a task within the bound has to stay whole without a notice, got %q, %q", task, notice)
	}

	long := strings.Repeat("y", maxTaskRunes+1)
	cut, notice := TruncateTask(long)
	if !strings.HasSuffix(cut, "…") || len([]rune(cut)) != maxTaskRunes+1 {
		t.Fatalf("want %d kept runes plus the cut mark, got %d runes", maxTaskRunes, len([]rune(cut)))
	}
	if want := fmt.Sprintf("task was cut at %d runes", maxTaskRunes); notice != want {
		t.Fatalf("want the notice %q, got %q", want, notice)
	}

	f := newJobFixture(t, "NOTHING")
	job, err := f.watcher.Steer(Job{Terminal: "term-1", Task: long})
	if err != nil {
		t.Fatalf("a long task must not refuse the job: %v", err)
	}
	if job.Task != cut {
		t.Fatalf("want Add storing the task cut by the same rule, got %d runes", len([]rune(job.Task)))
	}
}

// A multi line criterion reaches the check as lines. Folded into one they read
// as one sentence, and a check that skims it treats the tail as elaboration
// instead of as conditions of their own.
func TestTheWakePromptCarriesADoneWhensLines(t *testing.T) {
	prompt := wakePrompt(Job{
		Terminal: "term-1",
		DoneWhen: "the tests pass\nthe report is written\nnothing was committed",
	}, Activity{Text: "the coder said something"})
	for _, want := range []string{
		"- done when, every line of it:\n",
		"\n    the tests pass\n",
		"\n    the report is written\n",
		"\n    nothing was committed\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt is missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "the tests pass the report") {
		t.Fatalf("the criterion's lines fell back into one sentence:\n%s", prompt)
	}

	single := wakePrompt(Job{Terminal: "term-1", DoneWhen: "the tests pass"}, Activity{Text: "x"})
	if !strings.Contains(single, "- done when: the tests pass\n") {
		t.Fatalf("a one line criterion keeps the plain form:\n%s", single)
	}
}

// A late answer belongs to the job its check was started for. The store keys
// jobs by terminal, and steering the terminal again replaces the entry, so
// without the identity in the check's context the old check would close the
// successor, spend one of its checks and write the old note on it.
func TestALateCheckAnswerDoesNotTouchTheNextJob(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	first, err := f.watcher.Steer(Job{Terminal: "term-1", DoneWhen: "the first report is written"})
	if err != nil {
		t.Fatalf("steer: %v", err)
	}
	seen := checkContext{MessageID: "m1", JobCreatedAt: first.CreatedAt}
	// The successor has to carry its own creation time, or the identities
	// cannot tell the two jobs apart.
	time.Sleep(2 * time.Millisecond)
	second, err := f.watcher.Steer(Job{Terminal: "term-1", DoneWhen: "the second report is written"})
	if err != nil {
		t.Fatalf("steer again: %v", err)
	}
	if second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatal("the two jobs share a creation time, this test proves nothing")
	}

	// The old check reports DONE after the terminal was steered again.
	f.watcher.conclude(first, seen, wakeOutcome{Verdict: VerdictDone, Text: "DONE: the first report is written"}, nil)
	fresh, ok := f.jobs.Get("term-1")
	if !ok {
		t.Fatal("the job is gone")
	}
	if fresh.State != JobSteering {
		t.Fatalf("a late answer closed the successor: %q", fresh.State)
	}
	if fresh.Wakes != 0 {
		t.Fatalf("the successor paid for a check it never asked for: %d wakes", fresh.Wakes)
	}
	if fresh.Note != "" {
		t.Fatalf("the old job's note landed on the successor: %q", fresh.Note)
	}

	// The same for a check that came back without a verdict: no silent count,
	// no note on the successor.
	f.watcher.conclude(first, seen, wakeOutcome{}, errors.New("the check hit its time limit"))
	fresh, _ = f.jobs.Get("term-1")
	if fresh.Silent != 0 || fresh.Note != "" {
		t.Fatalf("a late empty answer left a trace on the successor: %+v", fresh)
	}

	// The successor's own check still lands.
	f.watcher.conclude(second, checkContext{MessageID: "m2", JobCreatedAt: second.CreatedAt},
		wakeOutcome{Verdict: VerdictDone, Text: "DONE: the second report is written"}, nil)
	fresh, _ = f.jobs.Get("term-1")
	if fresh.State != JobDone || fresh.Wakes != 1 {
		t.Fatalf("the successor's own check has to count: %+v", fresh)
	}
	if !strings.Contains(fresh.Note, "second report") {
		t.Fatalf("want the successor's own note, got %q", fresh.Note)
	}
}

// A context without an identity counts as it always did: it cannot be told
// apart from its job, and dropping it would leave the job's CheckingSince
// standing forever.
func TestACheckWithoutAJobIdentityStillCounts(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	job, err := f.watcher.Steer(Job{Terminal: "term-1", DoneWhen: "the tests pass"})
	if err != nil {
		t.Fatalf("steer: %v", err)
	}
	f.watcher.conclude(job, checkContext{MessageID: "m1"},
		wakeOutcome{Verdict: VerdictDone, Text: "DONE: the tests pass"}, nil)
	fresh, _ := f.jobs.Get("term-1")
	if fresh.State != JobDone || fresh.Wakes != 1 {
		t.Fatalf("an identity-less check has to count as before: %+v", fresh)
	}
}

// A check that found nothing must leave no trace: no message in the transcript,
// no news, only the job's own line.
func TestWakeWithoutNewsLeavesNoTrace(t *testing.T) {
	f := newJobFixture(t, "WORKING: still writing the file")
	c, _ := f.steered(t)

	f.watcher.Handle("term-1")
	job := f.waitNote(t, "term-1")

	if !strings.Contains(job.Note, "still writing") {
		t.Fatalf("want the check's line on the job, got %q", job.Note)
	}
	if job.State != JobSteering {
		t.Fatalf("want the job still steering, got %q", job.State)
	}
	fresh, err := f.svc.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(fresh.Messages) != 0 {
		t.Fatalf("want an untouched transcript, got %d messages", len(fresh.Messages))
	}
	select {
	case id := <-f.news:
		t.Fatalf("want no news for a check without news, got one for %s", id)
	default:
	}
}

// A finished job is the whole point: one message, marked as a check, plus the
// cockpit's news so the phone rings.
func TestWakeWithNewsWritesOneMarkedMessageAndNotifies(t *testing.T) {
	f := newJobFixture(t, "DONE: the README is written and mentions the project")
	c, _ := f.steered(t)

	f.watcher.Handle("term-1")
	f.waitJobState(t, "term-1", JobDone)
	fresh, err := f.svc.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(fresh.Messages) != 1 {
		t.Fatalf("want exactly one message, got %d", len(fresh.Messages))
	}
	message := fresh.Messages[0]
	if message.Role != RoleAssistant {
		t.Fatalf("a check never writes as the user, got role %q", message.Role)
	}
	if message.Wake == nil || message.Wake.Terminal != "term-1" || message.Wake.Verdict != string(VerdictDone) {
		t.Fatalf("want the message marked as a check, got %+v", message.Wake)
	}
	// The report names the job on its own, so the notification about it needs
	// no lookup that could answer with a successor.
	if message.Wake.Name != "readme-task" || message.Wake.Project != "demo" {
		t.Fatalf("want the job named and placed in the note, got %+v", message.Wake)
	}
	if !strings.Contains(message.Content, "README is written") {
		t.Fatalf("want the report as the message, got %q", message.Content)
	}
	if strings.Contains(message.Content, "DONE") {
		t.Fatalf("want the verdict out of the text, got %q", message.Content)
	}
	select {
	case id := <-f.news:
		if id != c.ID {
			t.Fatalf("news for %s, want %s", id, c.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("want news for a finished job")
	}
}

// Every gate, one by one: none of them may cost a turn.
func TestWakeGatesRefuseBeforeSpendingATurn(t *testing.T) {
	t.Run("unknown terminal", func(t *testing.T) {
		f := newJobFixture(t, "DONE: x")
		f.steered(t)
		f.watcher.Handle("someone-else")
		if turns := len(f.runner.turns()); turns != 0 {
			t.Fatalf("want no turn for a terminal nobody steers, got %d", turns)
		}
	})
	t.Run("stopped job", func(t *testing.T) {
		f := newJobFixture(t, "DONE: x")
		f.steered(t)
		if err := f.watcher.Release("term-1"); err != nil {
			t.Fatalf("stop: %v", err)
		}
		f.watcher.Handle("term-1")
		if w, _ := f.jobs.Get("term-1"); w.Wakes != 0 {
			t.Fatalf("want no check for a stopped job, got %d", w.Wakes)
		}
	})
	t.Run("budget spent", func(t *testing.T) {
		f := newJobFixture(t, "DONE: x")
		_, job := f.steered(t)
		job.Wakes = job.MaxWakes
		f.jobs.Save(job)
		f.watcher.Handle("term-1")
		w, _ := f.jobs.Get("term-1")
		if w.State != JobExpired {
			t.Fatalf("want the job expired, got %q", w.State)
		}
		if len(f.runner.turns()) != 0 {
			t.Fatal("want no turn once the budget is spent")
		}
	})
	t.Run("expired in time", func(t *testing.T) {
		f := newJobFixture(t, "DONE: x")
		_, job := f.steered(t)
		job.ExpiresAt = time.Now().UTC().Add(-time.Minute)
		f.jobs.Save(job)
		f.watcher.Handle("term-1")
		w, _ := f.jobs.Get("term-1")
		if w.State != JobExpired {
			t.Fatalf("want the job expired, got %q", w.State)
		}
	})
}

// A job outlives the conversation it was started from: it belongs to the
// assistant, and its report lands in whichever conversation is live when it
// comes back. Nothing is lost when the user starts a new one or deletes the old.
func TestAJobOutlivesItsConversation(t *testing.T) {
	f := newJobFixture(t, "DONE: the file is there")
	started, _ := f.steered(t)
	if err := f.svc.Delete(started.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	f.watcher.Handle("term-1")
	f.waitJobState(t, "term-1", JobDone)

	current, ok := f.svc.Current()
	if !ok {
		t.Fatal("want a conversation for the report to land in")
	}
	if current.ID == started.ID {
		t.Fatal("the deleted conversation came back")
	}
	reports := 0
	for _, m := range current.Messages {
		if m.Wake != nil {
			reports++
		}
	}
	if reports != 1 {
		t.Fatalf("want the report in the live conversation, got %d of %d messages", reports, len(current.Messages))
	}
}

// A coder that reports twice while a check runs is worth one more check, not
// two: the second signal coalesces instead of paying twice for the same picture.
func TestSecondSignalCoalescesIntoOneMoreCheck(t *testing.T) {
	f := newJobFixture(t, "WORKING: going")
	release := make(chan struct{})
	var started int
	var mu sync.Mutex
	f.runner.answer = func(req TurnRequest) []Event {
		if !strings.Contains(req.Prompt, "you are steering") {
			return []Event{{Kind: EventDelta, Text: "chat answer"}}
		}
		mu.Lock()
		started++
		first := started == 1
		mu.Unlock()
		if first {
			<-release
		}
		return []Event{{Kind: EventDelta, Text: "WORKING: going"}}
	}
	f.steered(t)

	f.watcher.Handle("term-1")
	// Both arrive while the first check is still inside the runner.
	waitFor(t, "the first check to start", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return started == 1
	})
	f.watcher.Handle("term-1")
	f.watcher.Handle("term-1")
	close(release)

	job := f.waitWakes(t, "term-1", 2)
	time.Sleep(300 * time.Millisecond)
	if job = f.waitWakes(t, "term-1", 2); job.Wakes != 2 {
		t.Fatalf("want exactly two checks, got %d", job.Wakes)
	}
}

// The rule the whole design turns on: the user keeps writing while a check runs,
// and neither turn waits for the other.
func TestChatTurnAndCheckRunAtTheSameTime(t *testing.T) {
	f := newJobFixture(t, "DONE: the job is finished")
	releaseChat := make(chan struct{})
	f.runner.answer = func(req TurnRequest) []Event {
		if strings.Contains(req.Prompt, "you are steering") {
			return []Event{{Kind: EventDelta, Text: "DONE: the job is finished"}}
		}
		return []Event{{Kind: EventDelta, Text: "chat answer"}}
	}
	// The chat turn's process stays open while the check runs start to finish.
	f.runner.hold = func(req TurnRequest) chan struct{} {
		if strings.Contains(req.Prompt, "you are steering") {
			return nil
		}
		return releaseChat
	}
	c, _ := f.steered(t)

	if _, err := f.svc.Send(c.ID, "what is going on?", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, "the chat turn to be running", func() bool { return f.svc.Running(c.ID) })

	// The check runs while the chat turn is still open, and finishes.
	f.watcher.Handle("term-1")
	f.waitJobState(t, "term-1", JobDone)

	// The chat answer lands afterwards, with its own text, and the check's
	// message is still there: the two never wrote over each other.
	close(releaseChat)
	waitIdle(t, f.svc, c.ID)
	fresh, err := f.svc.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var chat, wake int
	for _, m := range fresh.Messages {
		switch {
		case m.Wake != nil:
			wake++
		case m.Role == RoleAssistant && m.Content == "chat answer":
			chat++
		}
	}
	if chat != 1 || wake != 1 {
		t.Fatalf("want one chat answer and one check message, got %d and %d: %+v", chat, wake, fresh.Messages)
	}
	if left := f.svc.runs.List(); len(left) != 0 {
		// The record is the credential: while it stands the token resolves, and
		// retire takes both away in one step.
		t.Fatalf("want every finished turn out of the register, %d left", len(left))
	}
}

// A check whose turn failed says so on the job and keeps it steered: a provider
// error is not a verdict.
func TestFailedCheckLeavesTheJobSteering(t *testing.T) {
	f := newJobFixture(t, "DONE: x")
	f.runner.answer = func(TurnRequest) []Event {
		return []Event{{Kind: EventError, Err: errors.New("The coder could not finish this answer.")}}
	}
	c, _ := f.steered(t)
	f.watcher.Handle("term-1")
	job := f.waitNote(t, "term-1")
	if job.State != JobSteering {
		t.Fatalf("want the job still steering after a failed check, got %q", job.State)
	}
	if !strings.Contains(job.Note, "could not") {
		t.Fatalf("want the failure on the job, got %q", job.Note)
	}
	fresh, _ := f.svc.Get(c.ID)
	if len(fresh.Messages) != 0 {
		t.Fatalf("want no transcript entry for a failed check, got %d", len(fresh.Messages))
	}
}

// The wake session is a session of its own, hidden while it runs and gone
// afterwards: a check leaves no resumable ghost behind.
func TestCheckSessionIsHiddenAndRemoved(t *testing.T) {
	f := newJobFixture(t, "WORKING: going")
	f.runner.exists = true
	c, _ := f.steered(t)
	f.watcher.Handle("term-1")
	f.waitWakes(t, "term-1", 1)

	turns := f.runner.turns()
	last := turns[len(turns)-1]
	if last.SessionID == c.NativeSessionID {
		t.Fatal("want a check to run in a session of its own, not the conversation's")
	}
	if last.Resume {
		t.Fatal("want a fresh session for a check")
	}
	waitFor(t, "the check session to be removed", func() bool {
		for _, id := range f.runner.deleted {
			if id == last.SessionID {
				return true
			}
		}
		return false
	})
	if f.svc.Reserved("claude", last.SessionID) {
		t.Fatal("want the reservation released after the check")
	}
}

func TestParseVerdict(t *testing.T) {
	cases := []struct {
		answer  string
		verdict Verdict
		text    string
	}{
		{"DONE: the tests pass", VerdictDone, "the tests pass"},
		{"done - the tests pass", VerdictDone, "the tests pass"},
		{"**BLOCKED:** it needs a token", VerdictBlocked, "it needs a token"},
		{"WORKING: writing the file", VerdictWorking, "writing the file"},
		{"NOTHING", VerdictNothing, ""},
		{"nothing to report", VerdictNothing, "to report"},
		{"", VerdictNothing, ""},
		// No contract at all is not news, but it is not thrown away either.
		{"I had a look and it seems fine", VerdictWorking, "I had a look and it seems fine"},
		// A model that reasons before it judges: the note starts at the verdict.
		{
			"I will check the actual state rather than trust the summary.WORKING: Round reported clean",
			VerdictWorking, "Round reported clean",
		},
		{"Let me look first.\n\nDONE: the file is there", VerdictDone, "the file is there"},
		{"Thinking about it… BLOCKED: it needs a token", VerdictBlocked, "it needs a token"},
		{"Let me think. **DONE:** the file is there", VerdictDone, "the file is there"},
		// Prose that happens to contain a verdict word is not a verdict, in
		// lower case and in capitals: only the contract form counts.
		{"I am done thinking and it still runs", VerdictWorking, "I am done thinking and it still runs"},
		{"I am DONE with the analysis and it still runs", VerdictWorking, "I am DONE with the analysis and it still runs"},
		// NOTHING is the one verdict the contract spells bare. A check that
		// reasons first and then writes it, here glued to the sentence before
		// it, answered NOTHING, not WORKING. A real check answer.
		{
			"The coder's turn is over but its screen shows 2 background shells still running (the e2e runner). Let me verify the runner is actually alive and not hung.NOTHING The coder is not actually stuck. Its turn ended by design: the e2e runner (docker run dc-e2e node assistant.js) is running as a background shell, and the Playwright chromium processes behind it are alive and consuming CPU right now. The coder explicitly ended its turn to wait for the runner's completion notification, which will re-invoke it to summarize. Nothing is in the way, so I sent nothing; the next signal from the session buys the next check.",
			VerdictNothing,
			"The coder is not actually stuck. Its turn ended by design: the e2e runner (docker run dc-e2e node assistant.js) is running as a background shell, and the Playwright chromium processes behind it are alive and consuming CPU right now. The coder explicitly ended its turn to wait for the runner's completion notification, which will re-invoke it to summarize. Nothing is in the way, so I sent nothing; the next signal from the session buys the next check.",
		},
		// A stray NOTHING never cuts an answer that carries another verdict
		// word in capitals, that answer may be a real report whose form just
		// failed to parse.
		{"The build failed. BLOCKED on the token, NOTHING else moves it", VerdictWorking, "The build failed. BLOCKED on the token, NOTHING else moves it"},
	}
	for _, tc := range cases {
		verdict, text := parseVerdict(tc.answer)
		if verdict != tc.verdict || text != tc.text {
			t.Errorf("%q -> %q %q, want %q %q", tc.answer, verdict, text, tc.verdict, tc.text)
		}
	}
}

// The prompt is what a check reads. It has to carry the job, what the terminal
// shows and the contract, or the answer cannot be parsed at all.
func TestWakePromptCarriesTheJobAndTheContract(t *testing.T) {
	job := Job{
		Terminal: "term-1",
		Name:     "readme-task",
		Project:  "demo",
		Task:     "Write the README",
		DoneWhen: "README.md exists",
	}
	prompt := wakePrompt(job, Activity{Text: "the coder printed this"})
	for _, want := range []string{
		"term-1", "readme-task", "demo", "Write the README", "README.md exists",
		"the coder printed this", "DONE:", "BLOCKED:", "WORKING:", "NOTHING",
		"dev-cockpit assistant terminal-screen term-1", "dev-cockpit assistant coder-send-prompt term-1",
		// A check that works up to its time limit is killed and reaches nobody,
		// so the prompt says what the limit is and what to do instead.
		"two hours",
		// Starting a coder is conditional on the task, projects stay refused.
		"Starting a coder is allowed when the task explicitly calls for that next step",
		"Creating or deleting projects is refused for this turn",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt misses %q:\n%s", want, prompt)
		}
	}
}

// A job that runs out must not stop quietly: the promise of the feature is that
// the user hears about a job without looking, and that includes hearing that
// nobody is looking any more. Exactly one report, then never again.
func TestAJobThatRunsOutReportsOnce(t *testing.T) {
	f := newJobFixture(t, "WORKING: going")
	c, job := f.steered(t)
	job.Wakes = job.MaxWakes
	f.jobs.Save(job)

	f.watcher.Handle("term-1")
	expired := f.waitJobState(t, "term-1", JobExpired)
	if !strings.Contains(expired.Note, "used up") {
		t.Fatalf("want the reason on the job, got %q", expired.Note)
	}
	if len(f.runner.turns()) != 0 {
		t.Fatal("a job that ran out must not spend another turn")
	}

	fresh, err := f.svc.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(fresh.Messages) != 1 {
		t.Fatalf("want exactly one report, got %d messages", len(fresh.Messages))
	}
	message := fresh.Messages[0]
	if message.Role != RoleAssistant || message.Wake == nil || message.Wake.Verdict != string(VerdictExpired) {
		t.Fatalf("want one message marked as a stopped job, got %+v", message.Wake)
	}
	for _, want := range []string{"stopped steering", "readme-task", "used up", "README.md exists"} {
		if !strings.Contains(message.Content, want) {
			t.Fatalf("the report misses %q:\n%s", want, message.Content)
		}
	}
	select {
	case id := <-f.news:
		if id != c.ID {
			t.Fatalf("news for %s, want %s", id, c.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("want news when a job stops steering")
	}

	// A second signal finds a closed job: no second report, no second news.
	f.watcher.Handle("term-1")
	time.Sleep(200 * time.Millisecond)
	again, _ := f.svc.Get(c.ID)
	if len(again.Messages) != 1 {
		t.Fatalf("want the one report to stay the only one, got %d", len(again.Messages))
	}
	select {
	case <-f.news:
		t.Fatal("want no second news for the same job")
	default:
	}
}

// The same when the clock runs out instead of the budget.
func TestAJobThatRanOutOfTimeReportsOnce(t *testing.T) {
	f := newJobFixture(t, "WORKING: going")
	c, job := f.steered(t)
	job.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	f.jobs.Save(job)

	f.watcher.Handle("term-1")
	f.waitJobState(t, "term-1", JobExpired)
	fresh, _ := f.svc.Get(c.ID)
	if len(fresh.Messages) != 1 || !strings.Contains(fresh.Messages[0].Content, "time it was given is over") {
		t.Fatalf("want one report about the time running out, got %+v", fresh.Messages)
	}
}

// A check that used the last of the budget closes the job in the same breath,
// and that report is the only one.
func TestTheLastCheckReportsThatItWasTheLast(t *testing.T) {
	f := newJobFixture(t, "WORKING: one more look")
	c, job := f.steered(t)
	job.Wakes = job.MaxWakes - 1
	f.jobs.Save(job)

	f.watcher.Handle("term-1")
	f.waitJobState(t, "term-1", JobExpired)
	fresh, _ := f.svc.Get(c.ID)
	if len(fresh.Messages) != 1 {
		t.Fatalf("want one report after the last check, got %d", len(fresh.Messages))
	}
	if !strings.Contains(fresh.Messages[0].Content, "one more look") {
		t.Fatalf("want the last check's line carried into the report:\n%s", fresh.Messages[0].Content)
	}
}

// A job that stands still must never be reported as still going. This is the
// standstill the owner saw live: the check said WORKING, sent nothing, and
// nobody heard anything while nothing happened.
func TestAStandingJobIsNotReportedAsWorking(t *testing.T) {
	f := newJobFixture(t, "WORKING: the coder is still on it")
	f.sessions.set(Activity{Text: "the coder said something", Finished: true})
	c, _ := f.steered(t)

	f.watcher.Handle("term-1")
	f.waitJobState(t, "term-1", JobBlocked)

	fresh, err := f.svc.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(fresh.Messages) != 1 {
		t.Fatalf("want the standstill reported once, got %d messages", len(fresh.Messages))
	}
	report := fresh.Messages[0]
	if report.Wake == nil || report.Wake.Verdict != string(VerdictBlocked) {
		t.Fatalf("want it reported as blocked, got %+v", report.Wake)
	}
	for _, want := range []string{"is idle and the job is not done", "did not send it anything",
		"README.md exists", "the coder is still on it", "needs a decision"} {
		if !strings.Contains(report.Content, want) {
			t.Fatalf("the report misses %q:\n%s", want, report.Content)
		}
	}
	select {
	case <-f.news:
	case <-time.After(2 * time.Second):
		t.Fatal("want news for a job that stands still")
	}
}

// The other allowed answer: the check steers the idle coder itself and then
// WORKING is the truth, so the user is not bothered.
func TestASteeringCheckMayReportWorkingOnAnIdleCoder(t *testing.T) {
	f := newJobFixture(t, "WORKING: sent it the next step")
	f.sessions.set(Activity{Text: "the coder said something", Finished: true})
	c, _ := f.steered(t)
	// The runner answers like a check that used its one allowed write: sending
	// to the steered terminal is what the input path records as assistant input.
	f.runner.answer = func(req TurnRequest) []Event {
		if !strings.Contains(req.Prompt, "you are steering") {
			return []Event{{Kind: EventDelta, Text: "chat answer"}}
		}
		f.watcher.NoteAssistantInput("term-1")
		return []Event{{Kind: EventDelta, Text: "WORKING: sent it the next step"}}
	}

	f.watcher.Handle("term-1")
	job := f.waitNote(t, "term-1")
	if job.State != JobSteering {
		t.Fatalf("want the job still steering, got %q", job.State)
	}
	if !strings.Contains(job.Note, "next step") {
		t.Fatalf("want the check's line on the job, got %q", job.Note)
	}
	fresh, _ := f.svc.Get(c.ID)
	if len(fresh.Messages) != 0 {
		t.Fatalf("a steered job is not news, got %d messages", len(fresh.Messages))
	}
}

// Calling a job off while its check runs means it is off. A late answer must
// not reopen the job or push a report the user already said they did not want.
func TestAJobCalledOffWhileACheckRunsStaysOff(t *testing.T) {
	f := newJobFixture(t, "DONE: the file is there")
	c, _ := f.steered(t)
	f.runner.answer = func(req TurnRequest) []Event {
		if strings.Contains(req.Prompt, "you are steering") {
			// The user hits stop while the check is thinking.
			if err := f.watcher.Release("term-1"); err != nil {
				t.Errorf("stop: %v", err)
			}
			return []Event{{Kind: EventDelta, Text: "DONE: the file is there"}}
		}
		return []Event{{Kind: EventDelta, Text: "chat answer"}}
	}

	f.watcher.Handle("term-1")
	waitFor(t, "the check to finish", func() bool {
		f.watcher.mu.Lock()
		defer f.watcher.mu.Unlock()
		return len(f.watcher.running) == 0
	})

	job, _ := f.jobs.Get("term-1")
	if job.State != JobStopped {
		t.Fatalf("want the job still stopped, got %q", job.State)
	}
	fresh, _ := f.svc.Get(c.ID)
	if len(fresh.Messages) != 0 {
		t.Fatalf("a job that was called off reported anyway, got %d messages", len(fresh.Messages))
	}
}

// Release takes the actor away: a check that is still running on the job is
// killed at once, so nothing is paid to an end nobody wants, and a dead check
// can write nothing any more. What the kill leaves behind clears the running
// mark and nothing else: the stopped job keeps its state and its silence.
func TestReleaseKillsTheRunningCheck(t *testing.T) {
	f := newJobFixture(t, "DONE: never delivered")
	f.runner.block = make(chan struct{})
	c, _ := f.steered(t)

	f.watcher.Handle("term-1")
	waitFor(t, "the check to hold a turn", func() bool {
		f.svc.mu.Lock()
		defer f.svc.mu.Unlock()
		return len(f.svc.running) > 0
	})

	if err := f.watcher.Release("term-1"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	waitFor(t, "the killed check to conclude", func() bool {
		f.svc.mu.Lock()
		defer f.svc.mu.Unlock()
		return len(f.svc.running) == 0
	})
	waitFor(t, "the job to drop its running mark", func() bool {
		job, _ := f.jobs.Get("term-1")
		return !job.Checking()
	})
	job, _ := f.jobs.Get("term-1")
	if job.State != JobStopped {
		t.Fatalf("want the job stopped, got %q", job.State)
	}
	if job.Wakes != 0 || job.Silent != 0 {
		t.Fatalf("a killed check must not count, got %d wakes and %d silent", job.Wakes, job.Silent)
	}
	if strings.Contains(job.Note, "verdict") {
		t.Fatalf("the kill wrote noise on the stopped job: %q", job.Note)
	}
	fresh, _ := f.svc.Get(c.ID)
	if len(fresh.Messages) != 0 {
		t.Fatalf("a killed check reported anyway, got %d messages", len(fresh.Messages))
	}
}

// A working coder keeps its quiet WORKING: the rule is about a standstill, not
// about progress reports.
func TestAMovingCoderKeepsItsWorkingVerdict(t *testing.T) {
	f := newJobFixture(t, "WORKING: still writing")
	f.sessions.set(Activity{Text: "the coder said something"})
	c, _ := f.steered(t)

	f.watcher.Handle("term-1")
	job := f.waitNote(t, "term-1")
	if job.State != JobSteering {
		t.Fatalf("want the job still steering, got %q", job.State)
	}
	fresh, _ := f.svc.Get(c.ID)
	if len(fresh.Messages) != 0 {
		t.Fatalf("want no message for a working coder, got %d", len(fresh.Messages))
	}
}

// The prompt says what the check is for: find out what is in the way and get
// the coder going again. The rule that a WORKING has to come with
// something sent travels with it, because that is what the watcher enforces.
func TestWakePromptAsksToUnblockTheCoder(t *testing.T) {
	job := Job{Terminal: "term-1", DoneWhen: "the file exists"}
	prompt := wakePrompt(job, Activity{Text: "nothing moved"})
	for _, want := range []string{
		"is not moving", "what is in the way", "turned into BLOCKED",
		// Examples of what unblocking looks like, not a list of cases to work
		// through: the model decides from the screen.
		"API error", "/compact", "keys", "Examples",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt misses %q:\n%s", want, prompt)
		}
	}
}

// The opening matches what bought the check. Most checks are bought by a
// report, the coder finished or asked something, and its judge used to be
// greeted with a stall story. A coder whose turn is over gets the report
// frame, the job stands to be judged against its criterion, and only a
// session that stands still mid work keeps the not moving opening.
func TestWakePromptOpensWithWhatBoughtTheCheck(t *testing.T) {
	job := Job{Terminal: "term-1", DoneWhen: "the file exists"}
	reported := wakePrompt(job, Activity{Text: "the turn is over", Finished: true})
	for _, want := range []string{
		"A coder you are steering reported.",
		"stands to be judged against its criterion",
	} {
		if !strings.Contains(reported, want) {
			t.Fatalf("the report prompt misses %q:\n%s", want, reported)
		}
	}
	if strings.Contains(reported, "is not moving") {
		t.Fatalf("the report prompt opens with the stall story:\n%s", reported)
	}
	stalled := wakePrompt(job, Activity{Text: "the same picture"})
	if !strings.Contains(stalled, "A coder you are steering is not moving.") {
		t.Fatalf("the stall prompt misses its opening:\n%s", stalled)
	}
	if strings.Contains(stalled, "stands to be judged") {
		t.Fatalf("the stall prompt carries the report frame:\n%s", stalled)
	}
	// The advice for a stuck coder travels in both, because a reported job
	// that is not done needs the same next step.
	for _, prompt := range []string{reported, stalled} {
		if !strings.Contains(prompt, "what is in the way") {
			t.Fatalf("the prompt misses the unblock advice:\n%s", prompt)
		}
	}
}

// A screen carries the coder's input line, and whatever stands in it is the
// coder's own suggestion. One sentence says so, and only when the reading really
// is a screen: a transcript has no input line. The sentence has to close both
// doors, movement and ownership, because that line is the one thing on a screen
// that looks like somebody typed it.
func TestWakePromptWarnsAboutTheInputLineOnlyForAScreen(t *testing.T) {
	job := Job{Terminal: "term-1", DoneWhen: "the file exists"}
	screen := wakePrompt(job, Activity{Text: "a picture", Screen: true})
	for _, want := range []string{
		"input line", "Nobody typed it", "belongs to nobody",
		"says nothing about whether anything is moving",
	} {
		if !strings.Contains(screen, want) {
			t.Fatalf("the screen prompt misses %q:\n%s", want, screen)
		}
	}
	transcript := wakePrompt(job, Activity{Text: "user: do it\ncoder: done"})
	if strings.Contains(transcript, "input line") {
		t.Fatalf("the transcript prompt talks about a screen:\n%s", transcript)
	}
	// Whatever the source, the raw screen is one command away and that is where
	// the check looks for what stopped the coder.
	for _, prompt := range []string{screen, transcript} {
		if !strings.Contains(prompt, "dev-cockpit assistant terminal-screen term-1") {
			t.Fatalf("the prompt does not offer the raw screen:\n%s", prompt)
		}
	}
}

// The check asks the coder of the job, not a terminal: that is the whole point
// of the capability.
func TestAWakeAsksTheCoderOfTheJob(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	f.steered(t)

	f.watcher.Handle("term-1")
	f.waitWakes(t, "term-1", 1)

	asks := f.sessions.asks()
	if len(asks) != 1 || asks[0] != "claude/term-1" {
		t.Fatalf("want the job's coder asked once, got %v", asks)
	}
}

// A coder that cannot be asked any more is not working, and the check still
// runs: what it left behind may well answer the criterion.
func TestACoderThatIsGoneStillGetsOneCheck(t *testing.T) {
	f := newJobFixture(t, "WORKING: still going")
	f.sessions.err = errors.New("No active coder with that id.")
	c, _ := f.steered(t)

	f.watcher.Handle("term-1")
	f.waitJobState(t, "term-1", JobBlocked)

	fresh, _ := f.svc.Get(c.ID)
	if len(fresh.Messages) != 1 {
		t.Fatalf("want one report about the job, got %d", len(fresh.Messages))
	}
}

// A blocked job asked the user for a decision. An assistant send to that coder
// is the decision, so the job takes itself up again instead of leaving the
// coder to work on with nobody steering.
func TestSendingToABlockedJobTakesItUpAgain(t *testing.T) {
	f := newJobFixture(t, "BLOCKED: it needs a token")
	f.sessions.set(Activity{Text: "the coder said something", Finished: true})
	f.steered(t)

	f.watcher.Handle("term-1")
	blocked := f.waitJobState(t, "term-1", JobBlocked)
	if blocked.Wakes != 1 {
		t.Fatalf("want the check counted, got %d", blocked.Wakes)
	}

	f.watcher.NoteAssistantInput("term-1")

	job, _ := f.jobs.Get("term-1")
	if job.State != JobSteering {
		t.Fatalf("want the job steering again, got %q", job.State)
	}
	if job.Wakes != 0 || !job.LastWakeAt.IsZero() {
		t.Fatalf("want a fresh budget for the new leg, got %d checks", job.Wakes)
	}
	if !strings.Contains(job.Note, "Steering again") {
		t.Fatalf("want the job to say what happened, got %q", job.Note)
	}
}

// The other closed states are decisions or limits of their own. A send must
// not undo them behind the user's back, the page has one tap for it.
func TestSendingToAClosedJobLeavesItClosed(t *testing.T) {
	for _, state := range []JobState{JobStopped, JobDone, JobExpired} {
		f := newJobFixture(t, "NOTHING")
		c, job := f.steered(t)
		job.State = state
		f.jobs.Save(job)

		f.watcher.NoteAssistantInput("term-1")

		fresh, _ := f.jobs.Get("term-1")
		if fresh.State != state {
			t.Fatalf("a send reopened a %q job as %q", state, fresh.State)
		}
		if fresh.LastAssistantInputAt.IsZero() {
			t.Fatalf("the input was not recorded on a %q job", state)
		}
		_ = c
	}
}

// A check is counted when it came back, not when it started: a check a restart
// killed never produced an answer and must not cost one of the ten. While it
// runs, the job says so, which is what the page shows.
func TestAWakeIsCountedWhenItComesBack(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	release := make(chan struct{})
	f.runner.answer = func(req TurnRequest) []Event {
		if strings.Contains(req.Prompt, "you are steering") {
			<-release
			return []Event{{Kind: EventDelta, Text: "NOTHING"}}
		}
		return []Event{{Kind: EventDelta, Text: "chat answer"}}
	}
	f.steered(t)

	f.watcher.Handle("term-1")
	waitFor(t, "the job to say a check is running", func() bool {
		job, ok := f.jobs.Get("term-1")
		return ok && job.Checking()
	})
	if job, _ := f.jobs.Get("term-1"); job.Wakes != 0 {
		t.Fatalf("a check that has not answered already cost %d", job.Wakes)
	}
	close(release)

	job := f.waitWakes(t, "term-1", 1)
	if job.Checking() {
		t.Fatal("the job still says a check is running")
	}
	if job.LastWakeAt.IsZero() {
		t.Fatal("the time of the check was not recorded")
	}
}

// What a killed process leaves behind on a job: a mark that says a check is
// running while nothing is. The next start clears it and says why.
func TestRecoverClearsTheMarkOfAnInterruptedCheck(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	_, job := f.steered(t)
	job.CheckingSince = time.Now().UTC().Add(-time.Hour)
	f.jobs.Save(job)

	f.watcher.Recover(nil)
	// Recovering also looks again, so the mark has settled for good only once
	// that check came back.
	f.waitWakes(t, "term-1", 1)

	fresh, _ := f.jobs.Get("term-1")
	if fresh.Checking() {
		t.Fatal("the mark of the interrupted check is still standing")
	}
	if !strings.Contains(fresh.Note, "interrupted by a restart") {
		t.Fatalf("the job does not say what happened: %q", fresh.Note)
	}
	if fresh.State != JobSteering {
		t.Fatalf("recovering closed the job: %q", fresh.State)
	}
}

// orphanCheck leaves behind what a server killed mid check leaves behind: a job
// that says a check is running, an entry in the run register, and a process
// still writing its verdict into a file. Nothing follows it.
func orphanCheck(t *testing.T, svc *Service, conversationID, terminalID string, runner *fakeRunner) RunRecord {
	t.Helper()
	c, err := svc.Get(conversationID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	sessionID, err := terminal.NewKey()
	if err != nil {
		t.Fatalf("session id: %v", err)
	}
	svc.reserve(c.CoderID, sessionID)
	a, err := svc.launch(runner, TurnRequest{
		SessionID: sessionID,
		Workdir:   c.ProjectPath,
		Prompt:    "A coder you are steering is not moving.",
	}, RunRecord{
		ID:        statefile.NewID(),
		Kind:      RunCheck,
		MessageID: statefile.NewID(),
		CoderID:   c.CoderID,
		SessionID: sessionID,
		Terminal:  terminalID,
		Deadline:  time.Now().UTC().Add(wakeTimeout),
	})
	if err != nil {
		t.Fatalf("launch the check: %v", err)
	}
	return a.rec
}

// A check outlives a restart the same way a chat turn does: the turn keeps
// running, the new server reads its file on, and the verdict reaches the job and
// the transcript once. Without this a restart would pay for a check nobody ever
// hears the answer to, and start a second one on the same job.
func TestACheckSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{
		events: []Event{{Kind: EventDelta, Text: "DONE: the job "}},
		after:  []Event{{Kind: EventDelta, Text: "is finished"}},
		block:  make(chan struct{}),
	}
	svc, _, _, runs := newTestServiceIn(t, dir, runner)
	c, err := svc.create("claude", "/projects/demo")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	jobs := &JobStore{path: filepath.Join(dir, "jobs.json")}
	jobs.Save(Job{
		Terminal:      "term-1",
		CoderID:       "claude",
		DoneWhen:      "the file is there",
		State:         JobSteering,
		MaxWakes:      defaultMaxWakes,
		CreatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().UTC().Add(defaultJobTTL),
		CheckingSince: time.Now().UTC(),
	})
	rec := orphanCheck(t, svc, c.ID, "term-1", runner)
	if !detach.Alive(rec.PID, rec.Lock) {
		t.Fatal("want the check to still be running")
	}

	// The restart: a new service and a new watcher over the same state.
	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	news := make(chan string, 4)
	restarted.SetHooks(func() {}, func(conversationID string) { news <- conversationID })
	watcher := NewWatcher(restarted, jobs, &fakeSessions{})

	adopted := restarted.Recover()
	if len(adopted) != 1 || adopted[0].Terminal != "term-1" {
		t.Fatalf("want the running check handed to the watcher, got %+v", adopted)
	}
	watcher.Recover(adopted)

	close(runner.block)
	waitFor(t, "the report to be written", func() bool {
		fresh, err := restarted.Get(c.ID)
		if err != nil {
			return false
		}
		for _, m := range fresh.Messages {
			if m.Wake != nil {
				return true
			}
		}
		return false
	})

	job, _ := jobs.Get("term-1")
	if job.State != JobDone {
		t.Fatalf("want the job closed on the verdict, got %q", job.State)
	}
	if job.Wakes != 1 {
		t.Fatalf("want the check counted once, got %d", job.Wakes)
	}
	if job.Checking() {
		t.Fatal("want the running mark cleared")
	}

	fresh, err := restarted.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var reports []Message
	for _, m := range fresh.Messages {
		if m.Wake != nil {
			reports = append(reports, m)
		}
	}
	if len(reports) != 1 {
		t.Fatalf("want exactly one report, got %d: %+v", len(reports), fresh.Messages)
	}
	if reports[0].ID != rec.MessageID {
		t.Fatalf("want the report to carry the id the check reserved, got %q", reports[0].ID)
	}
	if reports[0].Content != "the job is finished" {
		t.Fatalf("want the whole verdict, including the part written after the restart, got %q", reports[0].Content)
	}
	// The message is written first and the news goes out right after it, so a
	// look that happens to land between the two says nothing. Waiting for it is
	// the assertion; only a news that never comes is a failure.
	select {
	case <-news:
	case <-time.After(2 * time.Second):
		t.Fatal("want the report to raise news")
	}
	if len(runs.List()) != 0 {
		t.Fatal("want the register empty once the check ended")
	}
}

// A check that concluded while the server was down delivers its verdict no
// matter how long the downtime was. A deadline that passed in the meantime
// means nothing to a process that is already done writing, and must not race
// the reading of its file.
func TestAnOverdueFinishedCheckStillDeliversItsVerdict(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{
		events: []Event{{Kind: EventDelta, Text: "DONE: the job is finished"}},
	}
	svc, _, _, runs := newTestServiceIn(t, dir, runner)
	c, err := svc.create("claude", "/projects/demo")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	jobs := &JobStore{path: filepath.Join(dir, "jobs.json")}
	jobs.Save(Job{
		Terminal:      "term-1",
		CoderID:       "claude",
		DoneWhen:      "the file is there",
		State:         JobSteering,
		MaxWakes:      defaultMaxWakes,
		CreatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().UTC().Add(defaultJobTTL),
		CheckingSince: time.Now().UTC(),
	})
	rec := orphanCheck(t, svc, c.ID, "term-1", runner)
	waitFor(t, "the check to finish on its own", func() bool { return !detach.Alive(rec.PID, rec.Lock) })
	// The downtime was longer than the check's time budget.
	runs.Update(rec.ID, func(r *RunRecord) { r.Deadline = time.Now().UTC().Add(-time.Minute) })

	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	watcher := NewWatcher(restarted, jobs, &fakeSessions{})

	adopted := restarted.Recover()
	if len(adopted) != 1 {
		t.Fatalf("want the finished check handed to the watcher, got %+v", adopted)
	}
	watcher.Recover(adopted)

	waitFor(t, "the verdict to close the job", func() bool {
		job, ok := jobs.Get("term-1")
		return ok && job.State == JobDone
	})
	fresh, err := restarted.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	found := false
	for _, m := range fresh.Messages {
		if m.Wake != nil {
			if m.Content != "the job is finished" {
				t.Fatalf("want the verdict, not a timeout, got %q", m.Content)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("want the verdict reported into the conversation")
	}
}

// Concluding the same check twice writes one message. The id is reserved when
// the check starts, so a report that is applied again lands on a message that
// is already there.
func TestAReportIsWrittenOnce(t *testing.T) {
	f := newJobFixture(t, "DONE: x")
	c, _ := f.steered(t)

	job := Job{Terminal: "term-1", Name: "readme-task", Project: "cockpit"}
	first := f.svc.recordWake(job, "fixed-id", VerdictDone, "the job is finished")
	second := f.svc.recordWake(job, "fixed-id", VerdictDone, "the job is finished")
	if first != "fixed-id" || second != "fixed-id" {
		t.Fatalf("want both to name the same message, got %q and %q", first, second)
	}
	fresh, _ := f.svc.Get(c.ID)
	reports := 0
	for _, m := range fresh.Messages {
		if m.Wake != nil {
			reports++
		}
	}
	if reports != 1 {
		t.Fatalf("want one report, got %d", reports)
	}
}

// A report carries the name of the job it was written for, and keeps it. Read
// back from the store later the name would be the successor's: the store keys
// jobs by terminal, and a terminal is steered again all the time.
func TestAReportCarriesTheJobsName(t *testing.T) {
	f := newJobFixture(t, "DONE: the README is written")
	c, _ := f.steered(t)

	f.watcher.Handle("term-1")
	f.waitJobState(t, "term-1", JobDone)
	fresh, err := f.svc.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(fresh.Messages) != 1 || fresh.Messages[0].Wake == nil {
		t.Fatalf("want one report marked as a check, got %+v", fresh.Messages)
	}
	if name := fresh.Messages[0].Wake.Name; name != "readme-task" {
		t.Fatalf("want the job's name on the report, got %q", name)
	}

	if _, err := f.watcher.Steer(Job{
		Terminal: "term-1",
		Name:     "another-task",
		Project:  "demo",
		CoderID:  "claude",
		DoneWhen: "the tests pass",
	}); err != nil {
		t.Fatalf("steer: %v", err)
	}
	again, _ := f.svc.Get(c.ID)
	if name := again.Messages[0].Wake.Name; name != "readme-task" {
		t.Fatalf("want the report to keep the name of its own job, got %q", name)
	}
}

// A job that ran out writes the same kind of report, so it names its job too.
func TestTheReportOfAJobThatRanOutNamesIt(t *testing.T) {
	f := newJobFixture(t, "WORKING: going")
	c, job := f.steered(t)
	job.Wakes = job.MaxWakes
	f.jobs.Save(job)

	f.watcher.Handle("term-1")
	f.waitJobState(t, "term-1", JobExpired)
	fresh, _ := f.svc.Get(c.ID)
	if len(fresh.Messages) != 1 || fresh.Messages[0].Wake == nil {
		t.Fatalf("want one report, got %+v", fresh.Messages)
	}
	if name := fresh.Messages[0].Wake.Name; name != "readme-task" {
		t.Fatalf("want the job's name on the report, got %q", name)
	}
}

// The session a check runs in is recognizable by its name, which is the only
// thing that survives the process that reserved it.
func TestACheckSessionIsRecognizableByItsName(t *testing.T) {
	if !IsCheckSession(wakeSessionName("a conversation")) {
		t.Fatal("a check's own session is not recognized")
	}
	if IsCheckSession("readme-task") {
		t.Fatal("a coder session was taken for a check")
	}
	if IsCheckSession("") {
		t.Fatal("an unnamed session was taken for a check")
	}
}

// A check that never answers is a result, not silence. It costs no wake, the job
// says what happened, and nobody has to guess why nothing moved.
func TestACheckWithoutAVerdictCostsNoWakeAndSaysSo(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	f.runner.answer = func(req TurnRequest) []Event {
		if strings.Contains(req.Prompt, "you are steering") {
			return []Event{{Kind: EventError, Err: errors.New("The turn hit its time limit before the coder answered.")}}
		}
		return []Event{{Kind: EventDelta, Text: "chat answer"}}
	}
	c, _ := f.steered(t)

	f.watcher.Handle("term-1")
	job := f.waitNote(t, "term-1")

	if job.Wakes != 0 {
		t.Fatalf("a check without a verdict cost %d of the budget", job.Wakes)
	}
	if !strings.Contains(job.Note, "without a verdict") || !strings.Contains(job.Note, "time limit") {
		t.Fatalf("the job does not say what happened: %q", job.Note)
	}
	if job.State != JobSteering {
		t.Fatalf("one silent check closed the job: %q", job.State)
	}
	fresh, _ := f.svc.Get(c.ID)
	if len(fresh.Messages) != 0 {
		t.Fatalf("the first silent check already wrote to the user: %d messages", len(fresh.Messages))
	}
}

// A check that answers nothing at all is the same case: an empty answer used to
// parse as "nothing to report", which is how a check disappeared without a
// trace.
func TestAnEmptyAnswerIsNotNothingToReport(t *testing.T) {
	f := newJobFixture(t, "")
	f.steered(t)

	f.watcher.Handle("term-1")
	job := f.waitNote(t, "term-1")

	if !strings.Contains(job.Note, "without a verdict") {
		t.Fatalf("an empty answer passed as a quiet check: %q", job.Note)
	}
	if job.Wakes != 0 {
		t.Fatalf("an empty answer cost %d of the budget", job.Wakes)
	}
}

// Not counting them cannot mean retrying forever. The second one in a row closes
// the job and tells the user, because nothing is checking that coder any more.
func TestTwoSilentChecksInARowReachTheUser(t *testing.T) {
	f := newJobFixture(t, "")
	c, _ := f.steered(t)

	f.watcher.Handle("term-1")
	f.waitNote(t, "term-1")
	f.watcher.Handle("term-1")
	f.waitJobState(t, "term-1", JobBlocked)

	fresh, _ := f.svc.Get(c.ID)
	if len(fresh.Messages) != 1 {
		t.Fatalf("want one report about the checks failing, got %d", len(fresh.Messages))
	}
	if !strings.Contains(fresh.Messages[0].Content, "cannot check on") {
		t.Fatalf("the report does not say what is wrong:\n%s", fresh.Messages[0].Content)
	}
	select {
	case <-f.news:
	case <-time.After(2 * time.Second):
		t.Fatal("want news when the checks themselves stop working")
	}
}

// A verdict clears the record: one failed check followed by a real answer is a
// job that works, not a job on its way out.
func TestAVerdictClearsTheSilentRun(t *testing.T) {
	f := newJobFixture(t, "")
	f.steered(t)

	f.watcher.Handle("term-1")
	f.waitNote(t, "term-1")
	if job, _ := f.jobs.Get("term-1"); job.Silent != 1 {
		t.Fatalf("want the silent check recorded, got %d", job.Silent)
	}

	f.runner.answer = answering("WORKING: still writing")
	f.watcher.Handle("term-1")
	f.waitWakes(t, "term-1", 1)
	job, _ := f.jobs.Get("term-1")
	if job.Silent != 0 {
		t.Fatalf("want the silent run cleared, got %d", job.Silent)
	}
}

// The signal that started a check is spent, its inbox file is gone, and nothing
// repeats it. So the job is what remembers, and the next start checks again
// instead of waiting for a signal that never comes.
func TestRecoverChecksAgainAfterARestart(t *testing.T) {
	f := newJobFixture(t, "DONE: it is finished")
	_, job := f.steered(t)
	job.CheckingSince = time.Now().UTC().Add(-time.Hour)
	f.jobs.Save(job)

	f.watcher.Recover(nil)

	f.waitJobState(t, "term-1", JobDone)
	if fresh, _ := f.jobs.Get("term-1"); fresh.Checking() {
		t.Fatal("the interrupted mark is still standing")
	}
}

// A job ends most often by going quiet, so its time has to run out without a
// signal. Everything else here goes through Handle; this one never calls it.
func TestAJobRunsOutOfTimeWithoutASignal(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	c, job := f.steered(t)
	job.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	f.jobs.Save(job)

	f.watcher.SweepExpired()

	fresh, _ := f.jobs.Get("term-1")
	if fresh.State != JobExpired {
		t.Fatalf("want the job expired without a signal, got %q", fresh.State)
	}
	conversation, _ := f.svc.Get(c.ID)
	if len(conversation.Messages) != 1 {
		t.Fatalf("want the one report a job that ran out writes, got %d", len(conversation.Messages))
	}
	if !strings.Contains(conversation.Messages[0].Content, "stopped steering") {
		t.Fatalf("the report does not say what happened:\n%s", conversation.Messages[0].Content)
	}
	// And once: a second sweep finds a closed job.
	f.watcher.SweepExpired()
	conversation, _ = f.svc.Get(c.ID)
	if len(conversation.Messages) != 1 {
		t.Fatalf("the sweep reported twice, got %d messages", len(conversation.Messages))
	}
}

// A job whose budget is used up is closed by the sweep too, not only by the next
// signal that may never come.
func TestAJobOutOfChecksIsClosedWithoutASignal(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	_, job := f.steered(t)
	job.Wakes = job.MaxWakes
	f.jobs.Save(job)

	f.watcher.SweepExpired()

	if fresh, _ := f.jobs.Get("term-1"); fresh.State != JobExpired {
		t.Fatalf("want the job expired, got %q", fresh.State)
	}
}

// A signal buys its check at once: the quiet window is gone, it swallowed the
// done signals of short jobs, which then waited for the heartbeat. What keeps
// a burst from paying twice is the per-job coalescing (running and pending),
// which has its own test above.
func TestASignalBuysItsCheckAtOnce(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	f.steered(t)

	f.watcher.Handle("term-1")
	f.waitWakes(t, "term-1", 1)
}

// Both writers of a job own different fields and write at the same time: the
// input route records the assistant's send, the check its counters. Read,
// change and write has to happen under one lock, or the later writer puts back
// what it read before the other one wrote.
func TestConcurrentWritersDoNotLoseEachOther(t *testing.T) {
	store := &JobStore{path: t.TempDir() + "/jobs.json"}
	store.Save(Job{Terminal: "term-1", State: JobSteering})

	const rounds = 100
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			store.Update("term-1", func(w *Job) bool { w.Wakes++; return true })
		}()
		go func() {
			defer wg.Done()
			store.Update("term-1", func(w *Job) bool { w.LastAssistantInputAt = time.Now().UTC(); return true })
		}()
	}
	wg.Wait()

	job, ok := store.Get("term-1")
	if !ok {
		t.Fatal("the job is gone")
	}
	if job.Wakes != rounds {
		t.Fatalf("want %d counted checks, got %d: a write was lost", rounds, job.Wakes)
	}
	if job.LastAssistantInputAt.IsZero() {
		t.Fatal("the recorded input was lost")
	}
}

// One entry per terminal ever steered grows forever, and the file is parsed
// whole on every read, so the jobs that are over are bounded by count. The
// oldest of them go, and the newest survive whatever the file's order is.
func TestTheStoreDropsTheOldestClosedJobs(t *testing.T) {
	store := &JobStore{path: t.TempDir() + "/jobs.json"}
	start := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	for i := 0; i < maxClosedJobs+20; i++ {
		store.Save(Job{
			Terminal:  fmt.Sprintf("term-%03d", i),
			State:     JobDone,
			CreatedAt: start.Add(time.Duration(i) * time.Minute),
		})
	}

	list := store.List()
	if len(list) != maxClosedJobs {
		t.Fatalf("want the closed jobs capped at %d, got %d", maxClosedJobs, len(list))
	}
	// The 20 oldest went, the newest one is still there, and the cut is by age
	// and not by where an entry happens to sit in the file.
	if _, ok := store.Get("term-000"); ok {
		t.Fatal("the oldest closed job survived the cap")
	}
	if _, ok := store.Get("term-019"); ok {
		t.Fatal("a job past the cap survived it")
	}
	for _, want := range []string{"term-020", fmt.Sprintf("term-%03d", maxClosedJobs+19)} {
		if _, ok := store.Get(want); !ok {
			t.Fatalf("%s was dropped, but it is within the cap", want)
		}
	}
}

// The cap counts only the jobs that are over. An open job still wakes the
// assistant and holds its terminal, so no number of closed ones may push it out.
func TestTheCapLeavesOpenJobsAlone(t *testing.T) {
	store := &JobStore{path: t.TempDir() + "/jobs.json"}
	start := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	store.Save(Job{Terminal: "open-1", State: JobSteering, CreatedAt: start})
	for i := 0; i < maxClosedJobs+20; i++ {
		store.Save(Job{
			Terminal:  fmt.Sprintf("term-%03d", i),
			State:     JobDone,
			CreatedAt: start.Add(time.Duration(i+1) * time.Minute),
		})
	}

	if _, ok := store.Get("open-1"); !ok {
		t.Fatal("the open job was dropped by the cap on the closed ones")
	}
	if got := len(store.List()); got != maxClosedJobs+1 {
		t.Fatalf("want %d entries, got %d", maxClosedJobs+1, got)
	}
}

// A session that is gone leaves an entry nothing can resolve, and every read of
// the store pays for parsing it. The startup pass drops those, and only those:
// an open job stays, because ending one is the heartbeat's, with a report the
// user gets.
func TestPruneTerminalsDropsWhatCannotBeResolvedAnyMore(t *testing.T) {
	store := &JobStore{path: t.TempDir() + "/jobs.json"}
	store.Save(Job{Terminal: "alive", State: JobDone})
	store.Save(Job{Terminal: "gone", State: JobDone})
	store.Save(Job{Terminal: "gone-but-open", State: JobSteering})

	if removed := store.PruneTerminals(map[string]bool{"alive": true}); removed != 1 {
		t.Fatalf("want one entry removed, got %d", removed)
	}
	if _, ok := store.Get("gone"); ok {
		t.Fatal("the job of a terminal that is gone survived the prune")
	}
	if _, ok := store.Get("alive"); !ok {
		t.Fatal("the job of a live terminal was pruned")
	}
	if _, ok := store.Get("gone-but-open"); !ok {
		t.Fatal("an open job was pruned, so its ending would never reach the user")
	}
	if removed := store.PruneTerminals(map[string]bool{"alive": true}); removed != 0 {
		t.Fatalf("a second pass has nothing left to remove, got %d", removed)
	}
}

// Deleting a coder deletes its job. Release is for a terminal that stays: the
// entry keeps standing so the user sees what became of it. A deleted session
// has nothing to stand next to.
func TestForgetRemovesTheJobOfADeletedCoder(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	f.steered(t)

	f.watcher.Forget("term-1")
	if _, ok := f.jobs.Get("term-1"); ok {
		t.Fatal("the job of a deleted coder is still in the store")
	}
	if jobs := f.watcher.List(); len(jobs) != 0 {
		t.Fatalf("want no jobs left, got %+v", jobs)
	}
	// A terminal nobody steers is the normal case here and answers with nothing.
	f.watcher.Forget("term-1")
}

// The store keys on the terminal, so steering one again renews its job instead
// of adding a second one. A coder has one job, and the newest criterion is the
// one that decides it.
func TestSteeringATerminalAgainRenewsItsJob(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	f.steered(t)

	renewed, err := f.watcher.Steer(Job{Terminal: "term-1", CoderID: "claude", DoneWhen: "something else"})
	if err != nil {
		t.Fatalf("steer again: %v", err)
	}
	if renewed.DoneWhen != "something else" {
		t.Fatalf("want the new criterion, got %q", renewed.DoneWhen)
	}
	jobs := f.watcher.List()
	if len(jobs) != 1 || jobs[0].DoneWhen != "something else" {
		t.Fatalf("want one job per terminal, got %+v", jobs)
	}
}

// A check is told to call the cockpit's own commands the way a turn has to call
// them: the binary that is running and the directories of this instance. A bare
// name depends on a PATH the turn does not control, and on a machine with
// several instances it would read the endpoint of the wrong one.
func TestTheWakePromptNamesThisInstance(t *testing.T) {
	dir := t.TempDir()
	svc, workspace, err := New(dir, fakeCoders{runner: &fakeRunner{dir: t.TempDir()}}, Cockpit{
		Executable:  "/opt/dev-cockpit/dev-cockpit",
		StateDir:    "/var/lib/dc",
		ProjectsDir: "/srv/projects",
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	want := "/opt/dev-cockpit/dev-cockpit assistant --state-dir /var/lib/dc --projects-dir /srv/projects terminal-screen"
	if got := workspace.CockpitCommand("terminal-screen"); got != want {
		t.Fatalf("unexpected command %q", got)
	}

	watcher := NewWatcher(svc, &JobStore{path: dir + "/jobs.json"}, &fakeSessions{})
	prompt := watcher.wakePrompt(Job{Terminal: "term-1", DoneWhen: "x"}, Activity{Text: "something"})
	for _, want := range []string{
		"/opt/dev-cockpit/dev-cockpit assistant --state-dir /var/lib/dc --projects-dir /srv/projects terminal-screen term-1",
		"/opt/dev-cockpit/dev-cockpit assistant --state-dir /var/lib/dc --projects-dir /srv/projects coder-send-prompt term-1",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt misses %q:\n%s", want, prompt)
		}
	}
}

// The assistant may write into its own workspace, so it may put a link there
// that points anywhere. What counts is where a path really lands, not how it is
// spelled.
func TestAWorkspaceLinkOutOfTheWorkspaceIsRefused(t *testing.T) {
	dir := t.TempDir()
	_, workspace, err := New(dir, fakeCoders{}, Cockpit{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace.Workspace(), "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// A real file next to it still resolves, so the check is about the target
	// and not about refusing everything.
	if err := os.WriteFile(filepath.Join(workspace.Workspace(), "own.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := workspace.ResolveWorkspaceFile("link.txt"); err == nil {
		t.Fatal("a link pointing out of the workspace was served")
	}
	if _, err := workspace.ResolveWorkspaceFile("own.txt"); err != nil {
		t.Fatalf("a file of its own must still resolve: %v", err)
	}
}

// A job from the page may come without a criterion. It is stored empty, the
// prompt tells the check to judge against the session's own task, and the
// assistant's report lines say so instead of showing an empty criterion.
func TestAJobWithoutADoneWhenJudgesAgainstTheSessionsOwnTask(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	job, err := f.watcher.Steer(Job{Terminal: "term-9", CoderID: "claude"})
	if err != nil {
		t.Fatalf("a job without a criterion has to be allowed: %v", err)
	}
	if job.DoneWhen != "" {
		t.Fatalf("want an empty criterion stored, got %q", job.DoneWhen)
	}

	prompt := wakePrompt(job, Activity{Text: "the coder stopped", Finished: true})
	for _, want := range []string{
		"nothing was written down for this job",
		"task this session was given is complete",
		"name in your report what you checked",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt has to say %q:\n%s", want, prompt)
		}
	}
	if line := DoneWhenLine(job.DoneWhen); line != "what the session's own task says" {
		t.Fatalf("the reports would show %q for a job without a criterion", line)
	}
	if line := DoneWhenLine("the tests pass"); line != "the tests pass" {
		t.Fatalf("a stored criterion has to stand as it is, got %q", line)
	}
}

// The prompt is where ownership is spelled out: a steered terminal is the
// assistant's to keep moving, so the check is offered the ways to write.
func TestTheWakePromptOffersTheWaysToWrite(t *testing.T) {
	job := Job{Terminal: "term-1", DoneWhen: "the tests pass"}
	activity := Activity{Text: "the coder stopped", Finished: true}
	prompt := wakePromptWith(job, activity, "dc output", "dc send", "dc keys")
	for _, want := range []string{
		"yours to keep moving", "dc send term-1", "dc keys term-1", "dc output term-1",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt has to say %q:\n%s", want, prompt)
		}
	}
}

// The same job is written from two sides while a check runs: the check writes
// what it found, the input route writes the assistant's send. Both go through
// the store's read modify write, so neither can drop the other's field. Reading
// the record, changing a copy and saving it back loses one of the two.
func TestAWakeAndAnInputAtTheSameTimeKeepBothFields(t *testing.T) {
	f := newJobFixture(t, "NOTHING")
	f.steered(t)

	const rounds = 100
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			f.watcher.mark("term-1", func(w *Job) { w.Wakes++ })
		}()
		go func() {
			defer wg.Done()
			f.watcher.NoteAssistantInput("term-1")
		}()
	}
	wg.Wait()

	job, ok := f.jobs.Get("term-1")
	if !ok {
		t.Fatal("the job is gone")
	}
	if job.Wakes != rounds {
		t.Fatalf("want %d counted checks, got %d: a check's write was lost", rounds, job.Wakes)
	}
	if job.LastAssistantInputAt.IsZero() {
		t.Fatal("the recorded input was lost")
	}
}
