package coderlogin

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// flowTimeout bounds a whole login. The device codes the providers hand out
// expire in this order of time anyway, so past it the flow ends in a readable
// sentence instead of waiting on nobody.
const flowTimeout = 15 * time.Minute

// maxCapture bounds what the flow keeps of the process output. A login prints
// a handful of lines; the cap only exists so a misbehaving CLI cannot grow
// memory while it runs.
const maxCapture = 128 << 10

// maxCode bounds the pasted code. The real codes are short; the bound keeps a
// paste of the wrong clipboard from traveling anywhere.
const maxCode = 4 << 10

// FlowState is the browser's view of a flow: one word for where it stands and
// the values that phase shows.
//
// The states are: "starting" until the CLI printed what the user needs,
// "waiting" while the user acts (open the URL, paste the code, authorize the
// device), "checking" after a code went in and nothing complained yet, then
// exactly one of "done", "failed" and "cancelled".
type FlowState struct {
	State     string `json:"state"`
	URL       string `json:"url,omitempty"`
	Code      string `json:"code,omitempty"`
	TakesCode bool   `json:"takesCode"`
	// Note is the CLI's complaint about the last pasted code, shown next to
	// the field for the retry.
	Note string `json:"note,omitempty"`
	// Error is the failed state's words, the CLI's where it left any.
	Error string `json:"error,omitempty"`
}

// Flow is one running login process. Everything it holds dies with it; the
// pasted code is not on it at all, it goes straight into the child's stdin.
type Flow struct {
	login Login
	onEnd func()

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout []byte
	stderr []byte
	// answerMark is the stderr length when the last code went in: what the
	// CLI says after it is about that code, what it said before is not.
	answerMark int
	answered   bool
	exited     bool
	exitOK     bool
	exitWords  string
	endedAt    time.Time
	cancelled  bool
	timedOut   bool
	timer      *time.Timer
}

// flowSink appends one output stream to its buffer under the flow's lock. It
// always reports the full write, so a chatty child never blocks on the cap.
type flowSink struct {
	flow *Flow
	buf  *[]byte
}

func (w flowSink) Write(p []byte) (int, error) {
	w.flow.mu.Lock()
	defer w.flow.mu.Unlock()
	room := maxCapture - len(*w.buf)
	if room > 0 {
		if room > len(p) {
			room = len(p)
		}
		*w.buf = append(*w.buf, p[:room]...)
	}
	return len(p), nil
}

// startFlow runs the login command. The child gets a process group of its own,
// so cancelling takes a spawned browser opener along instead of orphaning it.
func startFlow(login Login, onEnd func()) (*Flow, error) {
	name, args := login.Command()
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	f := &Flow{login: login, onEnd: onEnd, cmd: cmd}
	cmd.Stdout = flowSink{flow: f, buf: &f.stdout}
	cmd.Stderr = flowSink{flow: f, buf: &f.stderr}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	f.stdin = stdin
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	f.timer = time.AfterFunc(flowTimeout, f.timeout)
	go f.wait()
	return f, nil
}

// wait picks the process up. cmd.Wait also waits for the output copies, so
// after it the buffers hold everything the process wrote.
func (f *Flow) wait() {
	err := f.cmd.Wait()
	f.timer.Stop()
	f.mu.Lock()
	f.exited = true
	f.exitOK = err == nil
	f.endedAt = time.Now()
	if err != nil && !f.cancelled && !f.timedOut {
		f.exitWords = f.failWords(err)
	}
	f.mu.Unlock()
	if err == nil {
		if completer, ok := f.login.(Completer); ok {
			completer.LoginCompleted()
		}
	}
	if f.onEnd != nil {
		f.onEnd()
	}
}

// failWords is what a failed login shows: the CLI's own last words where it
// left any, the bare exit otherwise.
func (f *Flow) failWords(err error) string {
	if words := LastLine(string(f.stderr)); words != "" {
		return words
	}
	if words := LastLine(string(f.stdout)); words != "" {
		return words
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Sprintf("The login ended with exit code %d.", exit.ExitCode())
	}
	return "The login ended unexpectedly."
}

// Snapshot answers where the flow stands right now.
func (f *Flow) Snapshot() FlowState {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := FlowState{TakesCode: f.login.TakesCode()}
	if f.exited {
		switch {
		case f.cancelled:
			state.State = "cancelled"
		case f.timedOut:
			state.State = "failed"
			state.Error = "The login took too long and was ended."
		case f.exitOK:
			state.State = "done"
		default:
			state.State = "failed"
			state.Error = f.exitWords
		}
		return state
	}
	reading := f.login.Read(string(f.stdout), string(f.stderr[f.answerMark:]))
	state.URL = reading.URL
	state.Code = reading.Code
	if reading.URL == "" && reading.Code == "" {
		state.State = "starting"
		return state
	}
	if f.login.TakesCode() && f.answered && reading.Note == "" {
		state.State = "checking"
		return state
	}
	state.State = "waiting"
	if f.answered {
		state.Note = reading.Note
	}
	return state
}

// Answer writes the pasted code into the waiting process, followed by the
// newline that submits it. The code lives in the pipe and nowhere else.
func (f *Flow) Answer(code string) error {
	code = strings.TrimSpace(code)
	code = strings.NewReplacer("\r", "", "\n", "").Replace(code)
	if code == "" {
		return errors.New("The code is empty.")
	}
	if len(code) > maxCode {
		return errors.New("That is too long to be a login code.")
	}
	f.mu.Lock()
	if !f.login.TakesCode() {
		f.mu.Unlock()
		return errors.New("This login takes no code, it finishes on its own.")
	}
	if f.exited {
		f.mu.Unlock()
		return errors.New("The login is not waiting for a code any more.")
	}
	reading := f.login.Read(string(f.stdout), string(f.stderr[f.answerMark:]))
	if !reading.Waiting {
		f.mu.Unlock()
		return errors.New("The login is not waiting for a code right now.")
	}
	if f.answered && reading.Note == "" {
		f.mu.Unlock()
		return errors.New("A code is already being checked.")
	}
	f.answered = true
	f.answerMark = len(f.stderr)
	stdin := f.stdin
	f.mu.Unlock()
	if _, err := io.WriteString(stdin, code+"\n"); err != nil {
		return errors.New("The code could not reach the login process.")
	}
	return nil
}

// Cancel ends the flow by killing the child's process group.
func (f *Flow) Cancel() {
	f.mu.Lock()
	if f.exited {
		f.mu.Unlock()
		return
	}
	f.cancelled = true
	f.mu.Unlock()
	f.kill()
}

func (f *Flow) timeout() {
	f.mu.Lock()
	if f.exited {
		f.mu.Unlock()
		return
	}
	f.timedOut = true
	f.mu.Unlock()
	f.kill()
}

func (f *Flow) kill() {
	if f.cmd.Process != nil {
		_ = syscall.Kill(-f.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// Ended reports whether the process is gone, and since when.
func (f *Flow) Ended() (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.endedAt, f.exited
}
