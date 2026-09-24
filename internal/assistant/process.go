package assistant

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/marein/dev-cockpit/internal/detach"
)

// maxLineBytes bounds one structured output line. A provider packs a complete
// assistant message into a single JSON record, so the scanner needs far more
// than its default, but not an unbounded amount.
const maxLineBytes = 4 << 20

// stderrTailBytes is how much of a failing process's standard error is kept for
// the server log and for the parser's diagnosis. Of it at most one line ever
// reaches the browser, redacted and cut, see Quote.
const stderrTailBytes = 4 << 10

// unreadableLineBytes bounds how much of an undecodable output line reaches the
// server log.
const unreadableLineBytes = 300

// UnreadableLine shortens a raw output line for the server log. A parser that
// meets a line it cannot decode logs it through this and reads on, so the log
// says what arrived without carrying a whole answer, and the turn's outcome
// stays with the records the parser does evaluate.
func UnreadableLine(line []byte) string {
	text := strings.TrimSpace(string(line))
	if len(text) <= unreadableLineBytes {
		return text
	}
	cut := 0
	for i := range text {
		if i > unreadableLineBytes {
			break
		}
		cut = i
	}
	return text[:cut] + "…"
}

// pollInterval is how long the reader waits when the output file has nothing
// new. A provider writes in bursts, and a read that found something reads again
// at once, so this is the pause between bursts and not a delay on the answer.
const pollInterval = 40 * time.Millisecond

// Command is the process one turn runs: a program and its arguments, never a
// shell line. A prompt travels in the argv, so it can never become a command.
type Command struct {
	Name string
	Args []string
	// Env is what a coder needs in its environment on top of what this server
	// inherits, KEY=VALUE strings. It is read once, when the process starts, so
	// a turn picked up after a restart needs nothing from it.
	Env []string
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

// start launches one turn detached from this server, through internal/detach:
// its own session, its standard output straight into a file, no pipe between
// the two, and a lock that says whether it is still writing. That is what lets
// the answer keep being written while the cockpit restarts.
//
// A turn carries no timeout down there. A chat turn runs as long as the user
// lets it, and a check's deadline is the server's to enforce, because only the
// server knows what to write into the transcript when it passes.
//
// Who this turn is travels nowhere here: the turn runs the cockpit's own
// commands to act, and every one of them carries the assistant on its --as
// flag, spelled into the instruction file of the workspace the turn runs in.
//
// Whatever goes wrong on the way reads the same to the user: this server could
// not start the coder. The reason is the caller's log, not their sentence.
func start(c Command, workdir, outPath, errPath, lockPath string) (detach.Process, error) {
	if strings.TrimSpace(c.Name) == "" {
		return detach.Process{}, errors.New("The coder could not be started.")
	}
	// detach replaces the whole environment when one is set and inherits this
	// process's own when none is, so the coder's variables go on top of a
	// copy of it, and a command without any leaves the option nil. PWD stands
	// between the two because os/exec writes it for the working directory only
	// when it inherits (go.dev/issue/50599), and opencode reads its project
	// root off PWD before it asks the kernel: without that line a copy carries
	// the server's own, and the turn runs against the directory the server was
	// started from.
	var env []string
	if len(c.Env) > 0 {
		pwd, err := filepath.Abs(workdir)
		if err != nil {
			log.Printf("assistant: start %s: %v", c.Name, err)
			return detach.Process{}, errors.New("The coder could not be started.")
		}
		env = append(os.Environ(), "PWD="+pwd)
		env = append(env, c.Env...)
	}
	p, err := detach.Start(detach.Options{
		Command: append([]string{c.Name}, c.Args...),
		Dir:     workdir,
		Env:     env,
		Out:     outPath,
		Err:     errPath,
		Lock:    lockPath,
	})
	if err != nil {
		log.Printf("assistant: start %s: %v", c.Name, err)
		return detach.Process{}, errors.New("The coder could not be started.")
	}
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

// stderrTail is the end of what a turn wrote to standard error. It is read to
// name the failure, for the server log, and for the one line Quote shows of it
// when nothing could be named.
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
// the user reads about it, so no CLI's own wording leaks into an assistant. Read
// in full the rule is: the cockpit owns the sentence, and the CLI supplies at
// most a quoted detail behind it, see Quote. It is the login kind of Refusal,
// so every caller that compared against it keeps working.
var ErrNotLoggedIn = &Refusal{Kind: RefusalLogin}

// RefusalKind names why a CLI refused to start a turn.
type RefusalKind string

const (
	// RefusalLogin is a CLI nobody logged in on this machine.
	RefusalLogin RefusalKind = "login"
	// RefusalUnknownModel is a CLI that does not know the model it was given.
	RefusalUnknownModel RefusalKind = "unknown-model"
)

// Refusal is a CLI that refused to start the turn, named by a parser out of its
// own CLI's output and worded here. A parser fills the kind and what the CLI
// named, nothing else; the service places the refusal on its run before anybody
// reads it (placed), because the sentence says where the user fixes it, and that
// is decided by the run and not by the CLI: the ring button of the assistant
// for a chat turn and a check, the trigger for a reaction that ran on the
// trigger's own model. A refusal is what a check must not retry, the CLI will
// refuse again, which is why it is a type and not a sentence.
type Refusal struct {
	Kind RefusalKind
	// Model is the model the CLI said it does not know, empty where it named
	// none. What the user picked stands above it, see placed.
	Model string

	// run, picked and ownModel are the run's, filled by placed: the kind of turn,
	// the model the cockpit passed, and whether that model was the trigger's own.
	run      RunKind
	picked   string
	ownModel bool
}

// Error is the sentence the user reads, in this package's words.
func (r *Refusal) Error() string {
	switch r.Kind {
	case RefusalUnknownModel:
		name := r.picked
		if name == "" {
			name = r.Model
		}
		sentence := "The coder does not know the model it was started with."
		if name != "" {
			sentence = "The coder does not know the model " + name + "."
		}
		where := "at the ring button of this assistant"
		if r.run == RunReaction && r.ownModel {
			where = "on the trigger"
		}
		return sentence + " Pick another " + where + "."
	default:
		return "The coder is not logged in on this machine. Start it once in a terminal, log in there, and send this again."
	}
}

// Is makes every refusal of a kind match the sentinel of that kind, so
// errors.Is(err, ErrNotLoggedIn) holds for a placed copy as it does for the
// value a parser returned.
func (r *Refusal) Is(target error) bool {
	t, ok := target.(*Refusal)
	return ok && t.Kind == r.Kind
}

// UnknownModel is the refusal of a CLI that does not know the model it was
// given, with the name the CLI echoed. That name is the one string a CLI
// supplies that ends up inside a sentence instead of behind it as a quote, so
// it is read here under the value rule every picked name passes: cut to
// MaxModelRunes, then CleanModel, and a name the rule refuses is dropped, the
// sentence then says the model it was started with. A parser hands the name
// over as it read it and never builds the struct itself.
func UnknownModel(name string) *Refusal {
	runes := []rune(strings.TrimSpace(name))
	if len(runes) > MaxModelRunes {
		runes = runes[:MaxModelRunes]
	}
	name, err := CleanModel(string(runes))
	if err != nil {
		name = ""
	}
	return &Refusal{Kind: RefusalUnknownModel, Model: name}
}

// placed is this refusal on its run: a copy carrying what the sentence needs to
// say where the model was set. The parser's value is never written to, a parser
// may hand out one shared sentinel.
func (r *Refusal) placed(rec RunRecord) *Refusal {
	placed := *r
	placed.run, placed.picked, placed.ownModel = rec.Kind, rec.Model, rec.TriggerModel
	return &placed
}

// Said is a failure no recogniser could name, with the one line the CLI wrote
// about it kept apart from the cockpit's sentence: the sentence is the frame
// every surface owns, the line is the detail quoted behind it, so a CLI whose
// wording changes tomorrow still reaches the user in words instead of sending
// them to the log.
type Said struct {
	Err error
	// Line is one line, redacted and bounded, see quoteLine.
	Line string
}

func (s *Said) Error() string { return s.Err.Error() + " The coder said: " + s.Line }

func (s *Said) Unwrap() error { return s.Err }

// quoteRunes bounds the line quoted from a CLI. It is the bound sanitizeError
// keeps a sentence under, so the frame and its quote are two parts of one size.
const quoteRunes = 200

// Quote puts what a CLI said behind a failure the cockpit could not name: the
// last non empty line of said, one line, redacted, at most quoteRunes, as "The
// coder said: <line>". A parser calls it with the error record its CLI writes
// on standard output, the service with the tail of standard error. A refusal
// stays as it is, it has its own sentence; a failure that is quoted already
// stays as it is, the record on standard output is closer to the cause than
// whatever followed it on standard error; and nothing said leaves the frame
// alone.
func Quote(err error, said string) error {
	if err == nil {
		return nil
	}
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return err
	}
	var already *Said
	if errors.As(err, &already) {
		return err
	}
	line := quoteLine(said)
	if line == "" {
		return err
	}
	return &Said{Err: err, Line: line}
}

// quoteLine is the last non empty line of a CLI's output the way it may be
// shown: one line, redacted, cut at quoteRunes.
func quoteLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := sanitizeLine(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// sanitizeLine is what a line of a CLI's may look like on a page or a phone:
// one line, redacted, at most quoteRunes. It is the quote's alone: a sentence
// the cockpit wrote carries nothing to redact and may carry a model name,
// which the redaction would take for a token, see sanitizeError.
func sanitizeLine(text string) string {
	return truncateRunes(redact(oneLine(text)), quoteRunes)
}

// The shapes redact takes out of a quoted line. A line a CLI writes about a
// failure may echo the request that failed, and that request carried a key, a
// bearer token or a path under the home directory; a quote reaches the browser,
// the push channels and a webhook, so none of that may ride along.
var (
	// secretToken is a key by its issuer's prefix, which alone identifies the
	// shape: eight characters behind it are enough to tell one from a word.
	secretToken = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{8,}|gh[opsur]_[A-Za-z0-9]{8,}|github_pat_[A-Za-z0-9_]{8,}|xox[abprs]-[A-Za-z0-9-]{8,}|AKIA[A-Z0-9]{12,})`)
	// bearerToken is an authorization header echoed whole.
	bearerToken = regexp.MustCompile(`(?i)\bbearer\s+[^\s"',;]{8,}`)
	// secretValue is a key written as a name and a value, key=…, token: ….
	secretValue = regexp.MustCompile(`(?i)\b((?:api[_-]?key|token|secret|password|passwd|authorization)\s*[:=]\s*"?)([^\s"',;]+)`)
	// randomRun is a run of the token alphabet too long to be anything but a
	// token. The one long run of that alphabet a CLI line quotes legitimately
	// is a model name, and CleanModel bounds those at MaxModelRunes, so the
	// threshold stands one above it and the two rules cannot disagree; every
	// key shape known by name is caught above whatever its length.
	randomRun = regexp.MustCompile(`[A-Za-z0-9_-]{` + strconv.Itoa(MaxModelRunes+1) + `,}`)
	// absoluteDirs is the directory part of an absolute path, which is where
	// the user name and the state directory stand; the file name stays, it says
	// which file. The prefix keeps a URL's //host/ out of it.
	absoluteDirs = regexp.MustCompile(`(^|[\s"'(=:])/(?:[^/\s"'()]+/)+`)
)

// redact takes keys, tokens and directories out of one line, see the patterns.
func redact(line string) string {
	line = secretToken.ReplaceAllString(line, "[redacted]")
	line = bearerToken.ReplaceAllString(line, "Bearer [redacted]")
	line = secretValue.ReplaceAllString(line, "${1}[redacted]")
	line = randomRun.ReplaceAllStringFunc(line, func(run string) string {
		if strings.ContainsFunc(run, unicode.IsLetter) && strings.ContainsFunc(run, unicode.IsDigit) {
			return "[redacted]"
		}
		return run
	})
	return absoluteDirs.ReplaceAllString(line, "${1}…/")
}

// failureOf is the error a failed turn ends with, decided once the run is
// known: a refusal the parser named is placed on this run, so its sentence
// says where the model was set, and everything else that came from the coder
// carries one line of what the coder said, see Quote. An end the cockpit
// decided itself, the deadline and the size cap, quotes nothing: a line on
// standard error says nothing about a limit this server enforced.
func failureOf(err error, rec RunRecord, stderr string, fromCoder bool) error {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal.placed(rec)
	}
	if !fromCoder {
		return err
	}
	return Quote(err, stderr)
}
