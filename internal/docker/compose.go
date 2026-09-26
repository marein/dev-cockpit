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
// it ran, who asked for it, and how it ended. Failed covers every way a run
// did not go through; Declined narrows it to a parked run that never started.
type ComposeRun struct {
	ID     string
	Dir    string
	Label  string
	Action string
	Quiet  bool
	Owner  string
	Failed bool
	// Declined, Exited and Exit say how it ended, the way the view says it:
	// a declined run has no exit code, and a run without one did not finish
	// by its own decision.
	Declined bool
	Exited   bool
	Exit     int
	// ByUser says the user ended the run themselves, a Deny in the dialog or
	// a Cancel on the run page, of a parked run and a running one alike: the
	// end is written down and reported like every other, but it is no news
	// to the one who caused it.
	ByUser bool
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
	// Owner is the assistant that asked for the run, empty for a person. It
	// decides who hears the word at the end: an owned run is silent to the
	// user and reports into that assistant's thread instead, see the web
	// layer's composeDone.
	Owner string
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
	// Owner is the assistant that asked, empty for a person; Pending says the
	// run waits for the user's approval and nothing runs yet; Declined says
	// it never did, with Failure saying why.
	Owner    string
	Pending  bool
	Declined bool
}

// LastComposeRun answers the newest finished run of one project that a
// person started, which is what a notification about that project is about.
// It is asked per project and not as one global "the last run", because two
// projects can finish in the same moment and each one's news has to name its
// own run. An assistant's run is left out: it never writes that notification,
// its word goes into the assistant's thread, so counting it here would put an
// assistant's outcome under the user's own run.
func (s *Service) LastComposeRun(project string) (RunView, bool) {
	var newest RunView
	found := false
	for _, rec := range s.runs.List() {
		if !rec.Finished || rec.Label != project || rec.Owner != "" {
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
		if rec.Finished || rec.Pending {
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
//
// The launch runs under the pending lock like an approval's does. The entry
// exists from markLaunching on, before the process has a number, and a cancel
// in that gap would write its mark onto an entry markLaunched then saves over
// with its own copy: the cancel would answer yes and the run go on. Under the
// lock a cancel comes first and finds no run, or after and finds its process.
func (s *Service) startCompose(opts ComposeOptions) (ComposeRecord, detach.Process, error) {
	rec, err := s.prepareCompose(opts)
	if err != nil {
		return ComposeRecord{}, detach.Process{}, err
	}
	s.pending.Lock()
	defer s.pending.Unlock()
	return s.launchCompose(rec, false)
}

// prepareCompose reads everything a run needs before anything is claimed or
// started: the CLI, the daemon, the command. A line nobody can split and a
// program that is not there are the caller's mistake, not a run, and they are
// refused here whether the run starts now or waits for an approval first.
func (s *Service) prepareCompose(opts ComposeOptions) (ComposeRecord, error) {
	if err := s.composeReady(); err != nil {
		return ComposeRecord{}, err
	}
	argv, timeout, err := opts.Action.Resolve(opts.Dir, opts.Root)
	if err != nil {
		return ComposeRecord{}, err
	}
	return ComposeRecord{
		ID:        statefile.NewID(),
		Dir:       opts.Dir,
		Label:     opts.Label,
		Action:    opts.Action.Label,
		Argv:      argv,
		Timeout:   timeout,
		Quiet:     opts.Quiet,
		Owner:     opts.Owner,
		StartedAt: time.Now().UTC(),
	}, nil
}

// composeReady says whether a run can start right now: the CLI is there and
// the daemon answers. It is asked when a run is prepared and again when a
// parked one is approved, which may be half an hour later.
func (s *Service) composeReady() error {
	if !s.CLI() {
		return errors.New("the docker CLI is not installed")
	}
	if !s.State().Available {
		return errors.New("no reachable Docker host")
	}
	return nil
}

// launchCompose claims the directory and starts the prepared run. A run that
// was parked is launched from its stored entry, so what runs is exactly what
// the approval showed. A start that fails takes a fresh entry out of the
// register again, it was never a run; a parked one stays, because it is one
// the owner has to hear about, and its caller closes it with the reason.
//
// The start is written down on both sides of it: markLaunching before the
// process exists, markLaunched once its number is known. A restart in between
// finds Launching on the entry and Recover asks the run's files what happened,
// so a run an approval started is never read as one still waiting for it.
func (s *Service) launchCompose(rec ComposeRecord, parked bool) (ComposeRecord, detach.Process, error) {
	if !s.claim(rec.Dir, rec.ID) {
		return ComposeRecord{}, detach.Process{}, errors.New("a compose run is already under way here")
	}
	launching := s.markLaunching(rec)
	proc, err := s.spawn(launching)
	if err != nil {
		s.release(rec.Dir, rec.ID)
		if parked {
			s.runs.Save(rec)
		} else {
			s.runs.Delete(rec.ID)
		}
		return ComposeRecord{}, detach.Process{}, err
	}
	return s.markLaunched(launching, proc.PID()), proc, nil
}

// markLaunching writes down that the run is about to start, before anything
// is started.
func (s *Service) markLaunching(rec ComposeRecord) ComposeRecord {
	rec.Launching = true
	rec.StartedAt = time.Now().UTC()
	s.runs.Save(rec)
	return rec
}

// spawn starts the hold process of a run over the run's own files. The hold
// process writes nothing into the register, what it leaves are its files: the
// lock with its process number, and the result at the end.
func (s *Service) spawn(rec ComposeRecord) (detach.Process, error) {
	out, lock, result, err := s.runs.Files(rec.ID)
	if err != nil {
		return detach.Process{}, err
	}
	return detach.Start(detach.Options{
		Command: rec.Argv,
		Dir:     rec.Dir,
		Env:     append(os.Environ(), "DOCKER_HOST="+s.State().Host),
		Out:     out,
		Lock:    lock,
		Result:  result,
		// The timeout travels into the hold process: the server that asked for
		// the run may be long gone when it passes.
		Timeout: rec.Timeout,
	})
}

// markLaunched writes down the process a run runs as. From here on the entry
// is a running run like any other.
func (s *Service) markLaunched(rec ComposeRecord, pid int) ComposeRecord {
	rec.Pending = false
	rec.Launching = false
	rec.PID = pid
	s.runs.Save(rec)
	return rec
}

// MaxPendingPerOwner bounds how many parked runs one assistant may have
// standing at once. Every one of them is a question on every page and on the
// phone, and a handful is already more than a person answers in a row; the
// bound is what keeps a turn in a loop from burying the user in them.
const MaxPendingPerOwner = 5

// ParkCompose registers a run that waits for the user's approval and answers
// its id at once. The command is resolved and refused the way RunCompose
// refuses it, so a parked run is one that can start; nothing runs, no
// directory is held, and the entry survives a restart like every other one.
// ApproveCompose starts it, DeclineCompose ends it, and a process that starts
// over a parked entry declines it, because the question it waited for lived in
// the process that is gone. A directory a run is already under way in refuses
// the way a direct start refuses it, because an approval there could only end
// in that refusal; one owner parks at most one run per directory, a second
// would only fail at its approval, and at most MaxPendingPerOwner in all.
func (s *Service) ParkCompose(opts ComposeOptions) (string, error) {
	rec, err := s.prepareCompose(opts)
	if err != nil {
		return "", err
	}
	s.pending.Lock()
	defer s.pending.Unlock()
	if s.ComposeBusy(rec.Dir) {
		return "", errors.New("a compose run is already under way here")
	}
	standing := 0
	for _, other := range s.runs.List() {
		if other.Finished || !other.Pending || other.Owner != rec.Owner {
			continue
		}
		if other.Dir == rec.Dir {
			return "", errors.New("a run here already waits for the user's approval")
		}
		standing++
	}
	if standing >= MaxPendingPerOwner {
		return "", fmt.Errorf("%d runs already wait for the user's approval", standing)
	}
	rec.Pending = true
	s.runs.Save(rec)
	return rec.ID, nil
}

// ApproveCompose starts a parked run. What the approval showed is what runs:
// the stored argv and timeout, not a second resolution of the action. A start
// that fails, the docker CLI or the daemon gone since the run was parked or
// the directory taken by a run that started meanwhile, ends the run with that
// reason through the one completion path, so the owner hears about it the way
// it hears about every end. The move out of Pending and the launch happen
// under one lock, so a cancel or a decline racing it either comes first and
// the approval is refused, or comes after and finds a run with a process.
func (s *Service) ApproveCompose(id string) error {
	s.pending.Lock()
	rec, err := s.parked(id)
	if err != nil {
		s.pending.Unlock()
		return err
	}
	if err := s.composeReady(); err != nil {
		run := s.closeUnstarted(rec, err, false)
		s.pending.Unlock()
		s.reportUnstarted(run, err)
		return err
	}
	launched, proc, err := s.launchCompose(rec, true)
	if err != nil {
		run := s.closeUnstarted(rec, err, false)
		s.pending.Unlock()
		s.reportUnstarted(run, err)
		return err
	}
	s.pending.Unlock()
	go s.await(launched, proc)
	return nil
}

// DeclineCompose ends a parked run without starting it, with the sentence that
// says why: denied, unanswered, or lost in a restart. byUser marks a Deny the
// user gave, see ComposeRun.ByUser.
func (s *Service) DeclineCompose(id, reason string, byUser bool) error {
	s.pending.Lock()
	rec, err := s.parked(id)
	if err != nil {
		s.pending.Unlock()
		return err
	}
	cause := errors.New(reason)
	run := s.closeUnstarted(rec, cause, true)
	run.ByUser = byUser
	s.pending.Unlock()
	s.reportUnstarted(run, cause)
	return nil
}

// WithdrawCompose ends a parked run declined without reporting it. It is for
// a park whose refusal the caller answers itself, a question that could not
// be asked: the command that parked it reads why, and a note about a run it
// was just told did not start would say the same thing twice.
func (s *Service) WithdrawCompose(id, reason string) error {
	s.pending.Lock()
	defer s.pending.Unlock()
	rec, err := s.parked(id)
	if err != nil {
		return err
	}
	s.closeUnstarted(rec, errors.New(reason), true)
	return nil
}

// DeclinePending ends every parked run match picks, with one reason, and
// answers their ids so the caller can take their questions down. It is what a
// deleted assistant and a deleted project do to the runs that still wait for
// them: approving one later would start a command nobody can hear about, or in
// a directory that is gone.
func (s *Service) DeclinePending(match func(RunView) bool, reason string) []string {
	s.pending.Lock()
	var closed []ComposeRun
	cause := errors.New(reason)
	for _, rec := range s.runs.List() {
		if rec.Finished || !rec.Pending || !match(runView(rec)) {
			continue
		}
		closed = append(closed, s.closeUnstarted(rec, cause, true))
	}
	s.pending.Unlock()
	ids := make([]string, 0, len(closed))
	for _, run := range closed {
		s.reportUnstarted(run, cause)
		ids = append(ids, run.ID)
	}
	return ids
}

// Disown hands the running runs of an owner that is going away to the user
// and answers their ids. Their end then takes the path a run the user started
// takes, the project's notification and LastComposeRun, instead of a report
// into a thread that is gone, which would end the run in silence. Parked runs
// are not touched, DeclinePending ends those, since nobody started them.
func (s *Service) Disown(owner string) []string {
	if owner == "" {
		return nil
	}
	s.pending.Lock()
	defer s.pending.Unlock()
	var ids []string
	for _, rec := range s.runs.List() {
		if rec.Finished || rec.Pending || rec.Owner != owner {
			continue
		}
		rec.Owner = ""
		s.runs.Save(rec)
		ids = append(ids, rec.ID)
	}
	return ids
}

// DisownRun hands one finished run to the user, the counterpart of Disown for
// a run whose owner was already gone when it ended: a delete that raced its
// end, or a run registered under an owner deleted right before. The report
// had nowhere to go, so the run takes the user's path after the fact, and
// LastComposeRun names it for the project's notification. It answers whether
// the entry was an owned run that is over.
func (s *Service) DisownRun(id string) bool {
	s.pending.Lock()
	defer s.pending.Unlock()
	rec, ok := s.runs.Get(id)
	if !ok || !rec.Finished || rec.Owner == "" {
		return false
	}
	rec.Owner = ""
	s.runs.Save(rec)
	return true
}

// parked answers a run that still waits for its approval and refuses every
// other entry: one that runs, one that is over, one nobody knows. It reads the
// register fresh and is only asked under the pending lock.
func (s *Service) parked(id string) (ComposeRecord, error) {
	rec, ok := s.runs.Get(id)
	if !ok {
		return ComposeRecord{}, errors.New("no such run")
	}
	if rec.Finished || !rec.Pending {
		return ComposeRecord{}, errors.New("the run is not waiting for an approval")
	}
	return rec, nil
}

// closeUnstarted writes a run that never had a process down as over, with its
// reason, and answers what its report carries. It has no output and no exit
// code, and it is not the same as a run that ended without a result: nothing
// was ever started. The report goes out through reportUnstarted, after the
// pending lock is let go, so the callback never runs under it.
func (s *Service) closeUnstarted(rec ComposeRecord, err error, declined bool) ComposeRun {
	rec.Pending = false
	rec.Launching = false
	rec.Finished = true
	rec.Declined = declined
	rec.EndedAt = time.Now().UTC()
	rec.Failure = err.Error()
	s.runs.Finish(rec)
	return ComposeRun{ID: rec.ID, Dir: rec.Dir, Label: rec.Label, Action: rec.Action, Quiet: rec.Quiet, Owner: rec.Owner, Failed: true, Declined: declined}
}

// reportUnstarted passes the end of a run closeUnstarted wrote down to the one
// completion callback, so a declined run reaches its owner the way a failed
// one does.
func (s *Service) reportUnstarted(run ComposeRun, err error) {
	s.compose.mu.Lock()
	done := s.compose.done
	s.compose.mu.Unlock()
	if done != nil {
		done(run, err, "")
	}
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
// as a run that stopped without saying why. It reads the entry under the
// pending lock, so a parked run it declines cannot be started by an approval
// in the same moment, and a run an approval is launching is found with its
// process. byUser marks a cancel the user gave, which for a parked run is
// the same click as a Deny and for a running one is written on the entry for
// the end to read, see ComposeRun.ByUser.
func (s *Service) CancelCompose(id string, byUser bool) error {
	s.pending.Lock()
	rec, ok := s.runs.Get(id)
	if !ok {
		s.pending.Unlock()
		return errors.New("no such run")
	}
	if rec.Finished {
		s.pending.Unlock()
		return errors.New("the run is already over")
	}
	if rec.Pending {
		// Nothing runs yet, so there is nothing to kill: calling a parked run
		// off is declining it, and it reads as cancelled like a running one.
		rec.Cancelled = true
		cause := errors.New("the run was cancelled")
		run := s.closeUnstarted(rec, cause, true)
		run.ByUser = byUser
		s.pending.Unlock()
		s.reportUnstarted(run, cause)
		return nil
	}
	rec.Cancelled = true
	rec.CancelledByUser = byUser
	s.runs.Save(rec)
	s.pending.Unlock()
	_, lock, _ := s.runs.paths(rec.ID)
	pid := rec.PID
	if pid <= 0 {
		// Adopted from a server that died before it wrote the number down.
		pid = detach.LockPID(lock)
	}
	detach.Kill(pid, lock)
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
		Running:   !rec.Finished && !rec.Pending,
		StartedAt: rec.StartedAt,
		EndedAt:   rec.EndedAt,
		Exited:    rec.Exited,
		Exit:      rec.Exit,
		Cancelled: rec.Cancelled,
		Failure:   rec.Failure,
		Owner:     rec.Owner,
		Pending:   rec.Pending,
		Declined:  rec.Declined,
	}
}

// Recover picks up the compose runs of an earlier server. What is still going
// gets its directory claimed and its busy mark back and is waited out; what
// finished while nobody was looking reports now, which is the notification the
// restart would otherwise have swallowed. A run that is over and was reported
// stays where it is, its output is what somebody still reads. A parked run is
// declined, unless its start had already been decided: that one is settled by
// what its files say (recoverLaunch), because the approval may well have
// started a process the register never heard of. Run it after
// OnComposeDone is set and before anything can start a run of its own.
func (s *Service) Recover() {
	for _, rec := range s.runs.List() {
		if rec.Finished {
			continue
		}
		if rec.Launching {
			started, ok := s.recoverLaunch(rec)
			if !ok {
				continue
			}
			rec = started
		} else if rec.Pending {
			// The question a parked run waited for lived in the process that
			// is gone, and nobody can answer a question nobody sees any more.
			// The run ends declined and its owner hears why.
			if err := s.DeclineCompose(rec.ID, "the approval was lost in a restart", false); err != nil {
				log.Printf("docker: decline run %s: %v", rec.ID, err)
			}
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

// recoverLaunch settles an entry whose start was decided by a process that
// went away before it wrote the outcome down. The run's files are the truth,
// the register is not: a lock that is held, a process number in the lock file
// or a result say a process existed, and the entry is then written down as
// the running run it is, for Recover to adopt or report like any other. None
// of the three means nothing was ever started, and the run ends failed, not
// declined: whoever had the say let it start, the restart is what stopped it.
func (s *Service) recoverLaunch(rec ComposeRecord) (ComposeRecord, bool) {
	_, lock, result := s.runs.paths(rec.ID)
	pid := rec.PID
	if pid <= 0 {
		pid = detach.LockPID(lock)
	}
	alive := detach.Alive(pid, lock)
	_, wrote := detach.Result(result)
	if !alive && !wrote && pid <= 0 {
		cause := errors.New("the cockpit restarted before the run could start")
		s.pending.Lock()
		run := s.closeUnstarted(rec, cause, false)
		s.pending.Unlock()
		s.reportUnstarted(run, cause)
		return ComposeRecord{}, false
	}
	return s.markLaunched(rec, pid), true
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
	// The cancel and a handed over owner are written on the entry by somebody
	// else, which is not this goroutine's copy of it. The read and the end
	// stand under the pending lock, so Disown either reaches the entry before
	// this reads it or finds it over.
	s.pending.Lock()
	if latest, ok := s.runs.Get(rec.ID); ok {
		rec.Cancelled = latest.Cancelled
		rec.CancelledByUser = latest.CancelledByUser
		rec.Owner = latest.Owner
	}
	code, exited := detach.Result(result)
	err := composeOutcome(code, exited, rec)
	output := tailOf(readTail(out, composeOutputTail), composeOutputTail)
	run := ComposeRun{ID: rec.ID, Dir: rec.Dir, Label: rec.Label, Action: rec.Action, Quiet: rec.Quiet, Owner: rec.Owner, Failed: err != nil, Exited: exited, Exit: code, ByUser: rec.Cancelled && rec.CancelledByUser}
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
	s.pending.Unlock()
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
