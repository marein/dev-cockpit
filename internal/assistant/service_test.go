package assistant

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/local/dev-cockpit/internal/statefile"
)

// fakeRunner scripts one provider. A turn is a real detached process writing
// lines into a file, so the fake is a real process too: the events a test asks
// for are written into a data file and a small shell script plays them back one
// line at a time. Nothing about the mechanism is faked away, only the coder is.
type fakeRunner struct {
	mu     sync.Mutex
	events []Event
	// answer wins over events when set, so a test can answer a check
	// differently from a chat turn.
	answer func(TurnRequest) []Event
	// block holds the process open until the test closes it, the way a coder
	// that is still working does. hold picks the channel per turn, for a test
	// that keeps one turn open while another runs to its end.
	block chan struct{}
	hold  func(TurnRequest) chan struct{}
	// after are the events the process writes once block is released, so a test
	// can put half an answer before a restart and half after it.
	after []Event
	// unfinished leaves out the record that closes a turn, which is what a
	// provider that died halfway through looks like.
	unfinished bool
	// stderr is what the process writes to standard error, which is where a CLI
	// that never got going says why.
	stderr     string
	exists     bool
	deleted    []string
	deleteFn   func(string) error
	requests   []TurnRequest
	commandErr error
	// dir holds the scripts and the files a turn is played back from, set by
	// newTestService, and over is closed when the test ends so nothing writes
	// into a directory that is already being removed. done says the same under
	// the lock, for a release that was already on its way when the test ended:
	// a test whose last statement closes block races the removal of dir
	// otherwise, and the release lands in a directory being deleted.
	dir  string
	over <-chan struct{}
	done bool
	seq  int
}

// end stops the releases of this runner and waits out one that is already
// writing, so nothing touches dir after the test's own cleanup returns.
func (r *fakeRunner) end(over chan struct{}) {
	close(over)
	r.mu.Lock()
	r.done = true
	r.mu.Unlock()
}

// encodeEvent writes one event as a line the fake parser reads back. The
// payload is encoded, so a delta carrying newlines or quotes travels through a
// file and a shell without a second meaning.
func encodeEvent(ev Event) string {
	switch ev.Kind {
	case EventTool:
		return "T" + base64.StdEncoding.EncodeToString([]byte(ev.Text))
	case EventError:
		text := ""
		if ev.Err != nil {
			text = ev.Err.Error()
		}
		return "E" + base64.StdEncoding.EncodeToString([]byte(text))
	default:
		return "D" + base64.StdEncoding.EncodeToString([]byte(ev.Text))
	}
}

func (r *fakeRunner) Command(req TurnRequest) (Command, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	events := r.events
	answer := r.answer
	block := r.block
	hold := r.hold
	after := r.after
	unfinished := r.unfinished
	stderr := r.stderr
	commandErr := r.commandErr
	r.seq++
	seq := r.seq
	dir := r.dir
	r.mu.Unlock()
	if commandErr != nil {
		return Command{}, commandErr
	}
	if answer != nil {
		events = answer(req)
	}
	if hold != nil {
		block = hold(req)
	}

	var lines strings.Builder
	for _, ev := range events {
		lines.WriteString(encodeEvent(ev))
		lines.WriteString("\n")
	}
	data := filepath.Join(dir, fmt.Sprintf("turn-%d.lines", seq))
	if err := os.WriteFile(data, []byte(lines.String()), 0o600); err != nil {
		return Command{}, err
	}

	var script strings.Builder
	if stderr != "" {
		// Through a file, so a complaint spanning several lines travels without
		// a second meaning in the shell.
		text := filepath.Join(dir, fmt.Sprintf("turn-%d.stderr", seq))
		if err := os.WriteFile(text, []byte(stderr), 0o600); err != nil {
			return Command{}, err
		}
		fmt.Fprintf(&script, "cat %s >&2\n", text)
	}
	fmt.Fprintf(&script, "while IFS= read -r line; do printf '%%s\n' \"$line\"; done < %s\n", data)
	if block != nil {
		release := filepath.Join(dir, fmt.Sprintf("release-%d", seq))
		over := r.over
		go func() {
			select {
			case <-block:
				r.mu.Lock()
				if !r.done {
					_ = os.WriteFile(release, []byte("go"), 0o600)
				}
				r.mu.Unlock()
			case <-over:
			}
		}()
		fmt.Fprintf(&script, "while [ ! -f %s ]; do sleep 0.02; done\n", release)
	}
	if len(after) > 0 {
		var rest strings.Builder
		for _, ev := range after {
			rest.WriteString(encodeEvent(ev))
			rest.WriteString("\n")
		}
		tail := filepath.Join(dir, fmt.Sprintf("turn-%d.after", seq))
		if err := os.WriteFile(tail, []byte(rest.String()), 0o600); err != nil {
			return Command{}, err
		}
		fmt.Fprintf(&script, "while IFS= read -r line; do printf '%%s\n' \"$line\"; done < %s\n", tail)
	}
	if !unfinished {
		script.WriteString("printf 'R\\n'\n")
	}
	return Command{Name: "/bin/sh", Args: []string{"-c", script.String()}}, nil
}

func (r *fakeRunner) Parse(sessionID string, events chan<- Event) Parser {
	return &fakeParser{events: events}
}

// fakeParser reads back what the fake wrote, in the same shape a real coder's
// parser does: one record per line, and a closing record without which the turn
// counts as broken off.
type fakeParser struct {
	events    chan<- Event
	sawResult bool
}

func (p *fakeParser) Line(line []byte) error {
	if len(line) == 0 {
		return nil
	}
	if line[0] == 'R' {
		p.sawResult = true
		return nil
	}
	payload, err := base64.StdEncoding.DecodeString(string(line[1:]))
	if err != nil {
		return errors.New("The coder sent an answer this version cannot read.")
	}
	switch line[0] {
	case 'T':
		p.events <- Event{Kind: EventTool, Text: string(payload)}
	case 'E':
		p.events <- Event{Kind: EventError, Err: errors.New(string(payload))}
	default:
		p.events <- Event{Kind: EventDelta, Text: string(payload)}
	}
	return nil
}

func (p *fakeParser) Finish() error {
	if !p.sawResult {
		return errors.New("The coder stopped before it finished the answer.")
	}
	return nil
}

// Diagnose takes the general path, the one a coder whose CLI only speaks on
// standard error takes.
func (p *fakeParser) Diagnose(err error, stderr string) error {
	if LooksLikeLogin(stderr) {
		return ErrNotLoggedIn
	}
	return nil
}

func (r *fakeRunner) SessionExists(string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exists
}

func (r *fakeRunner) DeleteSession(id string) error {
	r.mu.Lock()
	r.deleted = append(r.deleted, id)
	fn := r.deleteFn
	r.mu.Unlock()
	if fn != nil {
		return fn(id)
	}
	return nil
}

func (r *fakeRunner) turns() []TurnRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TurnRequest(nil), r.requests...)
}

type fakeCoders struct{ runner *fakeRunner }

func (c fakeCoders) Available() []CoderInfo {
	if c.runner == nil {
		return nil
	}
	return []CoderInfo{{ID: "claude", Label: "Claude", Runner: c.runner}}
}

// fakeProjects resolves every project onto a real directory, because a turn is
// a real process now and a process needs somewhere to run.
type fakeProjects struct {
	missing bool
	root    string
}

func (p *fakeProjects) ValidatePath(raw string) (string, error) {
	if p.missing {
		return "", errors.New("gone")
	}
	dir := filepath.Join(p.root, filepath.Base(raw))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func (p *fakeProjects) ProjectNameFor(path string) string { return filepath.Base(path) }

type fakeTerminals struct {
	id     string
	err    error
	stops  []string
	stopFn func() error
}

func (t *fakeTerminals) ResumeReserved(coderID, sessionID, projectPath, title string) (string, error) {
	if t.err != nil {
		return "", t.err
	}
	return t.id, nil
}

func (t *fakeTerminals) Stop(coderID, terminalID string) error {
	t.stops = append(t.stops, terminalID)
	if t.stopFn != nil {
		return t.stopFn()
	}
	return nil
}

func newTestService(t *testing.T, runner *fakeRunner) (*Service, *Store, *fakeProjects) {
	t.Helper()
	svc, store, projects, _ := newTestServiceIn(t, t.TempDir(), runner)
	return svc, store, projects
}

// newTestServiceIn builds a service over a state directory a test names itself,
// so a restart can be played: the second service reads the same store and the
// same run register, and picks up whatever the first one left running.
func newTestServiceIn(t *testing.T, dir string, runner *fakeRunner) (*Service, *Store, *fakeProjects, *RunStore) {
	t.Helper()
	if runner != nil && runner.dir == "" {
		runner.dir = t.TempDir()
		over := make(chan struct{})
		t.Cleanup(func() { runner.end(over) })
		runner.over = over
	}
	store := NewStore(dir)
	runs := NewRunStore(dir)
	projects := &fakeProjects{root: filepath.Join(dir, "projects")}
	svc := newService(store, runs, fakeCoders{runner: runner}, projects)
	t.Cleanup(func() { quiesce(t, svc, nil) })
	return svc, store, projects, runs
}

// quiesce ends what a test left running and waits until nothing writes any
// more. A turn is a real process, and the goroutine that follows it writes the
// transcript, the job and the register when it ends, while t.TempDir removes
// the state directory the moment the test returns. Both are the same race, and
// the loser is either a state file written into a directory that is being
// deleted or the removal itself, which fails on a directory somebody filled
// again. So a test owns its turns to their end: registered before the
// directories it works in, it runs before they are removed.
func quiesce(t *testing.T, svc *Service, w *Watcher) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		svc.mu.Lock()
		open := make([]*activeRun, 0, len(svc.running))
		for _, a := range svc.running {
			open = append(open, a)
		}
		svc.mu.Unlock()
		checking := 0
		if w != nil {
			w.mu.Lock()
			checking = len(w.running)
			w.mu.Unlock()
		}
		if len(open) == 0 && checking == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("the test left %d turn(s) and %d check(s) running", len(open), checking)
			return
		}
		// A turn a test left open never ends on its own: the fake waits for a
		// release that is not coming. Ending it is what lets its goroutine
		// write the last of it while the directories are still there.
		for _, a := range open {
			a.cancelled.Store(true)
			a.proc.kill()
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitFor polls until cond holds, so no test sleeps a fixed amount waiting for
// an asynchronous generation.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func lastMessage(t *testing.T, svc *Service, id string) Message {
	t.Helper()
	c, err := svc.Get(id)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	m, ok := c.Last()
	if !ok {
		t.Fatal("conversation has no messages")
	}
	return m
}

func waitIdle(t *testing.T, svc *Service, id string) Message {
	t.Helper()
	waitFor(t, "the turn to settle", func() bool {
		return !svc.Running(id) && lastMessage(t, svc, id).State.Settled()
	})
	return lastMessage(t, svc, id)
}

type frameCollector struct {
	mu     sync.Mutex
	frames []StreamEvent
	done   chan struct{}
	cancel func()
}

func collectFrames(svc *Service, id string) *frameCollector {
	_, _, ch, cancel := svc.Subscribe(id)
	c := &frameCollector{done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(c.done)
		for ev := range ch {
			c.mu.Lock()
			c.frames = append(c.frames, ev)
			c.mu.Unlock()
		}
	}()
	return c
}

func (c *frameCollector) has(kind string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.frames {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

func (c *frameCollector) stop() []StreamEvent {
	c.cancel()
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]StreamEvent(nil), c.frames...)
}

func TestSendStreamsAndCompletes(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "Hel"}, {Kind: EventTool}, {Kind: EventDelta, Text: "lo"}}}
	svc, _, _ := newTestService(t, runner)

	created, err := svc.create("claude", "/projects/demo")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Title != DefaultTitle {
		t.Fatalf("want the default title, got %q", created.Title)
	}

	frames := collectFrames(svc, created.ID)
	if _, err := svc.Send(created.ID, "  What is up?  ", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	final := waitIdle(t, svc, created.ID)
	collected := frames.stop()

	if final.State != StateComplete || final.Content != "Hello" {
		t.Fatalf("want a complete Hello, got %s %q", final.State, final.Content)
	}
	conversation, _ := svc.Get(created.ID)
	if conversation.Title != "What is up?" {
		t.Fatalf("want the title derived from the first prompt, got %q", conversation.Title)
	}
	if len(conversation.Messages) != 2 || conversation.Messages[0].Role != RoleUser || conversation.Messages[0].Content != "What is up?" {
		t.Fatalf("unexpected transcript: %+v", conversation.Messages)
	}
	if got := svc.List(); len(got) != 1 || got[0].Preview != "Hello" || got[0].Unfinished {
		t.Fatalf("unexpected index entry: %+v", got)
	}

	var kinds []string
	for _, f := range collected {
		kinds = append(kinds, f.Kind)
	}
	if strings.Join(kinds, ",") != "message,start,delta,tool,delta,end" {
		t.Fatalf("unexpected frames: %v", kinds)
	}

	turns := runner.turns()
	if len(turns) != 1 || turns[0].Resume || turns[0].SessionID != created.ID || filepath.Base(turns[0].Workdir) != "demo" {
		t.Fatalf("unexpected first turn: %+v", turns)
	}
}

// A message is written on one device and read on all of them. It goes out on
// the conversation's stream before the answer opens, so a panel that stands
// open somewhere else pulls the question and puts the answer under it, instead
// of showing an answer to something nobody there can see.
func TestASentMessageIsAnnouncedBeforeItsAnswer(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	frames := collectFrames(svc, created.ID)
	run, err := svc.Send(created.ID, "vom Telefon", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, created.ID)
	collected := frames.stop()

	conversation, _ := svc.Get(created.ID)
	user := conversation.Messages[0]
	if user.Role != RoleUser {
		t.Fatalf("want the prompt first in the transcript, got %+v", conversation.Messages)
	}
	if run.UserMessageID != user.ID {
		t.Fatalf("want the run to name the sent message %q, got %q", user.ID, run.UserMessageID)
	}
	if len(collected) < 2 {
		t.Fatalf("want the message announced and the answer opened, got %+v", collected)
	}
	if collected[0].Kind != FrameMessage || collected[0].MessageID != user.ID {
		t.Fatalf("want the sent message announced first, got %+v", collected[0])
	}
	if collected[1].Kind != FrameStart || collected[1].MessageID == user.ID {
		t.Fatalf("want the answer to open after the message, got %+v", collected[1])
	}
}

func TestSecondTurnResumesTheProviderSession(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	if _, err := svc.Send(created.ID, "first", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, created.ID)

	runner.mu.Lock()
	runner.exists = true
	runner.mu.Unlock()

	if _, err := svc.Send(created.ID, "second", nil); err != nil {
		t.Fatalf("send again: %v", err)
	}
	waitIdle(t, svc, created.ID)

	turns := runner.turns()
	if len(turns) != 2 || turns[1].Resume != true || turns[1].SessionID != created.ID {
		t.Fatalf("want the second turn to resume the same session, got %+v", turns)
	}
	if turns[0].Title != DefaultTitle && turns[0].Title == "" {
		t.Fatalf("want the first turn to carry a session title, got %q", turns[0].Title)
	}
}

func TestSendWhileRunningQueuesAndFlushesAsOneTurn(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}, block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	frames := collectFrames(svc, created.ID)
	defer frames.stop()
	if _, err := svc.Send(created.ID, "first", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	second, err := svc.Send(created.ID, "second", nil)
	if err != nil || !second.Queued || second.MessageID == "" {
		t.Fatalf("want the second message queued, got %+v, %v", second, err)
	}
	third, err := svc.Send(created.ID, "third", nil)
	if err != nil || !third.Queued {
		t.Fatalf("want the third message queued, got %+v, %v", third, err)
	}
	conversation, _ := svc.Get(created.ID)
	waiting := queuedMessages(conversation)
	if len(waiting) != 2 || waiting[0].Content != "second" || waiting[1].Content != "third" {
		t.Fatalf("want both messages waiting in order, got %+v", waiting)
	}
	waitFor(t, "the waiting entries to reach the stream", func() bool { return frames.has(FrameMessage) })

	close(runner.block)
	waitFor(t, "the flush turn to start", func() bool { return len(runner.turns()) == 2 })
	waitIdle(t, svc, created.ID)

	turns := runner.turns()
	first := strings.Index(turns[1].Prompt, "--- Message 1 ---\nsecond")
	next := strings.Index(turns[1].Prompt, "--- Message 2 ---\nthird")
	if first < 0 || next < 0 || next < first {
		t.Fatalf("want one flush turn carrying both messages in order, got %q", turns[1].Prompt)
	}
	conversation, _ = svc.Get(created.ID)
	if len(queuedMessages(conversation)) != 0 {
		t.Fatalf("want no message left waiting, got %+v", conversation.Messages)
	}
	last, _ := conversation.Last()
	if last.Role != RoleAssistant || last.State != StateComplete || last.Content != "ok" {
		t.Fatalf("want the flush turn answered, got %+v", last)
	}
}

func TestAQueuedMessageCanBeDiscardedWhileItWaits(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}, block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	frames := collectFrames(svc, created.ID)
	defer frames.stop()
	if _, err := svc.Send(created.ID, "first", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	queued, err := svc.Send(created.ID, "never mind", nil)
	if err != nil || !queued.Queued {
		t.Fatalf("want the message queued, got %+v, %v", queued, err)
	}
	if err := svc.Discard(created.ID, queued.MessageID); err != nil {
		t.Fatalf("discard: %v", err)
	}
	waitFor(t, "the removal to reach the stream", func() bool { return frames.has(FrameGone) })
	// A message that already went out is answered, not silently dropped.
	conversation, _ := svc.Get(created.ID)
	if err := svc.Discard(created.ID, conversation.Messages[0].ID); err == nil {
		t.Fatal("want a discard of a sent message to be refused")
	}

	close(runner.block)
	waitIdle(t, svc, created.ID)
	if turns := runner.turns(); len(turns) != 1 {
		t.Fatalf("want no flush turn after the discard, got %d turns", len(turns))
	}
	conversation, _ = svc.Get(created.ID)
	if len(conversation.Messages) != 2 {
		t.Fatalf("want the discarded message gone from the transcript, got %+v", conversation.Messages)
	}
}

func TestStoppingATurnFlushesTheQueueRightAway(t *testing.T) {
	block := make(chan struct{})
	runner := &fakeRunner{
		events: []Event{{Kind: EventDelta, Text: "partial"}},
		hold: func(req TurnRequest) chan struct{} {
			if strings.Contains(req.Prompt, "first") {
				return block
			}
			return nil
		},
	}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	frames := collectFrames(svc, created.ID)
	defer frames.stop()
	if _, err := svc.Send(created.ID, "first", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, "the first delta", func() bool { return frames.has(FrameDelta) })
	if _, err := svc.Send(created.ID, "second", nil); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if err := svc.Cancel(created.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitFor(t, "the flush turn to start", func() bool { return len(runner.turns()) == 2 })
	waitIdle(t, svc, created.ID)

	turns := runner.turns()
	if turns[1].Prompt != "second" {
		t.Fatalf("want the queued message flushed on its own, got %q", turns[1].Prompt)
	}
	conversation, _ := svc.Get(created.ID)
	var stopped, answered bool
	for _, m := range conversation.Messages {
		if m.Role != RoleAssistant {
			continue
		}
		if m.State == StateCancelled {
			stopped = true
		}
		if m.State == StateComplete {
			answered = true
		}
	}
	if !stopped || !answered {
		t.Fatalf("want a stopped answer and a flushed one, got %+v", conversation.Messages)
	}
}

func TestAQueuedMessageSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	first := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}}
	svc1, store, _, _ := newTestServiceIn(t, dir, first)
	created, err := svc1.create("claude", "/projects/demo")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The server died while a message waited: the entry sits in the transcript
	// with nothing running, the state the next process has to pick up.
	c, _ := store.Load(created.ID)
	c.Messages = append(c.Messages, Message{
		ID:        statefile.NewID(),
		Role:      RoleUser,
		Content:   "queued while the server was down",
		CreatedAt: time.Now().UTC(),
		State:     StateQueued,
	})
	store.Save(c)

	second := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "after the restart"}}}
	svc2, _, _, _ := newTestServiceIn(t, dir, second)
	svc2.Recover()

	waitIdle(t, svc2, created.ID)
	turns := second.turns()
	if len(turns) != 1 || turns[0].Prompt != "queued while the server was down" {
		t.Fatalf("want the waiting message flushed after the restart, got %+v", turns)
	}
	conversation, _ := svc2.Get(created.ID)
	if len(queuedMessages(conversation)) != 0 {
		t.Fatalf("want nothing left waiting, got %+v", conversation.Messages)
	}
	last, _ := conversation.Last()
	if last.State != StateComplete || last.Content != "after the restart" {
		t.Fatalf("want the flushed turn answered, got %+v", last)
	}
}

func TestTransferIsRefusedWhileMessagesWait(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}}
	svc, store, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")
	if _, err := svc.Send(created.ID, "first", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, created.ID)

	c, _ := store.Load(created.ID)
	c.Messages = append(c.Messages, Message{
		ID:        statefile.NewID(),
		Role:      RoleUser,
		Content:   "still waiting",
		CreatedAt: time.Now().UTC(),
		State:     StateQueued,
	})
	store.Save(c)
	runner.mu.Lock()
	runner.exists = true
	runner.mu.Unlock()

	if _, err := svc.Transfer(created.ID, &fakeTerminals{id: "t1"}); err == nil {
		t.Fatal("want the transfer refused while a message waits")
	}
}

func TestGlobalGenerationLimit(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)

	var ids []string
	for i := 0; i < maxConcurrentRuns+1; i++ {
		created, err := svc.create("claude", "/projects/demo")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		ids = append(ids, created.ID)
	}
	// Creating a conversation archives the one before it, and an archived one
	// takes no message. The slot limit belongs to the substrate, so the test
	// puts them all back to active to drive more than one generation at once.
	for _, id := range ids {
		c, ok := svc.store.Load(id)
		if !ok {
			t.Fatalf("load %s", id)
		}
		c.Status = StatusActive
		svc.store.Save(c)
	}
	for i := 0; i < maxConcurrentRuns; i++ {
		if _, err := svc.Send(ids[i], "hello", nil); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if _, err := svc.Send(ids[maxConcurrentRuns], "hello", nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("want a busy error past the limit, got %v", err)
	}

	close(runner.block)
	for i := 0; i < maxConcurrentRuns; i++ {
		waitIdle(t, svc, ids[i])
	}
	if _, err := svc.Send(ids[maxConcurrentRuns], "hello", nil); err != nil {
		t.Fatalf("want the slot released after the runs finished, got %v", err)
	}
	waitIdle(t, svc, ids[maxConcurrentRuns])
}

func TestCancelKeepsThePartialAnswer(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "partial"}}, block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	frames := collectFrames(svc, created.ID)
	defer frames.stop()
	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, "the first delta", func() bool { return frames.has(FrameDelta) })
	if err := svc.Cancel(created.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	final := waitIdle(t, svc, created.ID)
	if final.State != StateCancelled {
		t.Fatalf("want a cancelled turn, got %s", final.State)
	}
	if final.Content != "partial" {
		t.Fatalf("want the partial answer kept, got %q", final.Content)
	}
	if !final.State.Retryable() {
		t.Fatal("want a cancelled turn to be retryable")
	}
}

func TestFailedTurnCanBeRetried(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventError, Err: errors.New("The coder could not finish this answer.")}}}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	failed := waitIdle(t, svc, created.ID)
	if failed.State != StateFailed || failed.Error == "" {
		t.Fatalf("want a failed turn with a message, got %s %q", failed.State, failed.Error)
	}

	runner.mu.Lock()
	runner.events = []Event{{Kind: EventDelta, Text: "second try"}}
	runner.mu.Unlock()

	frames := collectFrames(svc, created.ID)
	defer frames.stop()
	if _, err := svc.Retry(created.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	// The page that pressed retry drops the failed bubble itself, the others
	// only hear it from the stream.
	waitFor(t, "the replaced answer to reach the stream", func() bool { return frames.has(FrameGone) })
	final := waitIdle(t, svc, created.ID)
	if final.State != StateComplete || final.Content != "second try" {
		t.Fatalf("want the retry to succeed, got %s %q", final.State, final.Content)
	}
	conversation, _ := svc.Get(created.ID)
	if len(conversation.Messages) != 2 {
		t.Fatalf("want the failed answer replaced, got %d messages", len(conversation.Messages))
	}
	turns := runner.turns()
	if len(turns) != 2 || turns[1].Prompt != "hello" {
		t.Fatalf("want the retry to resend the same prompt, got %+v", turns)
	}
}

// A CLI that never got going writes no record at all, so there is nothing in the
// output to read and everything it has to say sits on standard error. The parser
// is asked either way, which is what turns it into the one sentence the user can
// act on.
func TestAFailedTurnIsNamedFromStandardError(t *testing.T) {
	runner := &fakeRunner{unfinished: true, stderr: "Error: No authentication information found.\n\nTo authenticate, run the '/login' command.\n"}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	failed := waitIdle(t, svc, created.ID)
	if failed.State != StateFailed {
		t.Fatalf("want a failed turn, got %s", failed.State)
	}
	if failed.Error != ErrNotLoggedIn.Error() {
		t.Fatalf("want the login sentence, got %q", failed.Error)
	}
}

// Nothing on standard error and a parser that has nothing to add leaves the
// generic failure standing: guessing at a cause is worse than not naming one.
func TestAFailedTurnWithNothingToNameStaysGeneric(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "half"}}, unfinished: true}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	failed := waitIdle(t, svc, created.ID)
	if failed.State != StateFailed {
		t.Fatalf("want a failed turn, got %s", failed.State)
	}
	if failed.Error != "The coder stopped before it finished the answer." {
		t.Fatalf("want the generic sentence, got %q", failed.Error)
	}
}

func TestRetryIsRefusedForACompletedTurn(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "done"}}}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")
	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, created.ID)

	if _, err := svc.Retry(created.ID); err == nil {
		t.Fatal("want a completed turn to refuse a retry, a charged answer is never resent on its own")
	}
}

func TestOversizePromptIsRefused(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	created, _ := svc.create("claude", "/projects/demo")

	if _, err := svc.Send(created.ID, "   ", nil); err == nil {
		t.Fatal("want an empty prompt to be refused")
	}
	if _, err := svc.Send(created.ID, strings.Repeat("x", MaxPromptBytes+1), nil); err == nil {
		t.Fatal("want an oversize prompt to be refused")
	}
	conversation, _ := svc.Get(created.ID)
	if len(conversation.Messages) != 0 {
		t.Fatalf("want no message persisted, got %d", len(conversation.Messages))
	}
}

func TestOversizeAnswerFailsAndKeepsThePrefix(t *testing.T) {
	chunk := strings.Repeat("y", 600<<10)
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: chunk}, {Kind: EventDelta, Text: chunk}}}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	final := waitIdle(t, svc, created.ID)
	if final.State != StateFailed {
		t.Fatalf("want the oversize answer to fail, got %s", final.State)
	}
	if len(final.Content) != len(chunk) {
		t.Fatalf("want the safe prefix kept, got %d bytes", len(final.Content))
	}
}

// orphanTurn leaves behind exactly what a server that was killed mid answer
// leaves behind: a message that is still streaming, an entry in the run
// register, and a process that is still writing into its output file. Nothing
// follows it, which is the whole point.
func orphanTurn(t *testing.T, svc *Service, conversationID string, runner *fakeRunner) RunRecord {
	t.Helper()
	c, err := svc.Get(conversationID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	rec := RunRecord{
		ID:           statefile.NewID(),
		Kind:         RunChat,
		Conversation: c.ID,
		MessageID:    statefile.NewID(),
		CoderID:      "claude",
		SessionID:    c.NativeSessionID,
	}
	c.Messages = append(c.Messages, Message{
		ID:        rec.MessageID,
		Role:      RoleAssistant,
		CreatedAt: time.Now().UTC(),
		RunID:     rec.ID,
		State:     StateStreaming,
	})
	svc.store.Save(c)
	a, err := svc.launch(runner, TurnRequest{SessionID: c.NativeSessionID, Workdir: c.ProjectPath, Prompt: "hello"}, rec)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	return a.rec
}

// A started turn describes itself on disk. Everything the recovery works from
// comes out of this entry, so it is checked against what starting a turn the
// normal way writes.
func TestAStartedTurnIsInTheRegister(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{})}
	svc, _, _, runs := newTestServiceIn(t, t.TempDir(), runner)
	created, _ := svc.create("claude", "/projects/demo")
	run, err := svc.Send(created.ID, "hello", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	list := runs.List()
	if len(list) != 1 {
		t.Fatalf("want one registered turn, got %d", len(list))
	}
	rec := list[0]
	switch {
	case rec.ID != run.RunID || rec.MessageID != run.MessageID:
		t.Fatalf("want the entry to name the run and its message, got %+v", rec)
	case rec.Kind != RunChat || rec.Conversation != created.ID || rec.CoderID != "claude":
		t.Fatalf("want the entry to name conversation and coder, got %+v", rec)
	case rec.PID <= 0 || rec.Output == "" || rec.Errors == "":
		t.Fatalf("want the entry to name the process and its files, got %+v", rec)
	}
	if _, err := os.Stat(rec.Output); err != nil {
		t.Fatalf("want the raw output file to exist: %v", err)
	}

	close(runner.block)
	waitIdle(t, svc, created.ID)
	if len(runs.List()) != 0 {
		t.Fatal("want the entry gone once the turn ended")
	}
	if _, err := os.Stat(rec.Output); !os.IsNotExist(err) {
		t.Fatalf("want the raw output removed with the entry, got %v", err)
	}
}

// The whole point of the feature: the server is restarted in the middle of an
// answer, and the answer is written to its end anyway. What was there before
// the restart is kept, what comes after it is appended, and the message carries
// no interruption at all.
func TestATurnSurvivesARestartAndIsWrittenToItsEnd(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{
		events: []Event{{Kind: EventDelta, Text: "before "}},
		after:  []Event{{Kind: EventDelta, Text: "and after"}},
		block:  make(chan struct{}),
	}
	svc, _, _, runs := newTestServiceIn(t, dir, runner)
	created, _ := svc.create("claude", "/projects/demo")
	rec := orphanTurn(t, svc, created.ID, runner)

	if !processAlive(rec.PID, rec.Lock) {
		t.Fatal("want the turn to still be running")
	}

	// A fresh service over the same state directory is what a restart looks
	// like: nothing is left of the old one but the register and the files.
	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	frames := collectFrames(restarted, created.ID)
	if adopted := restarted.Recover(); len(adopted) != 0 {
		t.Fatalf("want no adopted check for a chat turn, got %d", len(adopted))
	}
	waitFor(t, "the restarted server to pick the turn up", func() bool {
		return restarted.Running(created.ID)
	})

	close(runner.block)
	final := waitIdle(t, restarted, created.ID)
	if final.State != StateComplete {
		t.Fatalf("want the answer completed after the restart, got %s (%q)", final.State, final.Error)
	}
	if final.Content != "before and after" {
		t.Fatalf("want the whole answer, got %q", final.Content)
	}
	if final.Error != "" {
		t.Fatalf("want no interruption marker, got %q", final.Error)
	}
	if entries := restarted.List(); len(entries) != 1 || entries[0].Unfinished {
		t.Fatalf("want the conversation to count as finished, got %+v", entries)
	}
	if len(runs.List()) != 0 {
		t.Fatal("want the register empty once the turn ended")
	}

	// The browser sees it without reloading: the restarted server opens the
	// answer again and streams what it reads, including the part that was
	// already in the file.
	got := frames.stop()
	var text strings.Builder
	kinds := map[string]int{}
	for _, f := range got {
		kinds[f.Kind]++
		if f.Kind == FrameDelta {
			text.WriteString(f.Text)
		}
	}
	if kinds[FrameStart] == 0 || kinds[FrameEnd] == 0 {
		t.Fatalf("want the stream to open and close the answer, got %+v", kinds)
	}
	if text.String() != "before and after" {
		t.Fatalf("want the whole answer on the stream, got %q", text.String())
	}
}

// A turn that finished while nobody was looking is a finished turn, not a lost
// one. The record that closes it is in the file, so the answer is complete.
func TestATurnThatEndedDuringTheRestartIsComplete(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "all of it"}}}
	svc, _, _, _ := newTestServiceIn(t, dir, runner)
	created, _ := svc.create("claude", "/projects/demo")
	rec := orphanTurn(t, svc, created.ID, runner)
	waitFor(t, "the turn to end on its own", func() bool { return !processAlive(rec.PID, rec.Lock) })

	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	restarted.Recover()
	final := waitIdle(t, restarted, created.ID)
	if final.State != StateComplete || final.Content != "all of it" {
		t.Fatalf("want the finished answer picked up, got %s %q", final.State, final.Content)
	}
}

// A turn whose process really died is the one case that becomes an interrupted
// answer: the part that was written is kept and the turn can be sent again.
func TestATurnWhoseProcessDiedBecomesInterrupted(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "half"}}, unfinished: true}
	svc, _, _, _ := newTestServiceIn(t, dir, runner)
	created, _ := svc.create("claude", "/projects/demo")
	rec := orphanTurn(t, svc, created.ID, runner)
	waitFor(t, "the turn to die", func() bool { return !processAlive(rec.PID, rec.Lock) })

	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	restarted.Recover()
	final := waitIdle(t, restarted, created.ID)
	if final.State != StateInterrupted {
		t.Fatalf("want an interrupted turn, got %s", final.State)
	}
	if final.Content != "half" {
		t.Fatalf("want the part that arrived kept, got %q", final.Content)
	}
	if !final.State.Retryable() {
		t.Fatal("want an interrupted turn to be retryable")
	}
	if entries := restarted.List(); len(entries) != 1 || !entries[0].Unfinished {
		t.Fatalf("want the index to mark the conversation unfinished, got %+v", entries)
	}
}

// A streaming message that no register entry accounts for lost its process for
// good, and saying so is better than a bubble that spins forever.
func TestAStreamingMessageWithoutARunIsClosed(t *testing.T) {
	dir := t.TempDir()
	svc, store, _, _ := newTestServiceIn(t, dir, &fakeRunner{})
	created, _ := svc.create("claude", "/projects/demo")
	c, _ := svc.Get(created.ID)
	c.Messages = append(c.Messages, Message{ID: "m1", Role: RoleAssistant, RunID: "gone", State: StateStreaming, CreatedAt: time.Now().UTC()})
	store.Save(c)

	restarted, _, _, _ := newTestServiceIn(t, dir, &fakeRunner{})
	restarted.Recover()
	final := lastMessage(t, restarted, created.ID)
	if final.State != StateInterrupted {
		t.Fatalf("want the orphaned message interrupted, got %s", final.State)
	}
}

// The limit on running turns is counted from the register after a restart, not
// from a number that died with the previous process.
func TestTheLimitIsCountedAgainAfterARestart(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{block: make(chan struct{})}
	svc, _, _, _ := newTestServiceIn(t, dir, runner)
	var ids []string
	for i := 0; i < maxConcurrentRuns; i++ {
		created, _ := svc.create("claude", "/projects/demo")
		ids = append(ids, created.ID)
		orphanTurn(t, svc, created.ID, runner)
	}

	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	restarted.Recover()
	waitFor(t, "the restarted server to pick both turns up", func() bool {
		return restarted.Running(ids[len(ids)-1])
	})

	fresh, err := restarted.create("claude", "/projects/demo")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := restarted.Send(fresh.ID, "one more", nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("want the recovered turns to fill the limit, got %v", err)
	}

	close(runner.block)
	for _, id := range ids {
		waitIdle(t, restarted, id)
	}
	if _, err := restarted.Send(fresh.ID, "one more", nil); err != nil {
		t.Fatalf("want the limit free again once the recovered turns ended: %v", err)
	}
	waitIdle(t, restarted, fresh.ID)
}

// A stop is written down before the process is killed, so a restart that lands
// in between still reads it as a stop and not as a coder that fell over.
func TestAStoppedTurnStaysStoppedAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "partial"}}, unfinished: true}
	svc, _, _, runs := newTestServiceIn(t, dir, runner)
	created, _ := svc.create("claude", "/projects/demo")
	rec := orphanTurn(t, svc, created.ID, runner)
	rec.Cancelled = true
	runs.Save(rec)
	waitFor(t, "the turn to end", func() bool { return !processAlive(rec.PID, rec.Lock) })

	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	restarted.Recover()
	final := waitIdle(t, restarted, created.ID)
	if final.State != StateCancelled {
		t.Fatalf("want the stop to survive the restart, got %s", final.State)
	}
	if final.Content != "partial" {
		t.Fatalf("want the part before the stop kept, got %q", final.Content)
	}
}

// A turn whose output file was replaced under it is not read: those are not the
// words this prompt was answered with.
func TestATurnWithLostOutputIsNotReadBack(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "gone"}}}
	svc, _, _, runs := newTestServiceIn(t, dir, runner)
	created, _ := svc.create("claude", "/projects/demo")
	rec := orphanTurn(t, svc, created.ID, runner)
	waitFor(t, "the turn to end", func() bool { return !processAlive(rec.PID, rec.Lock) })
	rec.Processed = 1 << 20
	runs.Save(rec)

	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	restarted.Recover()
	final := waitIdle(t, restarted, created.ID)
	if final.State != StateInterrupted {
		t.Fatalf("want a truncated output to end as interrupted, got %s", final.State)
	}
	if final.Content != "" {
		t.Fatalf("want nothing read back, got %q", final.Content)
	}
	if len(runs.List()) != 0 {
		t.Fatal("want the entry gone")
	}
}

func TestReservationHidesTheSessionUntilTransfer(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "hi"}}, exists: false}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	if !svc.Reserved("claude", created.ID) {
		t.Fatal("want an active conversation to reserve its provider session")
	}
	if svc.Reserved("copilot", created.ID) {
		t.Fatal("want the reservation to be scoped to its coder")
	}

	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, created.ID)

	runner.mu.Lock()
	runner.exists = true
	runner.mu.Unlock()

	terminals := &fakeTerminals{id: created.ID}
	terminalID, err := svc.Transfer(created.ID, terminals)
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if terminalID != created.ID {
		t.Fatalf("want the terminal to carry the native session id, got %q", terminalID)
	}
	if svc.Reserved("claude", created.ID) {
		t.Fatal("want a transferred conversation to release its reservation")
	}

	conversation, _ := svc.Get(created.ID)
	if conversation.Status != StatusTransferred || conversation.TransferredSessionID != created.ID {
		t.Fatalf("unexpected transferred state: %+v", conversation.Summary)
	}
	if _, err := svc.Send(created.ID, "more", nil); err == nil {
		t.Fatal("want a transferred conversation to refuse new messages")
	}
	if _, err := svc.Transfer(created.ID, terminals); err == nil {
		t.Fatal("want a second transfer to be refused")
	}
}

func TestTransferIsRefusedWhileRunningOrUnfinished(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{}), exists: true}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")
	terminals := &fakeTerminals{id: created.ID}

	if _, err := svc.Transfer(created.ID, terminals); err == nil {
		t.Fatal("want a transfer without a conversation to be refused")
	}

	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := svc.Transfer(created.ID, terminals); err == nil {
		t.Fatal("want a transfer during a generation to be refused")
	}
	if err := svc.Cancel(created.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	close(runner.block)
	waitIdle(t, svc, created.ID)
	// A stopped answer is exactly when moving to a terminal is useful, so it
	// stays allowed: the terminal resumes whatever the provider stored.
	if _, err := svc.Transfer(created.ID, terminals); err != nil {
		t.Fatalf("want a transfer after a stopped answer to work, got %v", err)
	}
}

func TestFailedTransferKeepsTheChatActive(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "hi"}}, exists: true}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")
	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, created.ID)

	terminals := &fakeTerminals{err: errors.New("tmux said no")}
	if _, err := svc.Transfer(created.ID, terminals); err == nil {
		t.Fatal("want the transfer error to surface")
	}
	conversation, _ := svc.Get(created.ID)
	if conversation.Status != StatusActive {
		t.Fatalf("want the conversation to stay active, got %s", conversation.Status)
	}
	if !svc.Reserved("claude", created.ID) {
		t.Fatal("want the reservation to survive a failed transfer")
	}
}

// A crash between archiving and creating, or a state directory from before the
// rule existed, can hold more than one live conversation. Starting the service
// settles that: the newest stays, the rest become history.
func TestStartupKeepsOneLiveConversation(t *testing.T) {
	runner := &fakeRunner{exists: true}
	svc, store, _ := newTestService(t, runner)
	first, _ := svc.create("claude", "/projects/demo")
	second, _ := svc.create("claude", "/projects/demo")
	// Put the earlier one back the way a crash would have left it.
	c, _ := store.Load(first.ID)
	c.Status = StatusActive
	c.LastMessageAt = time.Now().Add(-time.Hour)
	store.Save(c)

	restarted := newService(store, svc.runs, svc.coders, svc.projects)

	back, err := restarted.Get(first.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if back.Status != StatusArchived {
		t.Fatalf("want the older conversation archived on startup, got %q", back.Status)
	}
	stayed, err := restarted.Get(second.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stayed.Status != StatusActive {
		t.Fatalf("want the newest conversation live, got %q", stayed.Status)
	}
	if !restarted.Reserved("claude", second.ID) {
		t.Fatal("want the live conversation reserved")
	}
	if restarted.Reserved("claude", first.ID) {
		t.Fatal("want the archived conversation released")
	}
}

// A new conversation ends the one before it: the transcript stays, the provider
// session behind it goes, because nothing can continue it any more.
func TestANewConversationArchivesTheOneBeforeIt(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "hi"}}, exists: true}
	svc, _, _ := newTestService(t, runner)
	first, _ := svc.create("claude", "/projects/demo")
	if _, err := svc.Send(first.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, first.ID)

	second, err := svc.create("claude", "/projects/demo")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	archived, err := svc.Get(first.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if archived.Status != StatusArchived {
		t.Fatalf("want the earlier conversation archived, got %q", archived.Status)
	}
	if len(archived.Messages) != 2 {
		t.Fatalf("want the transcript kept, got %d messages", len(archived.Messages))
	}
	if len(runner.deleted) != 1 || runner.deleted[0] != first.ID {
		t.Fatalf("want the provider session of the earlier conversation deleted, got %v", runner.deleted)
	}
	if svc.Reserved("claude", first.ID) {
		t.Fatal("want the reservation of the earlier conversation released")
	}
	if !svc.Reserved("claude", second.ID) {
		t.Fatal("want the new conversation reserved")
	}
	if _, err := svc.Send(first.ID, "again", nil); err == nil {
		t.Fatal("want an archived conversation to refuse a message")
	}
}

// Everything that acts asks for the live conversation, so opening one has to be
// the same conversation every time: a check that reports and a page that renders
// must not each leave a fresh empty one behind.
func TestOpenReturnsTheLiveConversation(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})

	first, err := svc.Open("")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	again, err := svc.Open("")
	if err != nil {
		t.Fatalf("open again: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("want the same conversation, got %s and %s", first.ID, again.ID)
	}
	if len(svc.List()) != 1 {
		t.Fatalf("want one conversation, got %d", len(svc.List()))
	}
	current, ok := svc.Current()
	if !ok || current.ID != first.ID {
		t.Fatalf("want Current to be the open one, got %+v (%v)", current, ok)
	}
}

// Asking for a coder is the new conversation button. An untouched one is that
// conversation already, a used one is left as the record it became.
func TestOpenWithACoderReusesAnUntouchedConversation(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "hi"}}}
	svc, _, _ := newTestService(t, runner)

	empty, _ := svc.Open("claude")
	same, _ := svc.Open("claude")
	if same.ID != empty.ID {
		t.Fatal("want an untouched conversation reused instead of a trail of empty ones")
	}

	if _, err := svc.Send(same.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, same.ID)
	fresh, err := svc.Open("claude")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if fresh.ID == same.ID {
		t.Fatal("want a conversation that was used left alone and a new one started")
	}
}

// The archive leaves a session alone while its turn is still running: killing it
// under the process would pull the provider's state out from under it. The turn
// ending is what drops it.
func TestArchiveDropsTheSessionAfterARunningTurn(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{}), exists: true}
	svc, _, _ := newTestService(t, runner)
	first, _ := svc.create("claude", "/projects/demo")
	if _, err := svc.Send(first.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}

	if _, err := svc.create("claude", "/projects/demo"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(runner.deleted) != 0 {
		t.Fatalf("want the session kept while the turn runs, got %v", runner.deleted)
	}

	close(runner.block)
	waitIdle(t, svc, first.ID)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		done := len(runner.deleted) == 1
		runner.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("want the session dropped after the turn, got %v", runner.deleted)
}

func TestDeleteRemovesTheProviderSession(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "hi"}}, exists: true}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	if err := svc.Delete(created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(runner.deleted) != 1 || runner.deleted[0] != created.ID {
		t.Fatalf("want the provider session deleted, got %v", runner.deleted)
	}
	if svc.Reserved("claude", created.ID) {
		t.Fatal("want the reservation released")
	}
	if _, err := svc.Get(created.ID); err == nil {
		t.Fatal("want the conversation gone")
	}
	if len(svc.List()) != 0 {
		t.Fatal("want the index entry gone")
	}
}

func TestDeleteKeepsTheChatWhenTheProviderRefuses(t *testing.T) {
	runner := &fakeRunner{exists: true, deleteFn: func(string) error { return errors.New("locked") }}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	if err := svc.Delete(created.ID); err == nil {
		t.Fatal("want the delete error to surface")
	}
	if _, err := svc.Get(created.ID); err != nil {
		t.Fatal("want the conversation kept, a hidden unreachable conversation is worse than a visible one")
	}
}

func TestDeleteStopsARunningGeneration(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{}), exists: true}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")
	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := svc.Delete(created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if svc.Running(created.ID) {
		t.Fatal("want the generation stopped before the delete returns")
	}
	close(runner.block)
}

func TestUnavailableCoderAndProject(t *testing.T) {
	svc, _, projects := newTestService(t, &fakeRunner{})
	if _, err := svc.create("nope", "/projects/demo"); err == nil {
		t.Fatal("want an unknown coder to be refused")
	}
	created, _ := svc.create("claude", "/projects/demo")

	projects.missing = true
	if _, err := svc.Send(created.ID, "hello", nil); err == nil {
		t.Fatal("want a moved project to block new messages")
	}
	if _, err := svc.Get(created.ID); err != nil {
		t.Fatal("want the transcript to stay readable")
	}
}

func TestRenameBoundsTheTitle(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	created, _ := svc.create("claude", "/projects/demo")

	if err := svc.Rename(created.ID, "   "); err == nil {
		t.Fatal("want an empty title to be refused")
	}
	long := strings.Repeat("ä", MaxTitleRunes+50)
	if err := svc.Rename(created.ID, long); err != nil {
		t.Fatalf("rename: %v", err)
	}
	conversation, _ := svc.Get(created.ID)
	if got := []rune(conversation.Title); len(got) > MaxTitleRunes+1 {
		t.Fatalf("want the title bounded, got %d runes", len(got))
	}
	if !strings.HasPrefix(conversation.Title, "ä") {
		t.Fatalf("want the title cut on a rune boundary, got %q", conversation.Title)
	}
}

func TestStreamSnapshotCarriesTheRunningAnswer(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "half"}}, block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")
	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, "the delta to arrive", func() bool {
		snapshot, running, _, cancel := svc.Subscribe(created.ID)
		cancel()
		return running && snapshot.Text == "half"
	})

	snapshot, running, _, cancel := svc.Subscribe(created.ID)
	defer cancel()
	if !running || snapshot.Kind != FrameStart || snapshot.Text != "half" {
		t.Fatalf("want a late subscriber to receive the running answer, got %+v", snapshot)
	}

	close(runner.block)
	waitIdle(t, svc, created.ID)

	_, stillRunning, _, cancel2 := svc.Subscribe(created.ID)
	defer cancel2()
	if stillRunning {
		t.Fatal("want no in-flight state after the turn ended")
	}
}

func TestTheAnswerIsRenderedWhileItStreams(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "# Title\n\nbody"}}, block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)
	svc.SetRenderer(func(src string) (string, error) { return "<rendered>" + src + "</rendered>", nil })
	created, _ := svc.create("claude", "/projects/demo")

	frames := collectFrames(svc, created.ID)
	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, "the rendered prefix", func() bool { return frames.has(FrameHTML) })

	var rendered string
	for _, f := range frames.stop() {
		if f.Kind == FrameHTML {
			rendered = f.HTML
		}
	}
	if rendered != "<rendered># Title\n\nbody</rendered>" {
		t.Fatalf("want the answer so far rendered, got %q", rendered)
	}

	// A page connecting now gets the rendered prefix plus the raw tail, never
	// the tail twice.
	snapshot, running, _, cancel := svc.Subscribe(created.ID)
	defer cancel()
	if !running || snapshot.HTML != rendered || snapshot.Text != "" {
		t.Fatalf("unexpected snapshot: running=%v html=%q text=%q", running, snapshot.HTML, snapshot.Text)
	}

	close(runner.block)
	waitIdle(t, svc, created.ID)
}

func TestStoreQuarantinesACorruptTranscript(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	svc := newService(store, NewRunStore(dir), fakeCoders{runner: &fakeRunner{}}, &fakeProjects{root: filepath.Join(dir, "projects")})
	created, _ := svc.create("claude", "/projects/demo")

	_, conversations, _ := Paths(dir)
	path := filepath.Join(conversations, created.ID+".json")
	if err := writeFile(path, "{not json"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := store.Load(created.ID); ok {
		t.Fatal("want a corrupt transcript to read as missing")
	}
	if _, err := fileExists(path + ".broken"); err != nil {
		t.Fatalf("want the corrupt file quarantined: %v", err)
	}
	if len(store.List()) != 1 {
		t.Fatal("want the index entry kept, so the loss stays visible")
	}
}

func TestStoreRejectsAnIDThatCouldEscapeTheDirectory(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, ok := store.Load("../../etc/passwd"); ok {
		t.Fatal("want a path-shaped id refused")
	}
	if err := store.Delete("../../etc/passwd"); err == nil {
		t.Fatal("want a path-shaped id refused on delete")
	}
	if ValidID("../x") || ValidID("") || ValidID("short") {
		t.Fatal("want ValidID to reject anything that is not a conversation id")
	}
}

func TestNewsReachesTheUserForAFinishedAndAFailedTurn(t *testing.T) {
	cases := []struct {
		name   string
		events []Event
		want   bool
	}{
		{"a finished answer", []Event{{Kind: EventDelta, Text: "done"}}, true},
		{"a failed answer", []Event{{Kind: EventError, Err: errors.New("the coder gave up")}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newTestService(t, &fakeRunner{events: tc.events})
			var mu sync.Mutex
			var notified []string
			svc.SetHooks(func() {}, func(id string) {
				mu.Lock()
				defer mu.Unlock()
				notified = append(notified, id)
			})

			created, err := svc.create("claude", "/projects/demo")
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if _, err := svc.Send(created.ID, "hello", nil); err != nil {
				t.Fatalf("send: %v", err)
			}
			waitIdle(t, svc, created.ID)

			waitFor(t, "the notification", func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(notified) == 1 && notified[0] == created.ID
			})
		})
	}
}

// A turn the user stopped stays silent: they were there when it happened.
func TestACancelledTurnIsNotNews(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)
	var mu sync.Mutex
	var notified []string
	svc.SetHooks(func() {}, func(id string) {
		mu.Lock()
		defer mu.Unlock()
		notified = append(notified, id)
	})

	created, err := svc.create("claude", "/projects/demo")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, "the turn to start", func() bool { return svc.Running(created.ID) })
	if err := svc.Cancel(created.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitIdle(t, svc, created.ID)

	mu.Lock()
	defer mu.Unlock()
	if len(notified) != 0 {
		t.Fatalf("want no notification for a stopped turn, got %v", notified)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func fileExists(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		return false, err
	}
	return true, nil
}

// A rename lands in the transcript while an answer streams, and the run writes
// that same transcript from its own copy. The copy has to learn about the new
// title, or the next flush would quietly put the old one back.
// A rename while a turn is running has to hold: the answer lands afterwards and
// writes into the same transcript.
func TestRenameDuringARunSurvivesTheEndOfTheTurn(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "partial"}}, block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	frames := collectFrames(svc, created.ID)
	defer frames.stop()
	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, "the first delta", func() bool { return frames.has(FrameDelta) })

	if err := svc.Rename(created.ID, "Renamed while running"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	close(runner.block)
	final := waitIdle(t, svc, created.ID)
	if final.Content != "partial" {
		t.Fatalf("want the answer intact, got %q", final.Content)
	}
	if c, _ := svc.Get(created.ID); c.Title != "Renamed while running" {
		t.Fatalf("want the rename to survive the end of the turn, got %q", c.Title)
	}
}

func TestReapUploadsDropsOnlyWhatNoMessagePointsAt(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}}
	svc, store, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	dir, err := svc.UploadDir(created.ID)
	if err != nil {
		t.Fatalf("files dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create files dir: %v", err)
	}
	sent := filepath.Join(dir, "sent.png")
	orphan := filepath.Join(dir, "never-sent.png")
	fresh := filepath.Join(dir, "still-picking.png")
	for _, path := range []string{sent, orphan, fresh} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, path := range []string{sent, orphan} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("age %s: %v", path, err)
		}
	}
	if _, err := svc.Send(created.ID, "look at this", []Attachment{{Name: "sent.png", Path: sent, Media: "image"}}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, created.ID)

	// A directory of a conversation that no longer exists goes away with its files.
	gone := filepath.Join(store.UploadRoot(), "99999999-9999-4999-8999-999999999999")
	if err := os.MkdirAll(gone, 0o700); err != nil {
		t.Fatalf("create stale dir: %v", err)
	}
	stale := filepath.Join(gone, "leftover.png")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("age stale: %v", err)
	}

	svc.ReapUploads(time.Hour)

	if _, err := os.Stat(sent); err != nil {
		t.Fatal("a file a message points at must never be reaped")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("a file that is younger than the grace period is still waiting for its message")
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Fatal("an upload no message points at should be gone")
	}
	if _, err := os.Stat(gone); err == nil {
		t.Fatal("the directory of a deleted conversation should be gone")
	}
}

// A draft is a message that was not sent yet, and its files are as referenced as
// a sent one's. A composer can stand open for a day, so the grace period alone
// does not save them: reaping them leaves the draft pointing at files that are
// gone, and sending it fails on a file the user still sees in the composer.
func TestReapUploadsKeepsWhatADraftPointsAt(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude", "/projects/demo")

	dir, err := svc.UploadDir(created.ID)
	if err != nil {
		t.Fatalf("files dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create files dir: %v", err)
	}
	drafted := filepath.Join(dir, "drafted.png")
	orphan := filepath.Join(dir, "never-picked.png")
	old := time.Now().Add(-2 * time.Hour)
	for _, path := range []string{drafted, orphan} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("age %s: %v", path, err)
		}
	}
	if _, _, err := svc.SaveDraft(created.ID, "look at this", []Attachment{{Name: "drafted.png", Path: drafted, Media: "image"}}); err != nil {
		t.Fatalf("save draft: %v", err)
	}

	svc.ReapUploads(time.Hour)

	if _, err := os.Stat(drafted); err != nil {
		t.Fatal("a file the open draft points at must never be reaped")
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Fatal("an upload nothing points at should be gone")
	}
}

// A title is written for the page, a session name is what a CLI accepts:
// copilot refuses anything over 100 characters, so the name is cut, on a rune
// boundary so a German title never breaks inside a character.
func TestSessionNameFitsWhatTheCLIsAccept(t *testing.T) {
	short := "Fix the login redirect"
	if got := SessionName(short); got != short {
		t.Fatalf("want a short title untouched, got %q", got)
	}
	long := strings.Repeat("ü", 200)
	got := SessionName(long)
	if len(got) > MaxSessionNameBytes {
		t.Fatalf("want at most %d bytes, got %d", MaxSessionNameBytes, len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("want a valid string, got %q", got)
	}
	if got == "" {
		t.Fatal("want a name, got nothing")
	}
}

// Search is the read behind the assistant's `conversation-list` command. The index
// alone answers a title match; a word that only fell in a message needs the
// transcript, so the search goes through the store the way the command does.
func TestSearchMatchesTitleAndMessageContentCaseInsensitively(t *testing.T) {
	svc, store, _ := newTestService(t, nil)
	old := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	store.Save(Conversation{
		Summary:  Summary{ID: "11111111-1111-4111-8111-111111111111", Title: "Fix the tabs", CoderID: "claude", Status: StatusArchived},
		Messages: []Message{{ID: "m1", Role: RoleUser, Content: "the strip flickers", CreatedAt: old, State: StateComplete}},
	})
	store.Save(Conversation{
		Summary:  Summary{ID: "22222222-2222-4222-8222-222222222222", Title: "Weekend plans", CoderID: "claude", Status: StatusArchived},
		Messages: []Message{{ID: "m2", Role: RoleAssistant, Content: "The BACKUP ran fine.", CreatedAt: old.Add(time.Hour), State: StateComplete}},
	})

	all := svc.Search("")
	if len(all) != 2 || all[0].ID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("want the whole index newest first, got %+v", all)
	}
	byTitle := svc.Search("TABS")
	if len(byTitle) != 1 || byTitle[0].ID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("a word in the title has to match regardless of case, got %+v", byTitle)
	}
	byContent := svc.Search("backup")
	if len(byContent) != 1 || byContent[0].ID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("a word in a message has to match regardless of case, got %+v", byContent)
	}
	if none := svc.Search("nowhere"); len(none) != 0 {
		t.Fatalf("a word nobody wrote must match nothing, got %+v", none)
	}
}

// Transcript is the read behind the `conversation-show` command: the last entries,
// each message cut visibly, the dropped tail counted instead of hidden, and
// the stored transcript untouched.
func TestTranscriptWindowsAndCutsVisibly(t *testing.T) {
	svc, store, _ := newTestService(t, nil)
	id := "33333333-3333-4333-8333-333333333333"
	var messages []Message
	for i := 0; i < TranscriptEntriesShown+4; i++ {
		messages = append(messages, Message{
			ID: fmt.Sprintf("m%d", i), Role: RoleUser, State: StateComplete,
			Content:   fmt.Sprintf("message %d %s", i, strings.Repeat("a", 60)),
			CreatedAt: time.Date(2026, 3, 4, 9, i, 0, 0, time.UTC),
		})
	}
	store.Save(Conversation{Summary: Summary{ID: id, Title: "long", CoderID: "claude", Status: StatusArchived}, Messages: messages})

	c, dropped, err := svc.Transcript(id, 0, 20)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if len(c.Messages) != TranscriptEntriesShown || dropped != 4 {
		t.Fatalf("want the default window of %d with 4 dropped, got %d with %d", TranscriptEntriesShown, len(c.Messages), dropped)
	}
	last := c.Messages[len(c.Messages)-1]
	if !strings.HasPrefix(last.Content, fmt.Sprintf("message %d", TranscriptEntriesShown+3)) {
		t.Fatalf("the window has to keep the newest messages, got %q", last.Content)
	}
	if !strings.Contains(last.Content, "runes shown, use --full") {
		t.Fatalf("a cut message has to say how much of it is shown, got %q", last.Content)
	}

	whole, droppedMore, err := svc.Transcript(id, 2, 0)
	if err != nil {
		t.Fatalf("transcript without a budget: %v", err)
	}
	if len(whole.Messages) != 2 || droppedMore != TranscriptEntriesShown+2 {
		t.Fatalf("want 2 messages with %d dropped, got %d with %d", TranscriptEntriesShown+2, len(whole.Messages), droppedMore)
	}
	if strings.Contains(whole.Messages[1].Content, "[cut:") {
		t.Fatalf("a zero budget has to keep the message whole, got %q", whole.Messages[1].Content)
	}
	if stored, err := svc.Get(id); err != nil || strings.Contains(stored.Messages[len(stored.Messages)-1].Content, "[cut:") {
		t.Fatalf("the stored transcript must never carry a cut note, got %v", err)
	}
	if _, _, err := svc.Transcript("99999999-9999-4999-8999-999999999999", 0, 0); err == nil {
		t.Fatal("an unknown conversation has to be refused")
	}
}
