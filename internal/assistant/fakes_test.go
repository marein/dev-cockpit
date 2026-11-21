package assistant

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
	env      []string
	exists   bool
	deleted  []string
	deleteFn func(string) error
	requests []TurnRequest
	// compactions are the requests the service handed CompactCommand.
	compactions []TurnRequest
	commandErr  error
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
	case EventCompact:
		return "C" + base64.StdEncoding.EncodeToString([]byte(ev.Compact.Outcome+"\n"+ev.Compact.Reason))
	case EventUsage:
		usage, _ := json.Marshal(ev.Usage)
		return "U" + base64.StdEncoding.EncodeToString(usage)
	case EventError:
		text := ""
		if ev.Err != nil {
			text = ev.Err.Error()
		}
		// A refusal travels as its kind and what the CLI named, the way a
		// real parser hands one over, so the service places it on the run.
		var refusal *Refusal
		if errors.As(ev.Err, &refusal) {
			return "F" + base64.StdEncoding.EncodeToString([]byte(string(refusal.Kind)+"\n"+refusal.Model))
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

// CompactCommand plays a compaction the way Command plays a turn, under the
// prompt a CLI compacts on, so an answer func tells the two apart.
func (r *fakeRunner) CompactCommand(req TurnRequest) (Command, error) {
	r.mu.Lock()
	r.compactions = append(r.compactions, req)
	r.mu.Unlock()
	req.Prompt = fakeCompactPrompt
	return r.Command(req)
}

const fakeCompactPrompt = "/compact"

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
	case 'C':
		outcome, reason, _ := strings.Cut(string(payload), "\n")
		p.events <- Event{Kind: EventCompact, Compact: &CompactReport{Outcome: outcome, Reason: reason}}
	case 'U':
		var usage ContextUsage
		if err := json.Unmarshal(payload, &usage); err != nil {
			return err
		}
		p.events <- Event{Kind: EventUsage, Usage: &usage}
	case 'E':
		p.events <- Event{Kind: EventError, Err: errors.New(string(payload))}
	case 'F':
		kind, model, _ := strings.Cut(string(payload), "\n")
		p.events <- Event{Kind: EventError, Err: &Refusal{Kind: RefusalKind(kind), Model: model}}
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

// fakeCoders is the one coder of the tests, with the model defaults the
// settings store would answer for it, read through CoderInfo.Defaults the way
// the real wiring reads them, so a test can set a default and watch what a
// turn does with it.
type fakeCoders struct {
	runner   *fakeRunner
	defaults ModelDefaults
}

func (c fakeCoders) Available() []CoderInfo {
	if c.runner == nil {
		return nil
	}
	return []CoderInfo{{ID: "claude", Label: "Claude", Runner: c.runner, Defaults: func() ModelDefaults { return c.defaults }}}
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
		reacting := 0
		for _, trigger := range svc.events.triggers.All() {
			if trigger.Reacting() {
				reacting++
			}
		}
		if len(open) == 0 && checking == 0 && reacting == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("the test left %d turn(s), %d check(s) and %d reaction(s) running", len(open), checking, reacting)
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
		return svc.RunningSince(id).IsZero() && lastMessage(t, svc, id).State.Settled()
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
