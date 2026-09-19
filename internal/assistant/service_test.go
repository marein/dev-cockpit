package assistant

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/detach"
	"github.com/marein/dev-cockpit/internal/markdown"
	"github.com/marein/dev-cockpit/internal/statefile"
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
	stderr string
	// env is what the command asks for in its environment, the way opencode
	// asks for its config. When set, the process writes what it found under
	// DC_FAKE_TURN and whether PATH came along into env-<seq>, so a test can
	// read what the turn really ran with.
	env        []string
	exists     bool
	deleted    []string
	deleteFn   func(string) error
	requests   []TurnRequest
	commandErr error
	// commandGate makes Command wait, the way opencode's first turn does
	// while it boots a server to create its provider session: a test holds
	// the gate to look at the service mid launch.
	commandGate chan struct{}
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
	env := r.env
	commandErr := r.commandErr
	gate := r.commandGate
	r.seq++
	seq := r.seq
	dir := r.dir
	r.mu.Unlock()
	if gate != nil {
		<-gate
	}
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
	if env != nil {
		seen := filepath.Join(dir, fmt.Sprintf("env-%d", seq))
		fmt.Fprintf(&script, "printf '%%s\n%%s\n' \"${DC_FAKE_TURN-unset}\" \"${PATH:+inherited}\" > %s\n", seen)
	}
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
	return Command{Name: "/bin/sh", Args: []string{"-c", script.String()}, Env: env}, nil
}

// envSeen reads what the process of turn seq found in its environment: the
// value under DC_FAKE_TURN, and whether PATH was inherited.
func (r *fakeRunner) envSeen(t *testing.T, seq int) (value string, inherited bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.dir, fmt.Sprintf("env-%d", seq)))
	if err != nil {
		t.Fatalf("the turn wrote no environment record: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected environment record %q", data)
	}
	return lines[0], lines[1] == "inherited"
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

// mustWorkdir is the workspace of one instance, the directory a turn of its
// runs in.
func mustWorkdir(t *testing.T, svc *Service, id string) string {
	t.Helper()
	dir, err := svc.workdirs.Workdir(id)
	if err != nil {
		t.Fatalf("workdir of %s: %v", id, err)
	}
	return dir
}

// fakeWorkdirs gives every instance a real directory, because a turn is a
// real process and a process needs somewhere to run.
type fakeWorkdirs struct {
	missing bool
	root    string
}

func (p *fakeWorkdirs) Workdir(instanceID string) (string, error) {
	if p.missing {
		return "", errors.New("gone")
	}
	dir := filepath.Join(p.root, instanceID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func newTestService(t *testing.T, runner *fakeRunner) (*Service, *Store, *fakeWorkdirs) {
	t.Helper()
	svc, store, projects, _ := newTestServiceIn(t, t.TempDir(), runner)
	return svc, store, projects
}

// newTestServiceIn builds a service over a state directory a test names itself,
// so a restart can be played: the second service reads the same store and the
// same run register, and picks up whatever the first one left running.
func newTestServiceIn(t *testing.T, dir string, runner *fakeRunner) (*Service, *Store, *fakeWorkdirs, *RunStore) {
	t.Helper()
	if runner != nil && runner.dir == "" {
		runner.dir = t.TempDir()
		over := make(chan struct{})
		t.Cleanup(func() { runner.end(over) })
		runner.over = over
	}
	store := NewStore(dir)
	runs := NewRunStore(dir)
	projects := &fakeWorkdirs{root: filepath.Join(dir, "projects")}
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
		// write the last of it while the directories are still there. The
		// process is read back under the lock: a turn still launching gets
		// one the moment its launcher finishes, and the cancelled mark is
		// what makes the launcher kill it right then.
		for _, a := range open {
			a.cancelled.Store(true)
			svc.mu.Lock()
			proc, launched := a.proc, a.launched
			svc.mu.Unlock()
			if launched {
				proc.Kill()
			}
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

	created, err := svc.create("claude")
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
	if len(turns) != 1 || turns[0].Resume || turns[0].SessionID != created.ID || turns[0].Instance != created.ID || filepath.Base(turns[0].Workdir) != created.ID {
		t.Fatalf("unexpected first turn: %+v", turns)
	}
}

// What a coder asks for in its environment reaches its process, on top of
// what this server inherits: opencode carries its config that way, and a
// variable that stayed behind would put the question tool back into a headless
// run. A command asking for nothing inherits the environment untouched.
func TestATurnRunsWithTheCommandsEnvironment(t *testing.T) {
	runner := &fakeRunner{
		events: []Event{{Kind: EventDelta, Text: "ok"}},
		env:    []string{"DC_FAKE_TURN=from the command"},
	}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")
	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	if final := waitIdle(t, svc, created.ID); final.State != StateComplete {
		t.Fatalf("want the turn complete, got %s", final.State)
	}
	value, inherited := runner.envSeen(t, 1)
	if value != "from the command" {
		t.Fatalf("want the command's variable in the process, got %q", value)
	}
	if !inherited {
		t.Fatal("want the server's own environment kept under the command's variables")
	}

	runner.mu.Lock()
	runner.env = []string{}
	runner.mu.Unlock()
	if _, err := svc.Send(created.ID, "again", nil); err != nil {
		t.Fatalf("second send: %v", err)
	}
	if final := waitIdle(t, svc, created.ID); final.State != StateComplete {
		t.Fatalf("want the second turn complete, got %s", final.State)
	}
	value, inherited = runner.envSeen(t, 2)
	if value != "unset" || !inherited {
		t.Fatalf("want a command without variables to inherit the environment alone, got %q inherited=%v", value, inherited)
	}
}

// A message is written on one device and read on all of them. It goes out on
// the conversation's stream before the answer opens, so a panel that stands
// open somewhere else pulls the question and puts the answer under it, instead
// of showing an answer to something nobody there can see.
func TestASentMessageIsAnnouncedBeforeItsAnswer(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

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
	created, _ := svc.create("claude")

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

// One turn per assistant: a second prompt into one that is answering waits in
// that assistant's queue, and the end of the turn sends everything waiting as
// one new turn.
func TestSendWhileRunningQueuesAndFlushesAsOneTurn(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}, block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

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
	instance, _ := svc.Get(created.ID)
	waiting := queuedMessages(instance)
	if len(waiting) != 2 || waiting[0].Content != "second" || waiting[1].Content != "third" {
		t.Fatalf("want both messages waiting in order, got %+v", waiting)
	}
	waitFor(t, "the waiting entries to reach the stream", func() bool { return frames.has(FrameMessage) })

	close(runner.block)
	waitFor(t, "the flush turn to start", func() bool { return len(runner.turns()) == 2 })
	waitIdle(t, svc, created.ID)

	turns := runner.turns()
	firstAt := strings.Index(turns[1].Prompt, "--- Message 1 ---\nsecond")
	next := strings.Index(turns[1].Prompt, "--- Message 2 ---\nthird")
	if firstAt < 0 || next < 0 || next < firstAt {
		t.Fatalf("want one flush turn carrying both messages in order, got %q", turns[1].Prompt)
	}
	instance, _ = svc.Get(created.ID)
	if len(queuedMessages(instance)) != 0 {
		t.Fatalf("want no message left waiting, got %+v", instance.Messages)
	}
	last, _ := instance.Last()
	if last.Role != RoleAssistant || last.State != StateComplete || last.Content != "ok" {
		t.Fatalf("want the flush turn answered, got %+v", last)
	}
}

// The queue is one assistant's. A thread that is thinking holds up nothing but
// itself: every other assistant answers at the same time, and a message waiting
// in one of them waits for that one alone.
func TestAWaitingAssistantDoesNotBlockAnother(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}, block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)
	waiting, _ := svc.Create("claude")
	other, _ := svc.Create("claude")

	if _, err := svc.Send(waiting.ID, "first", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	queued, err := svc.Send(waiting.ID, "second", nil)
	if err != nil || !queued.Queued {
		t.Fatalf("want the second message queued, got %+v, %v", queued, err)
	}
	mine, err := svc.Send(other.ID, "mine", nil)
	if err != nil || mine.Queued {
		t.Fatalf("want the other assistant to answer right away, got %+v, %v", mine, err)
	}
	// Nothing of the first assistant's queue reached the second one.
	instance, _ := svc.Get(other.ID)
	if len(queuedMessages(instance)) != 0 {
		t.Fatalf("want nothing waiting in the other assistant, got %+v", instance.Messages)
	}

	close(runner.block)
	waitIdle(t, svc, waiting.ID)
	waitIdle(t, svc, other.ID)
}

// A waiting message can be taken back until the flush sends it, and one that
// already went out is answered instead of silently dropped.
func TestAQueuedMessageCanBeDiscardedWhileItWaits(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}, block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

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
	instance, _ := svc.Get(created.ID)
	if err := svc.Discard(created.ID, instance.Messages[0].ID); err == nil {
		t.Fatal("want a discard of a sent message to be refused")
	}

	close(runner.block)
	waitIdle(t, svc, created.ID)
	if turns := runner.turns(); len(turns) != 1 {
		t.Fatalf("want no flush turn after the discard, got %d turns", len(turns))
	}
	instance, _ = svc.Get(created.ID)
	if len(instance.Messages) != 2 {
		t.Fatalf("want the discarded message gone from the transcript, got %+v", instance.Messages)
	}
}

// Stopping the running turn is what lets the queue go right away, with the
// stopped answer still standing above it.
func TestStoppingATurnFlushesTheQueueRightAway(t *testing.T) {
	block := make(chan struct{})
	runner := &fakeRunner{
		events: []Event{{Kind: EventDelta, Text: "partial"}},
		// The prompt a turn is held on is matched at its end: the assistant is
		// named after its first message, so "contains" would hold the second
		// turn too.
		hold: func(req TurnRequest) chan struct{} {
			if strings.HasSuffix(req.Prompt, "first") {
				return block
			}
			return nil
		},
	}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

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
	// On its own, not joined with anything.
	if turns[1].Prompt != "second" {
		t.Fatalf("want the queued message flushed on its own, got %q", turns[1].Prompt)
	}
	instance, _ := svc.Get(created.ID)
	var stopped, answered bool
	for _, m := range instance.Messages {
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
		t.Fatalf("want a stopped answer and a flushed one, got %+v", instance.Messages)
	}
}

// A message that queued before the restart is still waiting in its transcript,
// and the recovery is what lets it go.
func TestAQueuedMessageSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	first := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}}
	svc1, store, _, _ := newTestServiceIn(t, dir, first)
	created, err := svc1.create("claude")
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
	if len(turns) != 1 || !strings.HasSuffix(turns[0].Prompt, "queued while the server was down") {
		t.Fatalf("want the waiting message flushed after the restart, got %+v", turns)
	}
	instance, _ := svc2.Get(created.ID)
	if len(queuedMessages(instance)) != 0 {
		t.Fatalf("want nothing left waiting, got %+v", instance.Messages)
	}
	last, _ := instance.Last()
	if last.State != StateComplete || last.Content != "after the restart" {
		t.Fatalf("want the flushed turn answered, got %+v", last)
	}
}

// Running and unfinished are two different things and the index keeps them
// apart: a turn under way is work, a turn that stopped before it was done is
// the one thing left to report. Nothing downstream has to read one out of the
// other.
func TestTheIndexTellsARunningTurnFromAnUnfinishedOne(t *testing.T) {
	block := make(chan struct{})
	runner := &fakeRunner{
		events: []Event{{Kind: EventDelta, Text: "partial"}},
		hold:   func(TurnRequest) chan struct{} { return block },
	}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

	frames := collectFrames(svc, created.ID)
	defer frames.stop()
	if _, err := svc.Send(created.ID, "first", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, "the first delta", func() bool { return frames.has(FrameDelta) })
	if entries := svc.List(); len(entries) != 1 || !entries[0].Running || entries[0].Unfinished {
		t.Fatalf("want the turn under way marked running and not unfinished, got %+v", entries)
	}

	if err := svc.Cancel(created.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitIdle(t, svc, created.ID)
	if entries := svc.List(); len(entries) != 1 || entries[0].Running || !entries[0].Unfinished {
		t.Fatalf("want the stopped turn marked unfinished and not running, got %+v", entries)
	}
}

// A restart starts no turn of its own where nothing was waiting. A recovery
// that sent something anyway would be charging the user for a prompt they never
// see going out.
func TestARestartStartsNoTurnOfItsOwn(t *testing.T) {
	dir := t.TempDir()
	first := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}}
	svc1, _, _, _ := newTestServiceIn(t, dir, first)
	created, err := svc1.create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc1.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc1, created.ID)

	second := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "after the restart"}}}
	svc2, _, _, _ := newTestServiceIn(t, dir, second)
	svc2.Recover()

	if turns := second.turns(); len(turns) != 0 {
		t.Fatalf("the restart started a turn nobody asked for: %+v", turns)
	}
	instance, _ := svc2.Get(created.ID)
	if len(instance.Messages) != 2 {
		t.Fatalf("want the transcript as it was, got %+v", instance.Messages)
	}
	if !instance.Idle() {
		t.Fatal("want the assistant idle after the restart")
	}
	// And it takes the next message the moment somebody sends one.
	if _, err := svc2.Send(created.ID, "again", nil); err != nil {
		t.Fatalf("send after the restart: %v", err)
	}
	waitIdle(t, svc2, created.ID)
}

// There is no global cap on chat turns any more: every assistant answers at
// the same time as every other. What is still capped is one turn per assistant,
// and that one is a refusal, see TestSendWhileRunningIsRefusedAsBusy.
func TestEveryAssistantAnswersAtTheSameTime(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)

	const assistants = 4
	var ids []string
	for i := 0; i < assistants; i++ {
		created, err := svc.Create("claude")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		ids = append(ids, created.ID)
	}
	for i, id := range ids {
		if _, err := svc.Send(id, "hello", nil); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	for _, id := range ids {
		if !svc.Running(id) {
			t.Fatalf("want %s answering, nothing may wait for another assistant", id)
		}
	}

	close(runner.block)
	for _, id := range ids {
		waitIdle(t, svc, id)
	}
}

func TestCancelKeepsThePartialAnswer(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "partial"}}, block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

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
	created, _ := svc.create("claude")

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
	if len(turns) != 2 || !strings.HasSuffix(turns[1].Prompt, "hello") {
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
	created, _ := svc.create("claude")

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
	created, _ := svc.create("claude")

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
	created, _ := svc.create("claude")
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
	created, _ := svc.create("claude")

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
	created, _ := svc.create("claude")

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
		ID:        statefile.NewID(),
		Kind:      RunChat,
		Instance:  c.ID,
		MessageID: statefile.NewID(),
		CoderID:   "claude",
		SessionID: c.NativeSessionID,
	}
	c.Messages = append(c.Messages, Message{
		ID:        rec.MessageID,
		Role:      RoleAssistant,
		CreatedAt: time.Now().UTC(),
		RunID:     rec.ID,
		State:     StateStreaming,
	})
	svc.store.Save(c)
	if _, err := svc.launch(&rec, runner, TurnRequest{SessionID: c.NativeSessionID, Workdir: mustWorkdir(t, svc, c.ID), Prompt: "hello"}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	return rec
}

// A started turn describes itself on disk. Everything the recovery works from
// comes out of this entry, so it is checked against what starting a turn the
// normal way writes.
func TestAStartedTurnIsInTheRegister(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{})}
	svc, _, _, runs := newTestServiceIn(t, t.TempDir(), runner)
	created, _ := svc.create("claude")
	run, err := svc.Send(created.ID, "hello", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	// The entry lands when the launch does, off the send: the send answers
	// before the runner built its command.
	waitFor(t, "the registered turn", func() bool { return len(runs.List()) == 1 })
	list := runs.List()
	if len(list) != 1 {
		t.Fatalf("want one registered turn, got %d", len(list))
	}
	rec := list[0]
	switch {
	case rec.ID != run.RunID || rec.MessageID != run.MessageID:
		t.Fatalf("want the entry to name the run and its message, got %+v", rec)
	case rec.Kind != RunChat || rec.Instance != created.ID || rec.CoderID != "claude":
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
	created, _ := svc.create("claude")
	rec := orphanTurn(t, svc, created.ID, runner)

	if !detach.Alive(rec.PID, rec.Lock) {
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
	created, _ := svc.create("claude")
	rec := orphanTurn(t, svc, created.ID, runner)
	waitFor(t, "the turn to end on its own", func() bool { return !detach.Alive(rec.PID, rec.Lock) })

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
	created, _ := svc.create("claude")
	rec := orphanTurn(t, svc, created.ID, runner)
	waitFor(t, "the turn to die", func() bool { return !detach.Alive(rec.PID, rec.Lock) })

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
	created, _ := svc.create("claude")
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

// A turn that outlived the restart is picked up as this assistant's running
// turn again, so a message sent into it queues behind it instead of starting a
// second one next to it.
func TestARecoveredTurnStillHoldsItsAssistant(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{block: make(chan struct{})}
	svc, _, _, _ := newTestServiceIn(t, dir, runner)
	created, _ := svc.create("claude")
	orphanTurn(t, svc, created.ID, runner)

	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	restarted.Recover()
	waitFor(t, "the restarted server to pick the turn up", func() bool {
		return restarted.Running(created.ID)
	})

	queued, err := restarted.Send(created.ID, "one more", nil)
	if err != nil || !queued.Queued {
		t.Fatalf("want the send queued behind the recovered turn, got %+v, %v", queued, err)
	}

	close(runner.block)
	waitIdle(t, restarted, created.ID)
}

// A stop is written down before the process is killed, so a restart that lands
// in between still reads it as a stop and not as a coder that fell over.
func TestAStoppedTurnStaysStoppedAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "partial"}}, unfinished: true}
	svc, _, _, runs := newTestServiceIn(t, dir, runner)
	created, _ := svc.create("claude")
	rec := orphanTurn(t, svc, created.ID, runner)
	rec.Cancelled = true
	runs.Save(rec)
	waitFor(t, "the turn to end", func() bool { return !detach.Alive(rec.PID, rec.Lock) })

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
	created, _ := svc.create("claude")
	rec := orphanTurn(t, svc, created.ID, runner)
	waitFor(t, "the turn to end", func() bool { return !detach.Alive(rec.PID, rec.Lock) })
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

// The reservation is what keeps an assistant's provider session out of the
// coder lists, and it is one coder's: the same id under another coder is not
// reserved.
func TestReservationHidesTheSession(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "hi"}}, exists: false}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

	if !svc.Reserved("claude", created.ID) {
		t.Fatal("want an assistant to reserve its provider session")
	}
	if svc.Reserved("copilot", created.ID) {
		t.Fatal("want the reservation to be scoped to its coder")
	}

	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, created.ID)
	if !svc.Reserved("claude", created.ID) {
		t.Fatal("want the reservation to outlive a turn")
	}
}

// Every assistant that exists stays live across a restart, and each keeps its
// provider session reserved: none of them may turn up as a resumable coder.
func TestStartupKeepsEveryAssistantLive(t *testing.T) {
	runner := &fakeRunner{exists: true}
	svc, store, _ := newTestService(t, runner)
	first, _ := svc.create("claude")
	second, _ := svc.create("claude")

	restarted := newService(store, svc.runs, svc.coders, svc.workdirs)

	for _, id := range []string{first.ID, second.ID} {
		if _, err := restarted.Get(id); err != nil {
			t.Fatalf("get: %v", err)
		}
		if !restarted.Reserved("claude", id) {
			t.Fatalf("want the session of %s reserved", id)
		}
	}
}

// Starting another assistant changes nothing about the ones that are there:
// they keep their session, their transcript and their composer. This is the
// rule the whole feature turns on, the one that used to say the opposite.
func TestANewAssistantLeavesTheOthersAlone(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "hi"}}, exists: true}
	svc, _, _ := newTestService(t, runner)
	first, _ := svc.create("claude")
	if _, err := svc.Send(first.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, first.ID)

	second, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("want a second assistant, got the first one back")
	}

	if _, err := svc.Get(first.ID); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(runner.deleted) != 0 {
		t.Fatalf("want no provider session dropped, got %v", runner.deleted)
	}
	if !svc.Reserved("claude", first.ID) || !svc.Reserved("claude", second.ID) {
		t.Fatal("want both sessions reserved")
	}
	if _, err := svc.Send(first.ID, "again", nil); err != nil {
		t.Fatalf("want the first assistant to keep taking messages, got %v", err)
	}
}

// Creating is always somebody asking, and it always makes one: nothing reuses
// an untouched assistant any more, because nothing creates by itself either.
// That is what keeps the list what the user made.
func TestCreateStartsAnotherEveryTime(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})

	first, err := svc.Create("")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	second, err := svc.Create("")
	if err != nil {
		t.Fatalf("create again: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("want two assistants")
	}
	if len(svc.List()) != 2 {
		t.Fatalf("want two assistants in the index, got %d", len(svc.List()))
	}
	if list := svc.List(); list[0].ID != second.ID {
		t.Fatalf("want the one just made at the top of the list, got %s", list[0].ID)
	}
}

// The list order is the user's. A drag writes it, and it survives everything
// that writes the index afterwards: an answer in another assistant does not
// move anybody, and a new one lands on top without disturbing the rest.
func TestTheListKeepsTheOrderItWasSortedInto(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})

	first, _ := svc.Create("")
	second, _ := svc.Create("")
	third, _ := svc.Create("")

	svc.Reorder([]string{first.ID, third.ID, second.ID})
	if got := ids(svc.List()); !slices.Equal(got, []string{first.ID, third.ID, second.ID}) {
		t.Fatalf("want the posted order, got %v", got)
	}

	// An answer in the one at the back leaves the order alone.
	if _, err := svc.Send(second.ID, "hi", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, second.ID)
	if got := ids(svc.List()); !slices.Equal(got, []string{first.ID, third.ID, second.ID}) {
		t.Fatalf("want the order kept after an answer, got %v", got)
	}

	fourth, _ := svc.Create("")
	if got := ids(svc.List()); !slices.Equal(got, []string{fourth.ID, first.ID, third.ID, second.ID}) {
		t.Fatalf("want the new one on top, got %v", got)
	}
}

// An id the index does not carry is dropped, and an assistant the post never
// saw keeps its exact seat instead of being pushed to the end.
func TestReorderLeavesWhatItDoesNotNameWhereItIs(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})

	first, _ := svc.Create("")
	second, _ := svc.Create("")
	third, _ := svc.Create("")
	// Created newest first, so the list reads third, second, first.

	svc.Reorder([]string{"11111111-1111-4111-8111-111111111111", third.ID, first.ID})
	if got := ids(svc.List()); !slices.Equal(got, []string{third.ID, second.ID, first.ID}) {
		t.Fatalf("want the unknown id dropped and the seats kept, got %v", got)
	}

	svc.Reorder([]string{first.ID, third.ID})
	if got := ids(svc.List()); !slices.Equal(got, []string{first.ID, second.ID, third.ID}) {
		t.Fatalf("want the two named to swap seats around the one unnamed, got %v", got)
	}
}

func ids(list []Summary) []string {
	out := make([]string, 0, len(list))
	for _, entry := range list {
		out = append(out, entry.ID)
	}
	return out
}

func TestDeleteRemovesTheProviderSession(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "hi"}}, exists: true}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

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
	created, _ := svc.create("claude")

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
	created, _ := svc.create("claude")
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

// Settling a stopped turn lets the queue go, and the flush starts the next turn
// before the stopped one has closed its done channel. A delete that stopped
// one turn and moved on left that next turn running for an assistant whose
// transcript was gone: registered as running, recreating the workspace it was
// about to be started in, resuming a provider session the delete had just
// removed. The delete loops until nothing runs, and only then is nothing left
// that could start a turn.
func TestDeleteStopsTheTurnTheQueueFlushStarts(t *testing.T) {
	block := make(chan struct{})
	runner := &fakeRunner{
		events: []Event{{Kind: EventDelta, Text: "partial"}},
		// The first prompt is held open, so the second one queues behind it.
		hold: func(req TurnRequest) chan struct{} {
			if strings.HasSuffix(req.Prompt, "first") {
				return block
			}
			return nil
		},
	}
	svc, store, _ := newTestService(t, runner)
	created, _ := svc.create("claude")
	frames := collectFrames(svc, created.ID)
	defer frames.stop()
	if _, err := svc.Send(created.ID, "first", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, "the first delta", func() bool { return frames.has(FrameDelta) })
	if _, err := svc.Send(created.ID, "second", nil); err != nil {
		t.Fatalf("queue: %v", err)
	}

	if err := svc.Delete(created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if svc.Running(created.ID) {
		t.Fatal("want no turn of the deleted assistant running once the delete returned")
	}
	if _, ok := store.Load(created.ID); ok {
		t.Fatal("want the transcript gone")
	}
	// The flush did start the second turn, that is the case this test is about,
	// and the delete stopped it: it ends without a transcript to write into and
	// brings nothing back.
	waitFor(t, "the flushed turn to have been started", func() bool { return len(runner.turns()) == 2 })
	waitFor(t, "the flushed turn to be over", func() bool { return !svc.Running(created.ID) })
	if _, ok := store.Load(created.ID); ok {
		t.Fatal("want the deleted assistant to stay deleted after its last turn ended")
	}
	close(block)
}

func TestUnavailableCoderAndProject(t *testing.T) {
	svc, _, projects := newTestService(t, &fakeRunner{})
	if _, err := svc.create("nope"); err == nil {
		t.Fatal("want an unknown coder to be refused")
	}
	created, _ := svc.create("claude")

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
	created, _ := svc.create("claude")

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
	created, _ := svc.create("claude")
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
	created, _ := svc.create("claude")

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
	if rendered != "<rendered># Title\n\nbody"+RenderMark+"</rendered>" {
		t.Fatalf("want the answer so far rendered with the mark behind it, got %q", rendered)
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

// The page hangs the text that arrived since the last render on the mark, so
// the mark has to come out of the real renderer where the next character
// belongs: inside the block that is still open, or as a block of its own where
// the prefix closed one. Without it the tail lands behind the rendered markup
// and a sentence still being typed reads as two paragraphs.
func TestTheRenderedPrefixMarksWhereTheNextTextGoes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix string
		want   string
	}{
		{"mid sentence", "where you started, never where", "where" + RenderMark + "</p>"},
		{"after a closed emphasis", "this one is *important*", "</em>" + RenderMark + "</p>"},
		{"inside an open code fence", "```go\nfunc main() {", "func main() {" + RenderMark + "\n</code>"},
		{"inside the open list item", "- one\n- two", "two" + RenderMark + "</li>"},
		{"behind a finished block", "The sentence stands.\n\n", "<p>" + RenderMark + "</p>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html, err := markdown.RenderGFM(tc.prefix + RenderMark)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !strings.Contains(html, tc.want) {
				t.Fatalf("want %q in the rendered prefix, got %q", tc.want, html)
			}
		})
	}
}

// Deleting an assistant deletes the whole of it. Its two folders in the shared
// workspace are its as much as the transcript is, and what is left of an
// assistant nobody can open is disk nobody can reach.
func TestDeleteTakesTheAssistantsOwnFilesWithIt(t *testing.T) {
	dir := t.TempDir()
	svc, workspace, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	mine, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	theirs, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, instances, memory := Paths(dir)
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{mine.ID, theirs.ID} {
		uploads, _ := svc.UploadDir(id)
		write(filepath.Join(uploads, "picked.png"))
		write(filepath.Join(workspace.Dir(id), FilesDirName, "written.patch"))
	}
	// What is shared belongs to nobody in particular.
	write(filepath.Join(memory, "likes-go.md"))

	if err := svc.Delete(mine.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := os.Stat(filepath.Join(instances, mine.ID)); !os.IsNotExist(err) {
		t.Fatalf("the instance directory survived the delete: %v", err)
	}
	// And nothing of anybody else went with it.
	theirUploads, _ := svc.UploadDir(theirs.ID)
	for _, kept := range []string{
		filepath.Join(instances, theirs.ID, "transcript.json"),
		filepath.Join(theirUploads, "picked.png"),
		filepath.Join(workspace.Dir(theirs.ID), FilesDirName, "written.patch"),
		filepath.Join(memory, "likes-go.md"),
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("the delete took %s with it: %v", kept, err)
		}
	}
}

func TestStoreQuarantinesACorruptTranscript(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	svc := newService(store, NewRunStore(dir), fakeCoders{runner: &fakeRunner{}}, &fakeWorkdirs{root: filepath.Join(dir, "projects")})
	created, _ := svc.create("claude")

	_, instances, _ := Paths(dir)
	path := filepath.Join(instances, created.ID, "transcript.json")
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

			created, err := svc.create("claude")
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

	created, err := svc.create("claude")
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
	created, _ := svc.create("claude")

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
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

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
}

// The draft is a file of its own next to the transcript. Saving one writes that
// file and neither the thread nor the index: a draft is saved every time the
// typing pauses, and a thread that has been going for a week must not be
// rewritten for a keystroke.
func TestADraftWritesItsOwnFileAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	svc, store, _, _ := newTestServiceIn(t, dir, &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}})
	created, _ := svc.create("claude")
	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, created.ID)

	transcript := filepath.Join(store.InstanceDir(created.ID), "transcript.json")
	index := filepath.Join(dir, "assistant", "assistant.json")
	before := map[string][]byte{}
	for _, path := range []string{transcript, index} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		before[path] = data
	}

	if _, changed, err := svc.SaveDraft(created.ID, "half a thought", nil); err != nil || !changed {
		t.Fatalf("save draft: %v (changed %v)", err, changed)
	}
	draft := filepath.Join(store.InstanceDir(created.ID), "draft.json")
	if _, err := os.Stat(draft); err != nil {
		t.Fatalf("want the draft in a file of its own: %v", err)
	}
	for path, data := range before {
		fresh, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s again: %v", path, err)
		}
		if string(fresh) != string(data) {
			t.Fatalf("a draft save rewrote %s", filepath.Base(path))
		}
	}
	if got, _ := svc.Draft(created.ID); got.Text != "half a thought" {
		t.Fatalf("want the draft read back, got %+v", got)
	}
	// The same draft again writes nothing and wakes nobody.
	if _, changed, _ := svc.SaveDraft(created.ID, "half a thought", nil); changed {
		t.Fatal("want an unchanged draft to announce nothing")
	}
	// And sending empties it, so the words do not come back into the box.
	if _, err := svc.Send(created.ID, "half a thought", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, created.ID)
	if got, _ := svc.Draft(created.ID); got.Text != "" {
		t.Fatalf("want the draft spent after the send, got %+v", got)
	}
}

// A draft written before it had a file of its own is carried over on the first
// read, once. Nobody has to think about which of the two places holds it.
func TestADraftInAnOldTranscriptIsCarriedOver(t *testing.T) {
	dir := t.TempDir()
	svc, store, _, _ := newTestServiceIn(t, dir, &fakeRunner{})
	created, _ := svc.create("claude")

	// The shape a transcript had while the draft lived inside it.
	path := filepath.Join(store.InstanceDir(created.ID), "transcript.json")
	var raw map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse transcript: %v", err)
	}
	raw["draft"] = map[string]any{"text": "typed before the move", "updatedAt": time.Now().UTC()}
	out, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	got, err := svc.Draft(created.ID)
	if err != nil || got.Text != "typed before the move" {
		t.Fatalf("want the old draft carried over, got %+v, %v", got, err)
	}
	// It is in the new file now, so the transcript is never read for it again:
	// clearing the file leaves the draft empty instead of reviving the old one.
	draft := filepath.Join(store.InstanceDir(created.ID), "draft.json")
	if err := os.WriteFile(draft, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write draft: %v", err)
	}
	if got, _ := svc.Draft(created.ID); got.Text != "" {
		t.Fatalf("want the transcript left alone after the move, got %+v", got)
	}
}

// A draft is a message that was not sent yet, and its files are as referenced as
// a sent one's. A composer can stand open for a day, so the grace period alone
// does not save them: reaping them leaves the draft pointing at files that are
// gone, and sending it fails on a file the user still sees in the composer.
func TestReapUploadsKeepsWhatADraftPointsAt(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

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

// Search is the read behind the `assistant-list` command. The index
// alone answers a title match; a word that only fell in a message needs the
// transcript, so the search goes through the store the way the command does.
func TestSearchMatchesTitleAndMessageContentCaseInsensitively(t *testing.T) {
	svc, store, _ := newTestService(t, nil)
	old := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	store.Save(Instance{
		Summary:  Summary{ID: "11111111-1111-4111-8111-111111111111", Title: "Fix the tabs", CoderID: "claude"},
		Messages: []Message{{ID: "m1", Role: RoleUser, Content: "the strip flickers", CreatedAt: old, State: StateComplete}},
	})
	store.Save(Instance{
		Summary:  Summary{ID: "22222222-2222-4222-8222-222222222222", Title: "Weekend plans", CoderID: "claude"},
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

	// The list and `assistant-show --contains` read a message with one rule,
	// so a headline the thread search finds makes the assistant carry the word
	// here too. Two searches in one tool that answer differently are worse
	// than one search.
	store.Save(Instance{
		Summary: Summary{ID: "33333333-3333-4333-8333-333333333333", Title: "Nightly", CoderID: "claude"},
		Messages: []Message{{
			ID: "m3", Role: RoleCockpit, Content: "the check found nothing to say", CreatedAt: old.Add(2 * time.Hour), State: StateComplete,
			Note: &Note{Source: NoteCheck, Headline: "Job done: release-task", Verdict: "DONE"},
		}},
	})
	byHeadline := svc.Search("release-task")
	if len(byHeadline) != 1 || byHeadline[0].ID != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("a word in a note's headline has to match, got %+v", byHeadline)
	}
}

// Transcript is the read behind the `assistant-show` command: the last entries,
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
	store.Save(Instance{Summary: Summary{ID: id, Title: "long", CoderID: "claude"}, Messages: messages})

	c, dropped, err := svc.Transcript(id, "", 0, 20)
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

	whole, droppedMore, err := svc.Transcript(id, "", 2, 0)
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
	if _, _, err := svc.Transcript("99999999-9999-4999-8999-999999999999", "", 0, 0); err == nil {
		t.Fatal("an unknown conversation has to be refused")
	}
}

// `assistant-show --contains` narrows a thread the way `job-list --contains`
// narrows the jobs: before the cap. A word somebody searches a long thread for
// sits older than the last entries as often as not, and a filter behind the
// window would answer nothing at all. Searched is the body and the headline a
// note of the cockpit and an answer a trigger pushed stand under: a schedule's
// tick carries its spec there and nowhere else. The window, the cut and
// `--full` then work over the matches like over any other reading.
func TestTranscriptContainsNarrowsBeforeTheWindow(t *testing.T) {
	svc, store, _ := newTestService(t, nil)
	id := "44444444-4444-4444-8444-444444444444"
	at := func(i int) time.Time { return time.Date(2026, 3, 4, 9, i, 0, 0, time.UTC) }
	// The first match stands at the very front, older than the default window.
	messages := []Message{{
		ID: "asked", Role: RoleUser, State: StateComplete,
		Content: "the RELEASE notes are wrong", CreatedAt: at(0),
	}}
	for i := 1; i <= TranscriptEntriesShown+2; i++ {
		messages = append(messages, Message{
			ID: fmt.Sprintf("m%d", i), Role: RoleAssistant, State: StateComplete,
			Content: "nothing to see here", CreatedAt: at(i),
		})
	}
	// A note carries the word in its headline alone, and a pushed answer
	// carries a schedule in its origin, which stands in no body anywhere.
	messages = append(messages,
		Message{
			ID: "note", Role: RoleCockpit, State: StateComplete,
			Note:    &Note{Source: NoteCheck, Headline: "Job done: release-task", Verdict: "DONE"},
			Content: "the coder reported the suite green", CreatedAt: at(20),
		},
		Message{
			ID: "reaction", Role: RoleAssistant, State: StateComplete, Auto: true,
			Origin:  &Note{Source: NoteEvent, Headline: "Schedule 0 9 * * 1 ticked at 09:00"},
			Content: "nothing to do", CreatedAt: at(21),
		},
	)
	store.Save(Instance{Summary: Summary{ID: id, Title: "long", CoderID: "claude"}, Messages: messages})

	hit, dropped, err := svc.Transcript(id, "release", 0, 0)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if len(hit.Messages) != 2 || dropped != 0 {
		t.Fatalf("want the body match and the headline match, got %d with %d dropped", len(hit.Messages), dropped)
	}
	if hit.Messages[0].ID != "asked" || hit.Messages[1].ID != "note" {
		t.Fatalf("a match older than the window has to survive it, got %q and %q", hit.Messages[0].ID, hit.Messages[1].ID)
	}
	if hit.MessageCount != len(messages) {
		t.Fatalf("the total has to stay the whole thread's, got %d of %d", hit.MessageCount, len(messages))
	}
	if lower, _, _ := svc.Transcript(id, "ReLeAsE", 0, 0); len(lower.Messages) != 2 {
		t.Fatalf("the word has to compare case insensitively, got %d", len(lower.Messages))
	}
	if spec, _, _ := svc.Transcript(id, "0 9 * * 1", 0, 0); len(spec.Messages) != 1 || spec.Messages[0].ID != "reaction" {
		t.Fatalf("a word that only the origin headline carries has to match it, got %+v", spec.Messages)
	}

	// --entries windows the matches, not the thread, and counts the older
	// matches it left out.
	last, droppedOne, err := svc.Transcript(id, "release", 1, 0)
	if err != nil {
		t.Fatalf("transcript with entries: %v", err)
	}
	if len(last.Messages) != 1 || droppedOne != 1 || last.Messages[0].ID != "note" {
		t.Fatalf("want the newest match alone with 1 dropped, got %d with %d", len(last.Messages), droppedOne)
	}

	// The per message cut composes with the filter, and a zero budget, which
	// is what --full asks for, keeps a match whole.
	cut, _, err := svc.Transcript(id, "release", 0, 8)
	if err != nil {
		t.Fatalf("transcript with a budget: %v", err)
	}
	if !strings.Contains(cut.Messages[0].Content, "runes shown, use --full") {
		t.Fatalf("a cut match has to say how much of it is shown, got %q", cut.Messages[0].Content)
	}
	if strings.Contains(hit.Messages[0].Content, "[cut:") {
		t.Fatalf("--full has to keep a match whole, got %q", hit.Messages[0].Content)
	}

	// A word nobody wrote answers an empty thread, and still knows how long
	// the thread is, so the reading can say the filter took everything out.
	none, droppedNone, err := svc.Transcript(id, "nowhere", 0, 0)
	if err != nil {
		t.Fatalf("transcript without a match: %v", err)
	}
	if len(none.Messages) != 0 || droppedNone != 0 {
		t.Fatalf("want nothing shown and nothing dropped, got %d with %d", len(none.Messages), droppedNone)
	}
	if none.MessageCount != len(messages) {
		t.Fatalf("an empty match still knows the thread, got %d", none.MessageCount)
	}
	if stored, err := svc.Get(id); err != nil || len(stored.Messages) != len(messages) {
		t.Fatalf("a filtered reading must never touch the stored transcript, got %v", err)
	}
}

// The runner may work seconds building its command, the way opencode's first
// turn does while it boots a server to create its provider session. The send
// answers before that work begins, and the turn counts as running from that
// moment: a second send in the launch window queues rather than starting a
// second turn next to the one that is still coming up.
func TestASendAnswersWhileTheCommandIsStillBuilt(t *testing.T) {
	gate := make(chan struct{})
	runner := &fakeRunner{commandGate: gate, events: []Event{{Kind: EventDelta, Text: "hi"}}}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

	run, err := svc.Send(created.ID, "hello", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if run.RunID == "" {
		t.Fatal("the first send has to start a turn")
	}
	queued, err := svc.Send(created.ID, "more", nil)
	if err != nil || !queued.Queued {
		t.Fatalf("a send during the launch has to queue, got %+v, %v", queued, err)
	}
	close(gate)
	waitIdle(t, svc, created.ID)
	if turns := runner.turns(); len(turns) != 2 {
		t.Fatalf("want the launch turn and the flush behind it, got %d", len(turns))
	}
}

// A stop that lands while the turn is still launching has no process to kill
// yet. It is kept: the launcher kills the process the moment it begins, and
// the answer settles as the stop it was.
func TestAStopInTheLaunchWindowStopsTheTurn(t *testing.T) {
	gate := make(chan struct{})
	runner := &fakeRunner{commandGate: gate, block: make(chan struct{}), events: []Event{{Kind: EventDelta, Text: "hi"}}}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := svc.Cancel(created.ID); err != nil {
		t.Fatalf("cancel during the launch: %v", err)
	}
	close(gate)
	msg := waitIdle(t, svc, created.ID)
	if msg.State != StateCancelled {
		t.Fatalf("want the launched turn settled as a stop, got %q", msg.State)
	}
}
