// Package detach runs a program that outlives the cockpit. The process is put
// into a session of its own and writes into files instead of pipes, so nothing
// it does depends on the server that started it: a restart, a self update or a
// crash leaves it running, and whoever comes next picks it up again.
//
// What ties the two processes together is a lock file, not a process number. An
// exclusive flock is taken before anything starts and travels into the child as
// an inherited descriptor, so there is no moment where the program runs and the
// lock is free, and the lock dies with the last process that holds it. Whether
// that file can be locked is therefore whether the program still runs. No start
// times, no process table, the same code on every unix.
//
// The program does not run directly: it runs under a hold process, a copy of
// this binary whose one job is to hold the lock, enforce the timeout and write
// down the exit code, because the exit code of a process this server did not
// start is lost to it (nobody can wait for a process that is not their child).
package detach

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// HoldArgs is the argv prefix that reaches Hold in the real binary. A test
// binary has no command tree in front of it and recognizes the same token
// itself, so the caller builds one argv and both binaries read it the same way.
var HoldArgs = []string{"run-detached"}

// pollInterval is how often Wait looks at a process this server did not start.
// Nothing depends on noticing the end of such a run in the same instant, and a
// lock check is a file open, so this is deliberately unhurried.
const pollInterval = 500 * time.Millisecond

// timeoutExitCode is what a run that was killed for taking too long reports.
// It is what coreutils' timeout uses, so the number reads the same here.
const timeoutExitCode = 124

// Options describe one detached run.
type Options struct {
	// Command is the program and its arguments, never a shell line. Whatever
	// travels in here stays an argument and can never become a command.
	Command []string
	// Dir is the working directory of the program.
	Dir string
	// Env replaces the environment of the program when it is set; nil inherits
	// this process's own.
	Env []string
	// Out is the file the program's standard output is written to. Err is the
	// one for standard error; leaving it empty puts both into Out, which is
	// what a caller asking for the combined output wants.
	Out string
	Err string
	// Lock is the file whose exclusive lock says the run is still going.
	Lock string
	// Result is where the hold process writes the exit code. Leaving it empty
	// means nobody asks about it, and the run reports through its output alone.
	Result string
	// Timeout bounds the run. It is enforced by the hold process, not by the
	// caller: the server that asked for the run may be gone long before the
	// run is, and a context here would die with it.
	Timeout time.Duration
}

// Process is a started run: what it takes to find it again, and how to tell
// whether it is still going.
type Process struct {
	pid  int
	lock string
	// exited is closed when this server reaped its own child. A run adopted
	// from an earlier server has no such channel and is watched through the
	// lock instead.
	exited chan struct{}
}

// Adopt is a run this server did not start, named by what an earlier one wrote
// down about it.
func Adopt(pid int, lock string) Process {
	return Process{pid: pid, lock: lock}
}

// PID is the hold process, the one that a kill signals and a self update reaps.
// Whether the run is still going is decided by the lock alone.
func (p Process) PID() int { return p.pid }

// Alive reports whether the run is still going.
func (p Process) Alive() bool {
	if p.exited != nil {
		select {
		case <-p.exited:
			return false
		default:
			return true
		}
	}
	return Alive(p.pid, p.lock)
}

// Kill ends the whole process group. A program spawns helpers, and killing only
// the leader would leave them writing into a file nobody reads.
func (p Process) Kill() { Kill(p.pid, p.lock) }

// Wait blocks until the run has ended. A run this server started is waited for,
// one it adopted is watched through its lock.
func (p Process) Wait() {
	if p.exited != nil {
		<-p.exited
		return
	}
	for p.Alive() {
		time.Sleep(pollInterval)
	}
}

// Start launches one run detached from this server: its own session, its output
// straight into a file, no pipe between the two. That is what lets it keep
// running while the cockpit restarts, and it is why nothing here may hold a
// handle the child depends on.
func Start(opts Options) (Process, error) {
	if len(opts.Command) == 0 || strings.TrimSpace(opts.Command[0]) == "" {
		return Process{}, errors.New("nothing to run")
	}
	// The hold process starting is not the program starting, so a program that
	// is not there is caught here, where the caller still hears about it.
	if _, err := exec.LookPath(opts.Command[0]); err != nil {
		return Process{}, fmt.Errorf("%s: %w", opts.Command[0], err)
	}
	self, err := selfExecutable()
	if err != nil {
		return Process{}, err
	}
	out, err := os.OpenFile(opts.Out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Process{}, err
	}
	defer out.Close()
	errFile := out
	if opts.Err != "" {
		errFile, err = os.OpenFile(opts.Err, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return Process{}, err
		}
		defer errFile.Close()
	}
	lock, err := takeLock(opts.Lock)
	if err != nil {
		return Process{}, err
	}
	// This server's own copy of the lock goes when Start returns. The hold
	// process keeps its inherited one, and with it the lock, until it is over.
	defer lock.Close()

	cmd := exec.Command(self, holdArgv(opts)...)
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env
	cmd.Stdout = out
	cmd.Stderr = errFile
	cmd.ExtraFiles = []*os.File{lock}
	if err := detach(cmd); err != nil {
		return Process{}, err
	}
	if err := cmd.Start(); err != nil {
		return Process{}, err
	}

	p := Process{pid: cmd.Process.Pid, lock: opts.Lock, exited: make(chan struct{})}
	// The child is reaped for as long as this server lives, so it never sits
	// around as a zombie that still looks alive. A server that goes away leaves
	// that to the init process, and whoever adopts the run reaps it instead.
	go func() {
		_ = cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

// holdArgv is the argv of the hold process: what it needs, then the program,
// separated so a program's own arguments can never be read as ours.
func holdArgv(opts Options) []string {
	argv := append([]string{}, HoldArgs...)
	if opts.Result != "" {
		argv = append(argv, "--result", opts.Result)
	}
	if opts.Timeout > 0 {
		argv = append(argv, "--timeout", opts.Timeout.String())
	}
	argv = append(argv, "--")
	return append(argv, opts.Command...)
}

// Hold runs the program named in args and waits it out. It is the process
// behind `dev-cockpit run-detached`, and its job is to hold the run's lock,
// inherited as an open descriptor, for as long as the program runs: the program
// cannot drop it by closing its own descriptors, because this process keeps its
// copy. Standard output and error are already the files of the run, wired up by
// the server before this process started, and the program's exit code is
// returned as this process's own.
func Hold(args []string) int {
	resultPath, timeout, argv, err := parseHoldArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run-detached:", err)
		writeResult(resultPath, 1)
		return 1
	}
	code := runHeld(argv, timeout, resultPath)
	// The result is written before this process ends, so it is on disk by the
	// time the lock is released, which is what a reader waits for. A timeout
	// already wrote it inside runHeld, before it took the group down; writing
	// the same code twice costs nothing.
	writeResult(resultPath, code)
	return code
}

func parseHoldArgs(args []string) (result string, timeout time.Duration, argv []string, err error) {
	for len(args) > 0 {
		switch {
		case args[0] == "--":
			args = args[1:]
			if len(args) == 0 {
				return result, timeout, nil, errors.New("nothing to run")
			}
			return result, timeout, args, nil
		case args[0] == "--result" && len(args) > 1:
			result, args = args[1], args[2:]
		case args[0] == "--timeout" && len(args) > 1:
			timeout, err = time.ParseDuration(args[1])
			if err != nil {
				return result, 0, nil, fmt.Errorf("--timeout %s: %w", args[1], err)
			}
			args = args[2:]
		default:
			// No separator: everything left is the program, the shape an older
			// caller uses.
			return result, timeout, args, nil
		}
	}
	return result, timeout, nil, errors.New("nothing to run")
}

// runHeld runs the program and answers its exit code. A timeout says so in the
// run's own error stream, where the reason lands next to the output it belongs
// to, and then ends the whole process group, this process included: the
// program's helpers inherited the lock, so ending only the program would leave
// the run reading as alive while nothing writes any more. The result goes to
// disk first, which is the order a reader relies on, and on unix a timeout
// therefore never returns from here.
func runHeld(argv []string, timeout time.Duration, resultPath string) int {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		// A program that could not be started at all says so into the run's
		// own error file, which is where a reader looks for the reason.
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if timeout > 0 {
		select {
		case err := <-done:
			return exitCode(err)
		case <-time.After(timeout):
			fmt.Fprintf(os.Stderr, "timed out after %s\n", timeout)
			writeResult(resultPath, timeoutExitCode)
			killOwnGroup()
			// Only a caller without a group of its own gets here, and it ends
			// what it can reach, the program alone.
			_ = cmd.Process.Kill()
			<-done
			return timeoutExitCode
		}
	}
	return exitCode(<-done)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		return exit.ExitCode()
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

func writeResult(path string, code int) {
	if path == "" {
		return
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(code)+"\n"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "run-detached: write result:", err)
	}
}

// Result reads back the exit code a finished run wrote down. A run whose hold
// process was killed outright never wrote one, and that is what the second
// return value says: the run ended, but not by its own decision.
func Result(path string) (int, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, false
	}
	return code, true
}

// TimedOut reports whether a code is the one a run killed for taking too long
// answers with.
func TimedOut(code int) bool { return code == timeoutExitCode }

// selfExecutable is the binary a hold process is started from. A binary
// replaced underneath a running process, a self update between the swap and
// the re-exec, reads back with a " (deleted)" marker that exec would refuse.
func selfExecutable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, " (deleted)"), nil
}
