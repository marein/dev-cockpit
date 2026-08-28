package docker

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/detach"
	"github.com/marein/dev-cockpit/internal/statefile"
)

const (
	// composeOutputTail is how much of the run's output the completion
	// carries, enough to say why it failed without shipping a whole pull log.
	composeOutputTail = 2048
	// composeViewTail is how much of it the output view reads, the same idea
	// with a person in front of it instead of a notification line.
	composeViewTail = 256 << 10
)

// composeState tracks the compose runs in flight, one per directory, so a
// second run cannot race the first, and holds who hears about a run that ended.
// The map is this process's reading of the register on disk: it is filled by
// a run this process starts and rebuilt from the register at Recover.
type composeState struct {
	mu    sync.Mutex
	runs  map[string]bool
	waits map[string]chan struct{}
	done  func(run ComposeRun, err error, output string)
}

// ComposeRun is a finished compose run: which project it reported under, what
// it ran, and whether it failed.
type ComposeRun struct {
	ID     string
	Dir    string
	Label  string
	Action string
	Quiet  bool
	Failed bool
}

// ComposeOptions describe one compose run.
type ComposeOptions struct {
	// Dir is where the command runs, Label the name the run reports under.
	Dir   string
	Label string
	// Root is the project the stack belongs to, the ceiling a relatively named
	// program is searched up to.
	Root string
	// Action is the configured entry to run.
	Action Action
	// Quiet keeps a run that went through silent. The project deletion brings
	// the stacks down as a step of itself, where the row disappearing is the
	// word to the user and only a failure has something left to say.
	Quiet bool
}

// RunView is one compose run as a surface shows it: what it is, whether it is
// still going, and how it ended.
type RunView struct {
	ID        string
	Dir       string
	Project   string
	Action    string
	Command   string
	Running   bool
	StartedAt time.Time
	EndedAt   time.Time
	Exited    bool
	Exit      int
	Cancelled bool
	Failure   string
}

// LastComposeRun answers the newest finished run of one project, which is what
// a notification about that project is about. It is asked per project and not
// as one global "the last run", because two projects can finish in the same
// moment and each one's news has to name its own run.
func (s *Service) LastComposeRun(project string) (RunView, bool) {
	var newest RunView
	found := false
	for _, rec := range s.runs.List() {
		if !rec.Finished || rec.Label != project {
			continue
		}
		if !found || rec.EndedAt.After(newest.EndedAt) {
			newest, found = runView(rec), true
		}
	}
	return newest, found
}

// ComposeBusy reports whether a compose run is under way in dir.
func (s *Service) ComposeBusy(dir string) bool {
	s.compose.mu.Lock()
	defer s.compose.mu.Unlock()
	return s.compose.runs[dir]
}

// ComposeBusyUnder reports whether a compose run is under way in dir or
// anywhere below it. A project deletion asks this about the whole project: a
// run in a subdirectory has the same right not to have the ground pulled from
// under it, and a run that is past its last container still holds its claim
// while it tears the rest of the stack down, which is precisely the moment the
// container list has stopped naming it.
func (s *Service) ComposeBusyUnder(dir string) bool {
	if dir == "" {
		return false
	}
	root := filepath.Clean(dir)
	s.compose.mu.Lock()
	defer s.compose.mu.Unlock()
	for running := range s.compose.runs {
		if running == root || strings.HasPrefix(running, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// composeDeadlineGrace is how much past its own timeout a run is granted
// before a waiter gives it up for stuck: the hold process needs a moment to
// kill the run and write the end down.
const composeDeadlineGrace = time.Minute

// ComposeDeadline answers the latest moment the unfinished runs in dir or
// below it can still end on their own. Every run carries a timeout the hold
// process enforces, so its start plus that timeout plus a grace is as late as
// it gets, and a run still going past that is stuck. ok is false while nothing
// runs there, which is what lets a waiter tell a missing run from one that has
// all that time left.
func (s *Service) ComposeDeadline(dir string) (deadline time.Time, ok bool) {
	if dir == "" {
		return time.Time{}, false
	}
	root := filepath.Clean(dir)
	for _, rec := range s.runs.List() {
		if rec.Finished {
			continue
		}
		if rec.Dir != root && !strings.HasPrefix(rec.Dir, root+string(filepath.Separator)) {
			continue
		}
		until := rec.StartedAt.Add(rec.Timeout + composeDeadlineGrace)
		if !ok || until.After(deadline) {
			deadline, ok = until, true
		}
	}
	return deadline, ok
}

// OnComposeDone registers the one callback every finished run reports through,
// the ones this process started and the ones it found already running. Set it
// before Recover.
func (s *Service) OnComposeDone(fn func(run ComposeRun, err error, output string)) {
	s.compose.mu.Lock()
	s.compose.done = fn
	s.compose.mu.Unlock()
}

// RunCompose starts one configured action in a directory and returns the id of
// the run once it is under way. The run is detached (see internal/detach): it
// lives on when this server goes away, its output goes into a file of its own,
// and its lock is what says it is still going. The event stream shows the
// containers move while it runs; the word at the end reaches OnComposeDone. It
// refuses a directory that is already running one, a command that cannot be
// read, a missing CLI, and a cockpit without a reachable daemon.
func (s *Service) RunCompose(opts ComposeOptions) (string, error) {
	rec, proc, err := s.startCompose(opts)
	if err != nil {
		return "", err
	}
	go s.await(rec, proc)
	return rec.ID, nil
}

// startCompose registers the run and starts it, without waiting for it. It is
// separate from the waiting on purpose: the wait is what a server does while it
// is there, the register is what is left when it is not.
func (s *Service) startCompose(opts ComposeOptions) (ComposeRecord, detach.Process, error) {
	if !s.CLI() {
		return ComposeRecord{}, detach.Process{}, errors.New("the docker CLI is not installed")
	}
	state := s.State()
	if !state.Available {
		return ComposeRecord{}, detach.Process{}, errors.New("no reachable Docker host")
	}
	// The command is read before anything is claimed: a line nobody can split
	// and a program that is not there are the caller's mistake, not a run.
	argv, timeout, err := opts.Action.Resolve(opts.Dir, opts.Root)
	if err != nil {
		return ComposeRecord{}, detach.Process{}, err
	}
	id := statefile.NewID()
	if !s.claim(opts.Dir, id) {
		return ComposeRecord{}, detach.Process{}, errors.New("a compose run is already under way here")
	}
	out, lock, result, err := s.runs.Files(id)
	if err != nil {
		s.release(opts.Dir, id)
		return ComposeRecord{}, detach.Process{}, err
	}
	proc, err := detach.Start(detach.Options{
		Command: argv,
		Dir:     opts.Dir,
		Env:     append(os.Environ(), "DOCKER_HOST="+state.Host),
		Out:     out,
		Lock:    lock,
		Result:  result,
		// The timeout travels into the hold process: the server that asked for
		// the run may be long gone when it passes.
		Timeout: timeout,
	})
	if err != nil {
		s.release(opts.Dir, id)
		s.runs.Delete(id)
		return ComposeRecord{}, detach.Process{}, err
	}
	rec := ComposeRecord{
		ID:        id,
		Dir:       opts.Dir,
		Label:     opts.Label,
		Action:    opts.Action.Label,
		Argv:      argv,
		Timeout:   timeout,
		Quiet:     opts.Quiet,
		PID:       proc.PID(),
		StartedAt: time.Now().UTC(),
	}
	s.runs.Save(rec)
	return rec, proc, nil
}

// AwaitCompose blocks until the named run is over. A run this process knows
// nothing about is over as far as the caller is concerned, so it returns at
// once: the project deletion waits this way, and a deletion whose wait was cut
// short by a restart has no goroutine left to wait in anyway.
func (s *Service) AwaitCompose(id string) {
	s.compose.mu.Lock()
	wait, ok := s.compose.waits[id]
	s.compose.mu.Unlock()
	if !ok {
		return
	}
	<-wait
}

// CancelCompose ends a run that is still going. It goes at the hold process,
// never at this server: the run is detached, the server that asked for it may
// be gone, and the one thing that reaches it either way is its process group.
// The cancel is written down first, so the end reads as called off rather than
// as a run that stopped without saying why.
func (s *Service) CancelCompose(id string) error {
	rec, ok := s.runs.Get(id)
	if !ok {
		return errors.New("no such run")
	}
	if rec.Finished {
		return errors.New("the run is already over")
	}
	rec.Cancelled = true
	s.runs.Save(rec)
	_, lock, _ := s.runs.paths(rec.ID)
	detach.Kill(rec.PID, lock)
	return nil
}

// ComposeRunByID answers one run for a surface that shows it.
func (s *Service) ComposeRunByID(id string) (RunView, bool) {
	rec, ok := s.runs.Get(id)
	if !ok {
		return RunView{}, false
	}
	return runView(rec), true
}

// ComposeRunsForDir answers the runs of one stack directory, newest first, the
// running one included.
func (s *Service) ComposeRunsForDir(dir string) []RunView {
	if dir == "" {
		return nil
	}
	var out []RunView
	for _, rec := range s.runs.List() {
		if rec.Dir == dir {
			out = append(out, runView(rec))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// ComposeRunOutput reads what a run has written so far, the end of it when it grew
// past what anybody reads. It answers while the run goes and after it ended,
// which is the whole point of keeping the file.
func (s *Service) ComposeRunOutput(id string) string {
	if _, ok := s.runs.Get(id); !ok {
		return ""
	}
	out, _, _ := s.runs.paths(id)
	return readTail(out, composeViewTail)
}

func runView(rec ComposeRecord) RunView {
	return RunView{
		ID:        rec.ID,
		Dir:       rec.Dir,
		Project:   rec.Label,
		Action:    rec.Action,
		Command:   strings.Join(rec.Argv, " "),
		Running:   !rec.Finished,
		StartedAt: rec.StartedAt,
		EndedAt:   rec.EndedAt,
		Exited:    rec.Exited,
		Exit:      rec.Exit,
		Cancelled: rec.Cancelled,
		Failure:   rec.Failure,
	}
}

// Recover picks up the compose runs of an earlier server. What is still going
// gets its directory claimed and its busy mark back and is waited out; what
// finished while nobody was looking reports now, which is the notification the
// restart would otherwise have swallowed. A run that is over and was reported
// stays where it is, its output is what somebody still reads. Run it after
// OnComposeDone is set and before anything can start a run of its own.
func (s *Service) Recover() {
	for _, rec := range s.runs.List() {
		if rec.Finished {
			continue
		}
		_, lock, _ := s.runs.paths(rec.ID)
		if !detach.Alive(rec.PID, lock) {
			s.finish(rec)
			continue
		}
		if !s.claim(rec.Dir, rec.ID) {
			// Two entries for one directory cannot happen through RunCompose;
			// if it ever does, the second one is not left running unnoticed.
			log.Printf("docker: compose run %s in %s has no place any more", rec.ID, rec.Dir)
			s.finish(rec)
			continue
		}
		log.Printf("docker: %q in %s is still running as process %d, waiting for it",
			rec.Action, rec.Dir, rec.PID)
		go s.await(rec, detach.Adopt(rec.PID, lock))
	}
	s.runs.Sweep()
}

// claim takes the directory for one run and opens the channel its waiters
// block on. It is the whole refusal of a second run in the same place.
func (s *Service) claim(dir, id string) bool {
	s.compose.mu.Lock()
	defer s.compose.mu.Unlock()
	if s.compose.runs == nil {
		s.compose.runs = map[string]bool{}
		s.compose.waits = map[string]chan struct{}{}
	}
	if s.compose.runs[dir] {
		return false
	}
	s.compose.runs[dir] = true
	s.compose.waits[id] = make(chan struct{})
	return true
}

// release gives the directory back and lets every waiter go.
func (s *Service) release(dir, id string) {
	s.compose.mu.Lock()
	defer s.compose.mu.Unlock()
	delete(s.compose.runs, dir)
	if wait, ok := s.compose.waits[id]; ok {
		close(wait)
		delete(s.compose.waits, id)
	}
}

func (s *Service) await(rec ComposeRecord, proc detach.Process) {
	proc.Wait()
	s.finish(rec)
}

// finish reads what a run left behind, passes the word on and writes down how
// it ended. It is the one completion path: a run this server started and one it
// adopted end here alike.
func (s *Service) finish(rec ComposeRecord) {
	out, _, result := s.runs.paths(rec.ID)
	// The cancel is written on the entry by whoever called the run off, which
	// is not this goroutine's copy of it.
	if latest, ok := s.runs.Get(rec.ID); ok {
		rec.Cancelled = latest.Cancelled
	}
	code, exited := detach.Result(result)
	err := composeOutcome(code, exited, rec)
	output := tailOf(readTail(out, composeOutputTail), composeOutputTail)
	run := ComposeRun{ID: rec.ID, Dir: rec.Dir, Label: rec.Label, Action: rec.Action, Quiet: rec.Quiet, Failed: err != nil}
	s.compose.mu.Lock()
	done := s.compose.done
	s.compose.mu.Unlock()
	s.release(rec.Dir, rec.ID)
	rec.Finished = true
	rec.EndedAt = time.Now().UTC()
	rec.Exited, rec.Exit = exited, code
	if err != nil {
		rec.Failure = err.Error()
	}
	s.runs.Finish(rec)
	if done != nil {
		done(run, err, output)
	}
}

// composeOutcome turns what the hold process wrote down into the outcome. A
// run without a result never got to write one: it was called off, it was
// killed, or the machine went down under it, and either way it did not finish.
func composeOutcome(code int, exited bool, rec ComposeRecord) error {
	switch {
	case rec.Cancelled:
		return errors.New("the run was cancelled")
	case !exited:
		return errors.New("the run ended without a result")
	case detach.TimedOut(code):
		return fmt.Errorf("timed out after %s", rec.Timeout)
	case code != 0:
		return fmt.Errorf("exit status %d", code)
	}
	return nil
}

// readTail reads the end of a run's output, the part that says how it went. A
// compose up that pulled a dozen images writes far more than anybody reads.
func readTail(path string, max int) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	if size := info.Size(); size > int64(max) {
		if _, err := file.Seek(size-int64(max), io.SeekStart); err != nil {
			return ""
		}
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(max)))
	if err != nil {
		return ""
	}
	return string(data)
}

// CLI reports whether the docker CLI is on the PATH, which compose and the
// container shells need; the list, the actions and the logs go through the
// API and do not.
func (s *Service) CLI() bool {
	s.cliOnce.Do(func() {
		_, err := exec.LookPath("docker")
		s.cli = err == nil
	})
	return s.cli
}

// tailOf keeps the end of an output, where a failure says its reason.
func tailOf(output string, max int) string {
	output = strings.TrimSpace(output)
	if len(output) <= max {
		return output
	}
	return output[len(output)-max:]
}

// defaultSocketHost is the host nobody has to name; a shell command only
// carries DOCKER_HOST when the cockpit talks to something else.
const defaultSocketHost = "unix:///var/run/docker.sock"

// ExecCommand is the one line a shell types to get into a container:
// docker exec with a tty, bash when the image has it, sh otherwise.
func ExecCommand(host, container string) string {
	return dockerCLILine(host, "exec -it "+shellQuote(container)+
		" sh -c 'command -v bash >/dev/null 2>&1 && exec bash -il || exec sh -il'")
}

// LogsCommand is the line a shell types to follow a container's output; when
// the container stops, the follow ends and the shell prompt is back. The
// stream runs through this binary's own log formatter, and a filter narrows
// it to the matching lines plus their context.
func LogsCommand(host, container, filter string) string {
	return dockerCLILine(host, "logs -f --tail 200 "+shellQuote(container)) + logFormatterPipe(filter)
}

// ComposeLogsCommand is the same for a whole stack: every service of the
// compose project in one stream, run from the stack's own directory, which is
// how compose knows which project it is about.
func ComposeLogsCommand(host, filter string) string {
	return dockerCLILine(host, "compose logs -f --tail 200") + logFormatterPipe(filter)
}

// logFormatterPipe hands the log stream to the formatter of the binary that
// serves this cockpit, so the terminal needs nothing installed. The stderr
// merge stands before the pipe: the daemon keeps a container's two streams
// apart, and the error stream would otherwise land unformatted behind the
// pipeline. A binary that cannot name its own path leaves the logs plain
// rather than break the follow; the " (deleted)" marker is what a path reads
// back with while a self update has swapped the file underneath this process.
func logFormatterPipe(filter string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe = strings.TrimSuffix(exe, " (deleted)")
	pipe := " 2>&1 | " + shellQuote(exe) + " docker log-formatter"
	if filter != "" {
		pipe += " --grep " + shellQuote(filter)
	}
	return pipe
}

func dockerCLILine(host, rest string) string {
	line := "docker " + rest
	if host != "" && host != defaultSocketHost {
		line = "DOCKER_HOST=" + shellQuote(host) + " " + line
	}
	return line
}

// shellQuote wraps a value for a POSIX shell line. Container names stay
// plain, they carry no specials, but a host URL may.
func shellQuote(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n\"'`$\\!&|;<>()*?[]{}~#") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
