// Package git reads what a repository says about a project's working copy,
// and writes it in a deliberately short list of places: Commit, Push (plain,
// or force-with-lease), Fetch, the fast forward Pull, the two branch moves
// Checkout and CreateBranch, and Clone into a directory that holds nothing
// yet. Staging, discarding, stashing, merging and everything else that
// rewrites a repository stays with a coder or the command line, and a
// refused write leaves the working copy as it was.
//
// Every call goes through run, which is the one place the safety rules live,
// because a status poll runs next to a coder that may be committing right now:
//
//   - GIT_OPTIONAL_LOCKS=0 on every single call, so a read never takes the
//     index.lock away from that coder.
//   - no shell anywhere, the process is started with an argument list.
//   - "--" before any path, so a file named like a flag stays a file.
//   - core.quotepath=false and -z where git offers it, so paths arrive as
//     bytes instead of escapes.
//   - a timeout on every process and a cap on how much output is kept, and
//     the timeout ends the whole process group with a bounded pipe wait
//     (WaitDelay), so a helper git leaves behind — a credential helper
//     waiting for input, a signer's pinentry — can neither survive it nor
//     hold the answer open.
//   - every prompt fails in seconds instead of waiting for that timeout:
//     GIT_TERMINAL_PROMPT=0 for git's own questions, and for ssh's an
//     askpass forced to /bin/false, which turns a passphrase question into
//     an immediate denial while the host's choice of ssh (core.sshCommand,
//     GIT_SSH_COMMAND) stays untouched and agent keys keep working.
//   - GIT_ALLOW_PROTOCOL names the transports a URL may use, because the
//     dangerous ones are schemes and not options: ext:: runs the command in
//     the URL, and no "--" in front of it changes that.
//
// A directory that is not a repository is not an error: the calls answer "no
// repo" and the editor keeps looking exactly like it does without git.
package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DefaultTimeout caps one git process. Status on a normal repository answers in
// milliseconds; this is the ceiling for a repository on a slow or stalled disk,
// after which the caller gets an error instead of a hanging request.
const DefaultTimeout = 5 * time.Second

// maxOutput caps what one call keeps in memory. A repository with an
// implausible number of changed files truncates instead of filling the heap.
const maxOutput = 8 << 20

// promptWait is a person's window for one question of the askpass bridge:
// once a prompt reached the browser, the watchdog waits this long for the
// answer instead of the action's own budget, and every answer hands the
// action its full budget back. A variable only so a test can prove the
// message names this window and not the action's budget without sitting out
// two minutes for it.
var promptWait = 2 * time.Minute

// waitDelay is the grace Wait gets once the process is gone or killed: the
// output pipes may still be held open by something git left behind (an ssh
// asking for a passphrase nobody can answer, a signer, a credential helper),
// and without this bound Run would wait for their EOF instead of answering.
// A survivor never turns a clean exit into a failure, see run.
const waitDelay = 2 * time.Second

// ErrNoAnswer marks a call that produced no result at all: the process could
// not be started, the deadline ended it, or the caller dropped it. That is
// not the same as git having decided something, which is what an exit code
// is, and the difference matters exactly once: a directory that answers "not
// a repository" said so, while a call that never ran says nothing about the
// directory. Everything that only reports is free to read both as "no repo";
// Fingerprint is the one caller that may not, because publishing a move
// nobody made costs every open editor a round.
var ErrNoAnswer = errors.New("no answer")

// deadlineCause is how the breathing deadline of a prompted call says that it
// was the one that ended the call, and with which budget. A watchdog that
// only cancels is indistinguishable from a caller that dropped the request,
// and reporting a cancellation for a deadline is exactly the lie the three
// messages in run exist to avoid: a write runs on a context without
// cancellation, so nobody there can ever have dropped it.
type deadlineCause struct {
	budget time.Duration
}

func (d deadlineCause) Error() string { return "no answer within " + d.budget.String() }

// Repo reads one repository, addressed by a directory inside it. The directory
// is usually the project root, which may sit below the repository root; the
// paths git reports are always relative to the repository root, so they are cut
// back to the project in the calls that report them.
type Repo struct {
	dir     string
	timeout time.Duration
	// prompt, when set, lets this one call ask the person in the browser
	// through the askpass bridge; nil is the default and every question
	// fails fast. Only the user-triggered actions ever set it.
	prompt *Prompt
}

// Prompt is one action's line to the person in front of the browser: the
// environment that points the call's helpers at the bridge, and the two
// signals the watchdog stretches its deadline on — a question grants the
// person their own window, an answer grants the action its budget again.
type Prompt struct {
	Env      []string
	Asked    <-chan struct{}
	Answered <-chan struct{}
}

// WithPrompt answers a copy of the reader whose calls may ask.
func (r *Repo) WithPrompt(p *Prompt) *Repo {
	w := *r
	w.prompt = p
	return &w
}

// New returns a reader for the repository the given directory belongs to.
// Nothing runs yet, and the directory does not have to be a repository.
func New(dir string) *Repo {
	return &Repo{dir: dir, timeout: DefaultTimeout}
}

// repoInfo is the resolved repository around the directory: where its git
// directory is, and where the directory itself sits inside the work tree.
type repoInfo struct {
	gitDir string
	// prefix is the directory's path inside the work tree, empty at the root,
	// otherwise with a trailing slash, exactly as git reports it.
	prefix string
}

// resolve answers whether the directory is inside a repository and where. It is
// the one place that decides "no repo", every other call starts here.
func (r *Repo) resolve(ctx context.Context) (repoInfo, bool) {
	info, ok, _ := r.resolveErr(ctx)
	return info, ok
}

// resolveErr is resolve with the third state kept: a call git never answered
// travels as ErrNoAnswer instead of being flattened into "no repository".
// Only Fingerprint reads it, every other caller has nothing else to do with a
// failure than to stay away, which is what "no repo" already means to them.
func (r *Repo) resolveErr(ctx context.Context) (repoInfo, bool, error) {
	out, err := r.run(ctx, []string{"rev-parse", "--absolute-git-dir", "--show-prefix"}, nil)
	if err != nil {
		if errors.Is(err, ErrNoAnswer) {
			return repoInfo{}, false, err
		}
		return repoInfo{}, false, nil
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return repoInfo{}, false, nil
	}
	info := repoInfo{gitDir: lines[0]}
	if len(lines) > 1 {
		info.prefix = lines[1]
	}
	return info, true, nil
}

// WorkingCopy names the working copy this directory belongs to, for a caller
// that has to let one write at a time through it. The name is the absolute
// git directory, and that is the working copy and not the repository: a
// linked worktree has one of its own, while two projects inside the same
// checkout, a project below the repository root included, resolve to the same
// one and are the same working copy, which is exactly what a checkout and a
// commit may not do to each other.
//
// It keeps the third state (`resolveErr`, `ErrNoAnswer`) instead of flattening
// it, and it is the second caller after Fingerprint that may not do without
// it: false says "this directory holds no working copy", which a caller may
// key around, while the error says "nobody knows", which it may not. Guessing
// there produces two names for one working copy, and two names are no lock at
// all. It is the one rev-parse every other call starts with and nothing on
// top of it.
func (r *Repo) WorkingCopy(ctx context.Context) (string, bool, error) {
	info, ok, err := r.resolveErr(ctx)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	return info.gitDir, true, nil
}

// run executes one git call in the repository directory. args carry the
// subcommand and its options, paths carry repository paths and always go behind
// the "--" separator. There is no shell on this path, and the environment the
// process gets differs from ours in the four values below.
func (r *Repo) run(ctx context.Context, args []string, paths []string) ([]byte, error) {
	// With a prompt attached the deadline breathes instead of standing: the
	// base budget as before, stretched to a human's window while a question
	// is out and back to the full budget with every answer. Without one the
	// plain timeout stands.
	//
	// A breathing deadline cannot be a context deadline, so the watchdog
	// cancels — and then ctx.Err() says Canceled for something nobody
	// cancelled. It therefore hands its own reason along, because the whole
	// point of the three messages below is that a timeout never claims to be
	// a dropped call and the other way round.
	var cancel context.CancelFunc
	var watchdogCause func() error
	if r.prompt != nil {
		var withCause context.CancelCauseFunc
		ctx, withCause = context.WithCancelCause(ctx)
		cancel = func() { withCause(context.Canceled) }
		// budget is what the watchdog is armed with right now, the action's
		// own timeout until a question goes out and promptWait for as long as
		// one stands. The goroutine below writes it and the timer's own
		// function reads it, so it travels under the mutex.
		var mu sync.Mutex
		budget := r.timeout
		watchdog := time.AfterFunc(r.timeout, func() {
			mu.Lock()
			ran := budget
			mu.Unlock()
			withCause(deadlineCause{budget: ran})
		})
		arm := func(d time.Duration) {
			mu.Lock()
			budget = d
			mu.Unlock()
			watchdog.Reset(d)
		}
		watchdogCause = func() error { return context.Cause(ctx) }
		stop := make(chan struct{})
		// The goroutine is waited for and only then is the timer stopped, in
		// that order and both before run returns. Closing the channel alone
		// says when the goroutine was told to go, not when it went: it may be
		// standing between reading a signal and arming the timer, and a Stop
		// that passes it there rearms an AfterFunc for a call that is already
		// over — up to promptWait of a timer nobody is waiting on, cancelling
		// a context nobody reads.
		armed := make(chan struct{})
		defer func() {
			close(stop)
			<-armed
			watchdog.Stop()
		}()
		go func() {
			defer close(armed)
			for {
				select {
				case <-r.prompt.Asked:
					arm(promptWait)
				case <-r.prompt.Answered:
					arm(r.timeout)
				case <-stop:
					return
				}
			}
		}()
	} else {
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
	}
	defer cancel()

	full := make([]string, 0, len(args)+len(paths)+3)
	full = append(full, "-c", "core.quotepath=false")
	full = append(full, args...)
	if len(paths) > 0 {
		full = append(full, "--")
		full = append(full, paths...)
	}

	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.dir
	// os/exec keeps the last value of a duplicated key, so these win over
	// inherited ones. Without the first a status poll can take the index lock
	// from a coder that is committing at that moment. The rest makes every
	// prompt fail in seconds instead of running into the timeout: git's own
	// questions through GIT_TERMINAL_PROMPT=0, ssh's through an askpass
	// forced to /bin/false (OpenSSH 8.4+), so a key that wants a passphrase
	// is denied at once in ssh's words while a key from the agent keeps
	// working. Pinning the askpass, not the ssh: which ssh runs stays the
	// host's own wiring, GIT_SSH_COMMAND or core.sshCommand included, and a
	// desktop's GUI askpass cannot be popped by a server nobody sits in
	// front of.
	// The transports are a whitelist of ours and not the host's answer:
	// ext:: and fd:: are not options, so the "--" in front of a URL does
	// nothing about them, and ext:: runs whatever command the URL carries.
	// Whether they are refused is otherwise git's default plus whatever
	// protocol.*.allow the host happens to configure, which is a decision
	// this package cannot leave to a repository it clones for somebody.
	// file stays in: a local clone is an ordinary thing to do here.
	cmd.Env = append(os.Environ(),
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"SSH_ASKPASS=/bin/false",
		"SSH_ASKPASS_REQUIRE=force",
		"GIT_ALLOW_PROTOCOL=http:https:ssh:git:file",
	)
	// A call that may ask points its helpers at the bridge instead; the
	// last value wins, so the fail-fast defaults above stay for everything
	// else.
	if r.prompt != nil {
		cmd.Env = append(cmd.Env, r.prompt.Env...)
	}
	// The buffers ride on pipes, and Run waits for every writer to close its
	// end — not only git, also whatever git started and left behind. The wait
	// delay bounds that wait once the process is gone, and the group kill (on
	// the platforms that have one) takes the survivors with the timeout
	// instead of orphaning an ssh that still waits for its passphrase.
	cmd.WaitDelay = waitDelay
	killsWholeGroup(cmd)
	out := &cappedBuffer{max: maxOutput}
	errOut := &cappedBuffer{max: 4096}
	cmd.Stdout = out
	cmd.Stderr = errOut
	// A survivor holding the pipes past the grace is not git failing: the
	// process itself exited cleanly, ErrWaitDelay only says the pipes had to
	// be closed for it, and what made it through is the answer.
	if err := cmd.Run(); err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		message := strings.TrimSpace(errOut.buf.String())
		if message != "" {
			message = ": " + message
		}
		// Three ways to fail, and only the last one is git deciding
		// something. "signal: killed" names the mechanism and not the
		// reason, so each of the other two says its own, and what git wrote
		// before it died still travels along.
		var watchdog deadlineCause
		switch {
		case watchdogCause != nil && errors.As(watchdogCause(), &watchdog):
			// A breathing deadline ran out. It names the budget it was armed
			// with, which is the action's own or a person's window while a
			// question stood, and never the other one.
			return nil, fmt.Errorf("git %s: %w within %s%s", args[0], ErrNoAnswer, watchdog.budget, message)
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return nil, fmt.Errorf("git %s: %w within %s%s", args[0], ErrNoAnswer, r.timeout, message)
		case ctx.Err() != nil:
			// The deadline still stood, so somebody dropped this call: the
			// server is shutting down, or a caller that hangs on a request
			// lost it. Naming the timeout here would be a lie.
			return nil, fmt.Errorf("git %s: %w, the call was cancelled%s", args[0], ErrNoAnswer, message)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			// The process never ran: git is not on the path, the directory
			// is gone, the fork failed. Nothing here is about the repository.
			return nil, fmt.Errorf("git %s: %w: %s", args[0], ErrNoAnswer, err)
		}
		if message == "" {
			message = ": " + err.Error()
		}
		return nil, fmt.Errorf("git %s%s", args[0], message)
	}
	return out.buf.Bytes(), nil
}

// cappedBuffer keeps at most max bytes and swallows the rest. It keeps
// accepting writes, so a chatty git process finishes instead of blocking on a
// reader that stopped listening.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

// Fingerprint is what the poller compares between two rounds. It carries two
// parts, because they answer two different questions and one of them is
// expensive to answer wrongly.
//
// Base is the commit HEAD points at, the one thing the editor's diff is built
// against. It moves on a commit and on nothing cheaper.
//
// Worktree is what the working copy looks like, the status output itself. It
// moves on every keystroke that reaches the disk, this editor's own saves
// included.
//
// Both empty means the directory is no repository, which is a state like any
// other and stays that way between rounds. "git could not be asked" is not that
// state, and Fingerprint says so with its second return value instead of
// answering the zero value: a round that failed knows nothing, and treating
// nothing as a change publishes a move that never happened, twice, once on the
// failure and once when the next healthy round finds the old value again.
type Fingerprint struct {
	Base     string
	Worktree string
}

// Moved reports whether anything at all changed between two rounds.
func (f Fingerprint) Moved(other Fingerprint) bool {
	return f.Base != other.Base || f.Worktree != other.Worktree
}

// Fingerprint reads both parts in one pass. Splitting them is what lets a
// client tell "somebody saved a file" from "the commit I am comparing against
// moved": the first needs a fresh status and nothing else, the second is the
// only reason to fetch the revision again.
func (r *Repo) Fingerprint(ctx context.Context) (Fingerprint, bool) {
	if _, ok, err := r.resolveErr(ctx); !ok {
		// "This is no repository" is an answer and stays one between rounds.
		// "git could not be asked" is not, and it arrives here first: this is
		// the call every round starts with, so flattening the two would let a
		// stalled disk publish an empty fingerprint as a moved base, and the
		// next healthy round would publish the move back.
		if err != nil {
			return Fingerprint{}, false
		}
		return Fingerprint{}, true
	}
	status, err := r.run(ctx, statusArgs, nil)
	if err != nil {
		return Fingerprint{}, false
	}
	// An empty base is the repository without a first commit, and git says so
	// with an exit code. A call that answered nothing at all says nothing
	// about HEAD either, and taking it for the unborn state would publish a
	// moved base on the failure and the move back on the recovery, which is
	// the one thing this second return value exists to prevent.
	base := ""
	head, err := r.run(ctx, []string{"rev-parse", "HEAD"}, nil)
	switch {
	case err == nil:
		base = strings.TrimSpace(string(head))
	case errors.Is(err, ErrNoAnswer):
		return Fingerprint{}, false
	}
	work := sha256.Sum256(status)
	return Fingerprint{
		Base:     base,
		Worktree: hex.EncodeToString(work[:]),
	}, true
}
