package assistant

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// maxLineBytes bounds one structured output line. A provider packs a complete
// assistant message into a single JSON record, so the scanner needs far more
// than its default, but not an unbounded amount.
const maxLineBytes = 4 << 20

// stderrTailBytes is how much of a failing process's standard error is kept for
// the server log. It never reaches the browser.
const stderrTailBytes = 4 << 10

// pollInterval is how long the reader waits when the output file has nothing
// new. A provider writes in bursts, and a read that found something reads again
// at once, so this is the pause between bursts and not a delay on the answer.
const pollInterval = 40 * time.Millisecond

// Command is the process one turn runs: a program and its arguments, never a
// shell line. A prompt travels in the argv, so it can never become a command.
type Command struct {
	Name string
	Args []string
}

// eventBuffer is how many events a turn may run ahead of its reader. The
// service drains continuously; the buffer only absorbs a burst of deltas.
const eventBuffer = 64

// Parser turns one provider's output into events. Implemented next to each
// coder, because only they know the shape of their CLI's output.
type Parser interface {
	// Line consumes one structured output line. Returning an error ends the
	// turn with that error as the user facing message.
	Line(line []byte) error
	// Finish is called when the process ended, and reports what is wrong when
	// the record that closes a turn never arrived.
	Finish() error
	// Diagnose names the error of a failed turn out of everything this parser
	// saw, plus the end of standard error. Returning nil keeps err as it is.
	// It is called once, only for a turn that failed, and only after the
	// output was read to its end and the event channel is closed, so nothing
	// else touches the parser any more and there is no race on its state.
	// Every parser has to answer it, that is the point of it being part of the
	// interface: a new coder has to say what its CLI does when it never gets
	// going at all, which is where the exit code would have been the obvious
	// signal and is not available (it is not kept, and a turn adopted after a
	// restart has none left to get).
	Diagnose(err error, stderr string) error
}

// process is a started turn: what it takes to find it again, and how to tell
// whether it is still writing.
type process struct {
	// pid is the turn process, lock the file whose flock says whether the turn
	// still runs.
	pid  int
	lock string
	// exited is closed when this process reaped its own child. A turn adopted
	// from an earlier server has no such channel and is watched through the
	// lock instead, see alive.
	exited chan struct{}
}

// alive reports whether the provider is still writing.
func (p process) alive() bool {
	if p.exited != nil {
		select {
		case <-p.exited:
			return false
		default:
			return true
		}
	}
	return processAlive(p.pid, p.lock)
}

// kill ends the whole process group. A coder CLI spawns helpers, and killing
// only the leader would leave them writing into a file nobody reads.
func (p process) kill() { killProcess(p.pid, p.lock) }

// start launches one turn detached from this server: its own session, its
// standard output straight into a file, no pipe between the two. That is what
// lets the answer keep being written while the cockpit restarts, and it is why
// nothing here may hold a handle the child depends on.
//
// The provider runs under a turn process, a copy of this binary whose one job
// is to hold the turn's lock while the provider runs. The lock is taken here,
// before anything starts, and travels as an inherited descriptor, so there is
// no moment where the turn runs and the lock is free.
func start(c Command, workdir, source, outPath, errPath, lockPath string) (process, error) {
	if strings.TrimSpace(c.Name) == "" {
		return process{}, errors.New("The coder could not be started.")
	}
	// The turn process starting is not the provider starting, so a CLI that
	// is not there is caught here, where the caller still hears about it at
	// once.
	if _, err := exec.LookPath(c.Name); err != nil {
		return process{}, errors.New("The coder could not be started.")
	}
	self, err := selfExecutable()
	if err != nil {
		return process{}, errors.New("The coder could not be started.")
	}
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return process{}, errors.New("The coder could not be started.")
	}
	defer out.Close()
	errFile, err := os.OpenFile(errPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return process{}, errors.New("The coder could not be started.")
	}
	defer errFile.Close()
	lock, err := lockTurn(lockPath)
	if err != nil {
		return process{}, errors.New("The coder could not be started.")
	}
	// The server's own copy of the lock goes when start returns. The turn
	// process keeps its inherited one, and with it the lock, until it is over.
	defer lock.Close()

	argv := append([]string{}, runTurnArgs...)
	argv = append(argv, c.Name)
	argv = append(argv, c.Args...)
	cmd := exec.Command(self, argv...)
	cmd.Dir = workdir
	cmd.Stdout = out
	cmd.Stderr = errFile
	cmd.ExtraFiles = []*os.File{lock}
	// Where the message came from travels down the whole tree: this process,
	// the provider it runs, and the cockpit commands the provider calls from
	// its shell. That is how a job it starts knows where it was asked for.
	if source != "" {
		cmd.Env = append(os.Environ(), TurnSourceEnv+"="+source)
	}
	if err := detach(cmd); err != nil {
		return process{}, err
	}
	if err := cmd.Start(); err != nil {
		return process{}, errors.New("The coder could not be started.")
	}

	p := process{pid: cmd.Process.Pid, lock: lockPath, exited: make(chan struct{})}
	// The child is reaped for as long as this server lives, so it never sits
	// around as a zombie that still looks alive. A server that goes away leaves
	// that to the init process, and whoever adopts the turn reaps it instead.
	go func() {
		_ = cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

// LineHandler consumes one structured output line. Returning an error ends the
// turn with that error as the user facing message.
type LineHandler func(line []byte) error

// tail reads the raw output of a turn from the file the provider writes it to
// and hands every complete line to onLine. It returns when the process ended
// and the file is read to its end, so no output is lost between the last line
// and the exit, and it is the same code whether this server started that
// process or found it after a restart.
//
// Only complete lines are handed on: a record the provider is still writing is
// not a record yet. The count of bytes consumed goes to progress, which is what
// the register records as processed.
func tail(path string, alive func() bool, onLine LineHandler, progress func(int64)) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.New("The answer from the coder could not be read.")
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 64<<10)
	var (
		pending  []byte
		consumed int64
		drained  bool
	)
	for {
		chunk, err := reader.ReadBytes('\n')
		if len(chunk) > 0 {
			pending = append(pending, chunk...)
			if len(pending) > maxLineBytes {
				return errors.New("The answer from the coder could not be read.")
			}
		}
		if err == nil {
			consumed += int64(len(pending))
			line := bytes.TrimRight(pending, "\r\n")
			pending = pending[:0]
			drained = false
			if len(bytes.TrimSpace(line)) > 0 {
				if handlerErr := onLine(line); handlerErr != nil {
					return handlerErr
				}
			}
			if progress != nil {
				progress(consumed)
			}
			continue
		}
		if err != io.EOF {
			return errors.New("The answer from the coder could not be read.")
		}
		if !alive() {
			// The process is gone, so nothing can be written any more. One more
			// pass picks up whatever landed between the last read and the exit,
			// and the pass after that ends the loop.
			if drained {
				return nil
			}
			drained = true
			continue
		}
		time.Sleep(pollInterval)
	}
}

// stderrTail is the end of what a turn wrote to standard error. It reads the
// file only to pick between two curated sentences and for the server log,
// nothing from it is ever shown.
func stderrTail(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	size := info.Size()
	if size > stderrTailBytes {
		if _, err := file.Seek(size-stderrTailBytes, io.SeekStart); err != nil {
			return ""
		}
	}
	data, err := io.ReadAll(io.LimitReader(file, stderrTailBytes))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// loginPattern recognizes the one provider failure the user can do something
// about: the CLI has never been logged in on this machine.
var loginPattern = regexp.MustCompile(`(?i)(/login\b|please log ?in|not logged ?in|sign ?in|not authenticated|authentication (failed|required|error)|unauthorized|invalid api key)`)

// LooksLikeLogin reports whether text reads like a CLI that was never logged in.
// It is exported because every coder answers that question about its own output,
// and the pattern behind the answer stays one.
func LooksLikeLogin(text string) bool { return loginPattern.MatchString(text) }

// ErrNotLoggedIn is what a parser returns when its CLI never got going because
// nobody logged it in on this machine. The sentence lives here and not next to a
// coder on purpose: a parser answers whether it happened, this package owns what
// the user reads about it, so no CLI's own wording leaks into a conversation.
var ErrNotLoggedIn = errors.New("The coder is not logged in on this machine. Start it once in a terminal, log in there, and send this again.")
