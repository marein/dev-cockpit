package askpass

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// bridge starts a broker on a socket of its own and serves it for one test.
func bridge(t *testing.T) *Broker {
	t.Helper()
	dir := t.TempDir()
	b := New(dir)
	listener, err := Listen(dir)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() { _ = http.Serve(listener, b.Handler()) }()
	return b
}

// waitQuestion polls the way the browser does, bounded for the test.
func waitQuestion(t *testing.T, a *Action) *Question {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if q := a.Question(); q != nil {
			return q
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no question arrived")
	return nil
}

// The round trip the whole bridge exists for: a fake helper asks, the
// browser side sees the prompt line, answers, and the helper receives
// exactly that answer.
func TestAskRoundTripDeliversTheAnswer(t *testing.T) {
	b := bridge(t)
	a := b.Begin("demo", "push")
	if a == nil {
		t.Fatal("begin refused a fresh project")
	}
	defer a.End()

	got := make(chan string, 1)
	fail := make(chan error, 1)
	go func() {
		answer, err := Ask(b.socketPath, a.token, "Enter passphrase for key '/root/.ssh/id_ed25519':")
		if err != nil {
			fail <- err
			return
		}
		got <- answer
	}()

	q := waitQuestion(t, a)
	if q.Prompt != "Enter passphrase for key '/root/.ssh/id_ed25519':" {
		t.Fatalf("the prompt line got lost: %q", q.Prompt)
	}
	if q.Project != "demo" || q.Action != "push" {
		t.Fatalf("the question does not say whose it is: %+v", q)
	}
	select {
	case <-a.Asked():
	default:
		t.Fatal("the asked signal never fired")
	}
	if !a.Answer(q.ID, "s3cret", false) {
		t.Fatal("the answer was not accepted")
	}
	select {
	case answer := <-got:
		if answer != "s3cret" {
			t.Fatalf("the helper received %q", answer)
		}
	case err := <-fail:
		t.Fatalf("ask: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the helper never got its answer")
	}
	if a.Cancelled() {
		t.Fatal("a delivered answer is no cancellation")
	}
}

// The negative side: cancel denies the helper, marks the action, and an
// ended action denies a late asker instead of blocking it.
func TestCancelAndEndDenyTheHelper(t *testing.T) {
	b := bridge(t)
	a := b.Begin("demo", "push")
	defer a.End()

	fail := make(chan error, 1)
	go func() {
		_, err := Ask(b.socketPath, a.token, "Password for 'https://example.test':")
		fail <- err
	}()
	q := waitQuestion(t, a)
	if !a.Answer(q.ID, "", true) {
		t.Fatal("the cancel was not accepted")
	}
	select {
	case err := <-fail:
		if err == nil {
			t.Fatal("a cancelled question must deny the helper")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the helper never heard about the cancel")
	}
	if !a.Cancelled() {
		t.Fatal("the cancel left no mark")
	}

	done := b.Begin("other", "fetch")
	done.End()
	if _, err := Ask(b.socketPath, done.token, "anything"); err == nil {
		t.Fatal("an ended action must deny its helper")
	}
}

// The narrow window between the two: the /ask handler resolves the token,
// then End runs and reads pending as nil because the question is not parked
// yet, and only afterwards does ask park it. Nobody is left to answer that
// one, so ask has to hear the end itself; waiting on the reply alone left the
// helper blocked until its own budget ran out, minutes past the action.
func TestAskIsDeniedWhenTheActionEndsWhileTheQuestionIsParked(t *testing.T) {
	b := bridge(t)
	a := b.Begin("demo", "push")

	// End's first two sections, stopping where it stands when ask takes the
	// lock: the maps are cleared and pending was read as nil.
	a.broker.mu.Lock()
	delete(a.broker.byToken, a.token)
	delete(a.broker.byProject, a.project)
	a.broker.mu.Unlock()
	a.mu.Lock()
	a.pending = nil
	a.mu.Unlock()

	denied := make(chan bool, 1)
	go func() {
		_, ok := a.ask("Enter passphrase for key '/root/.ssh/id_ed25519':")
		denied <- ok
	}()
	// Give the question time to park, then finish End.
	time.Sleep(100 * time.Millisecond)
	close(a.done)

	select {
	case ok := <-denied:
		if ok {
			t.Fatal("a question nobody can answer must be denied, not granted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ask blocked past the action it belonged to")
	}
}

// The socket path is derived from a resolved state directory, because it
// travels into a git process whose working directory is the project: a
// relative path would be read against the wrong directory there, and every
// helper would miss the socket without saying why. The length that decides
// whether the address fits is measured on the resolved path for the same
// reason.
func TestSocketPathResolvesTheStateDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	relative := SocketPath("./state")
	absolute := SocketPath(filepath.Join(dir, "state"))

	if !filepath.IsAbs(relative) {
		t.Fatalf("the socket path must be absolute, got %q", relative)
	}
	if relative != absolute {
		t.Fatalf("two spellings of one directory gave two sockets: %q and %q", relative, absolute)
	}

	// A directory whose absolute path is too long falls back to the temp
	// directory. Measured on the short spelling it would not, and net.Listen
	// would then refuse the address instead.
	long := filepath.Join(dir, strings.Repeat("d", 120))
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(long)
	if got := SocketPath("."); !strings.HasPrefix(got, os.TempDir()) {
		t.Fatalf("a path too long for an address must fall back to the temp directory, got %q", got)
	}
}

// The stub is written into a directory that may not exist yet: whether the
// socket was opened first is the start's order and not a rule this may hang
// on, so WriteScript runs here without a Listen in front of it.
func TestWriteScriptMakesItsOwnDirectory(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")

	path, err := WriteScript(state)
	if err != nil {
		t.Fatalf("write script: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the stub was not written: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("the stub is %v", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat the directory: %v", err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Fatalf("the directory carries %v, the socket's permission is 0700", dir.Mode().Perm())
	}
}

// The binary's path is whatever this process was installed as, and a shell
// reads a quote, a dollar, a backtick or a backslash in it as syntax. The
// proof is not that a script exists, it is that running it starts exactly
// that path: the stand-in below writes down the path it was started as and
// the arguments it received.
func TestHelperScriptRunsAPathAShellWouldTakeApart(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, `dev "cockpit" $HOME `+"`id`"+` back\slash it's`)
	record := filepath.Join(dir, "record")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$0\" \"$@\" > "+record+"\n"), 0o700); err != nil {
		t.Fatalf("write the stand-in: %v", err)
	}
	script := filepath.Join(dir, "helper")
	if err := os.WriteFile(script, []byte(helperScript(binary)), 0o700); err != nil {
		t.Fatalf("write the stub: %v", err)
	}

	out, err := exec.Command("sh", script, "Enter passphrase for key 'id_ed25519':").CombinedOutput()
	if err != nil {
		t.Fatalf("run the stub: %v: %s", err, out)
	}

	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the stub started nothing: %v (%s)\n%s", err, out, helperScript(binary))
	}
	want := binary + "\naskpass\nEnter passphrase for key 'id_ed25519':\n"
	if string(got) != want {
		t.Fatalf("the stub ran\n%q\ninstead of\n%q", got, want)
	}
}

// The prompt line is the one value nobody in this process chose. It is bounded
// before it is parked, so neither the action's memory nor the dialog has to
// carry whatever a hook or a remote decided to write.
func TestAskBoundsThePromptLine(t *testing.T) {
	b := bridge(t)
	a := b.Begin("demo", "push")
	defer a.End()

	go func() { _, _ = Ask(b.socketPath, a.token, strings.Repeat("x", 4000)) }()

	q := waitQuestion(t, a)
	if got := len([]rune(q.Prompt)); got != maxPrompt+1 {
		t.Fatalf("the prompt was parked with %d runes", got)
	}
	if !strings.HasSuffix(q.Prompt, "…") {
		t.Fatalf("a cut line has to say that it was cut: %q", q.Prompt)
	}

	// Past the body bound nothing is parked at all: the helper is refused
	// before anything of it reaches the action.
	if _, err := Ask(b.socketPath, a.token, strings.Repeat("y", maxAskBody+1)); err == nil {
		t.Fatal("a body past the bound must be refused")
	}
}

// End is what every handler defers, on paths that may already have ended the
// action themselves, so ending twice is the same as ending once.
func TestEndTwiceIsTheSameAsOnce(t *testing.T) {
	b := bridge(t)
	a := b.Begin("demo", "push")
	a.End()
	a.End()
	if b.Find("demo") != nil {
		t.Fatal("an ended action must be gone from the maps")
	}
	if b.Begin("demo", "pull") == nil {
		t.Fatal("an ended action must free its project")
	}
}

// The doors: a helper without the right token is denied, and one project
// runs one bridged action. The collision is unreachable behind the write
// lock, but a second action under the same name would answer questions to
// the wrong caller, so it may never happen silently.
func TestTokensGateAndAProjectRunsOneAction(t *testing.T) {
	b := bridge(t)
	a := b.Begin("demo", "push")
	defer a.End()

	if _, err := Ask(b.socketPath, "wrong-token", "anything"); err == nil {
		t.Fatal("a wrong token must be denied")
	}
	if b.Find("other") != nil {
		t.Fatal("a project without an action must answer nothing")
	}
	if b.Find("demo") != a {
		t.Fatal("the project must find its action")
	}
	if b.Begin("demo", "pull") != nil {
		t.Fatal("a project already running an action must be refused")
	}
}

// The standing questions are one list, oldest first: that order is the queue
// the global dialog serves, and every move of the list fires the change hook
// the gitprompt event hangs on. The park, the answer and the end that takes a
// question along each count as a move; an end without a standing question
// moves nothing visible and stays silent.
func TestQuestionsListAndChangeHook(t *testing.T) {
	b := bridge(t)
	var moved atomic.Int32
	b.OnChange = func() { moved.Add(1) }
	// The park's change fires in the helper's goroutine a moment after the
	// question is visible, so counting it has to wait rather than glance.
	waitMoves := func(want int32) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for moved.Load() != want && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if got := moved.Load(); got != want {
			t.Fatalf("the change hook fired %d times, not %d", got, want)
		}
	}

	first := b.Begin("demo", "push")
	defer first.End()
	second := b.Begin("other", "clone")
	defer second.End()
	if got := len(b.Questions()); got != 0 {
		t.Fatalf("no question was asked yet, the list holds %d", got)
	}

	go func() { _, _ = Ask(b.socketPath, first.token, "Enter passphrase for key '/zz/one':") }()
	q1 := waitQuestion(t, first)
	go func() { _, _ = Ask(b.socketPath, second.token, "Enter passphrase for key '/zz/two':") }()
	q2 := waitQuestion(t, second)

	questions := b.Questions()
	if len(questions) != 2 {
		t.Fatalf("two standing questions, the list holds %d", len(questions))
	}
	if questions[0].ID != q1.ID || questions[1].ID != q2.ID {
		t.Fatalf("the list is not in park order: %+v", questions)
	}
	if questions[1].Project != "other" || questions[1].Action != "clone" {
		t.Fatalf("the second question does not say whose it is: %+v", questions[1])
	}
	waitMoves(2)

	if !first.Answer(q1.ID, "opensesame", false) {
		t.Fatal("the answer was not accepted")
	}
	if remaining := b.Questions(); len(remaining) != 1 || remaining[0].ID != q2.ID {
		t.Fatalf("the answered question did not leave the list: %+v", remaining)
	}
	waitMoves(3)

	second.End()
	if remaining := b.Questions(); len(remaining) != 0 {
		t.Fatalf("the ended action left its question standing: %+v", remaining)
	}
	waitMoves(4)

	first.End()
	if got := moved.Load(); got != 4 {
		t.Fatalf("an end without a question fired a change: %d", got)
	}
}
