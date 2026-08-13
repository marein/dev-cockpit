// Package askpass bridges the questions ssh and git ask into the browser.
// The server points SSH_ASKPASS and GIT_ASKPASS of one user-triggered action
// at a helper that reports the prompt line here and blocks; the standing
// questions are server state, addressed by the project whose action asked,
// so any signed-in page shows the question, the typed answer travels back to
// the helper, which prints it and lets the action continue.
//
// The rules the whole package is built around:
//
//   - only an action somebody started gets a bridge: nothing here is wired
//     into a call unless the handler opened an Action for it, and everything
//     else keeps failing fast against /bin/false.
//   - an answer lives in memory and only for its one question: it is never
//     logged, never written anywhere, and handed out exactly once.
//   - the helper authenticates with a one-time token from its environment;
//     the browser side needs nothing beyond the session, because a question
//     belongs to the cockpit and not to the page that started the action:
//     the page may be reloaded, updated away or lying on a desk while the
//     phone answers.
//   - an ended action unblocks every waiting helper with a denial, so
//     cancelling never leaves a hanging process behind.
package askpass

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/local/dev-cockpit/internal/filesystem"
)

// The socket lives in a directory of its own that carries the permission,
// exactly like the local API's: the directory is closed before the socket
// exists. Short names, because the whole path has to fit a socket address.
const (
	socketDirName = "ask"
	socketFile    = "s"
)

// maxSocketPath mirrors the local API's bound for a unix socket address.
const maxSocketPath = 100

// maxAskBody and maxPrompt bound what a helper may report. The prompt line is
// the one value here that nobody in this process chose: ssh and git write it,
// and what they write can come from the other side of the network or from a
// hook the working copy carries. One question is one line, so a helper that
// sends more is not a helper with more to say, and neither the action's memory
// nor the dialog has to carry it.
const (
	maxAskBody = 16 << 10
	maxPrompt  = 500
)

// SocketPath answers where the broker of a state directory listens. The
// directory is resolved first (filesystem.AbsDir, the same rule the local API's
// socket follows), and that is not cosmetic here: this path travels into a git
// process whose working directory is the project, so a relative one would be
// read against the wrong directory and every helper would miss the socket
// without saying why. It is also what the length below is measured on, so a
// short spelling of a long path cannot walk past the fallback into an address
// the kernel refuses.
func SocketPath(stateDir string) string {
	dir := filesystem.AbsDir(stateDir)
	inState := filepath.Join(dir, socketDirName, socketFile)
	if len(inState) <= maxSocketPath {
		return inState
	}
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join(os.TempDir(), "dev-cockpit-ask-"+hex.EncodeToString(sum[:8]), socketFile)
}

// Listen opens the broker's socket the way the local API opens its own.
func Listen(stateDir string) (net.Listener, error) {
	path := SocketPath(stateDir)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return net.Listen("unix", path)
}

// WriteScript puts the helper stub next to the socket: ssh executes
// SSH_ASKPASS without arguments of ours, so a two line script hands over to
// this binary's hidden askpass command. It is rewritten on every start,
// because the binary's path is baked in and a self update moves it. The
// directory is its own to make, with the permission the socket's carries: in
// which order a start opens the socket and writes the stub is not a rule
// this may depend on.
func WriteScript(stateDir string) (string, error) {
	binary, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(SocketPath(stateDir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "helper")
	if err := os.WriteFile(path, []byte(helperScript(binary)), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

// helperScript is the stub itself. The binary's path is nobody's choice here,
// it is wherever this process was installed or moved to by an update, and a
// shell reads a quote, a dollar, a backtick or a backslash in it as syntax:
// interpolated as it comes, such a path runs something else or nothing at
// all. Single quotes take everything literally, which leaves exactly one
// character to handle, the quote that ends them.
func helperScript(binary string) string {
	return "#!/bin/sh\nexec " + shellQuote(binary) + " askpass \"$@\"\n"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// Question is what the browser shows: the prompt line ssh or git wrote, an id
// that makes sure an answer meets its own question, and the two values that
// say whose question it is. Project and Action are this server's own truth
// from the handler that opened the bridge, while the prompt line is written
// by ssh, git, or whatever program the repository put in their way; the
// dialog keeps them visually apart for exactly that reason, and leaves the
// line out where Command below already says the same in more detail.
//
// External separates the two kinds of asker, and it is the whole policy in one
// field: an action somebody started in the app is a page they are looking at,
// while a proxied call (`dev-cockpit git`) was typed in a terminal that cannot
// answer anything, by a caller who may be a coding agent. Only an external
// question has to leave the app to be seen at all, so only that one becomes a
// notification and rides the push channels; ringing for a dialog the person is
// already in front of is the one thing this may not do.
//
// It is its own field and not read off Command, which is the text below and
// would carry the policy on a rendering detail: the day a second surface in
// the app wants to show what it is about to run, setting Command would turn
// the push channels on for it.
//
// Command is what the dialog shows a caller nobody in the browser can see:
// the command line as it was typed, because somebody answering a passphrase
// for them has to be able to read what is about to run. Dir rides along and is
// the working copy it runs in, which is half of what somebody has to
// recognise: the same `git push` means different things in two checkouts of
// one repository, and the caller picked its project through a working
// directory nobody in the browser can see.
type Question struct {
	ID       string `json:"id"`
	Project  string `json:"project"`
	Action   string `json:"action"`
	Prompt   string `json:"prompt"`
	External bool   `json:"external,omitempty"`
	Command  string `json:"command,omitempty"`
	Dir      string `json:"dir,omitempty"`
}

type answer struct {
	text string
	deny bool
}

type question struct {
	Question
	seq   uint64
	reply chan answer
}

// Action is one user-triggered git call's bridge. The helper side finds it by
// the one-time token, the browser side by the project it runs in: the write
// lock lets one write per working copy through, so a project never runs two.
type Action struct {
	broker  *Broker
	token   string
	project string
	action  string
	// external says the caller is not in the app, command is the proxied
	// command line it was started with and dir the working copy it runs in;
	// see Question.External for what hangs on the first of them.
	external bool
	command  string
	dir      string

	mu        sync.Mutex
	pending   *question
	cancelled bool
	ended     sync.Once
	done      chan struct{}
	asked     chan struct{}
	answered  chan struct{}
}

// Broker owns the socket the helpers call and the actions currently allowed
// to ask. Everything lives in memory and dies with the action.
type Broker struct {
	socketPath string

	// OnChange, when set, is called whenever the standing questions moved: one
	// was parked, answered, or taken along by its action's end. The server
	// hangs the gitprompt event on it. It is set once at wiring time, before
	// any action exists.
	OnChange func()

	// seq orders the standing questions for the dialog queue. Atomic and not
	// under mu, because ask allocates it while holding its action's own lock
	// and End takes the two locks the other way around.
	seq atomic.Uint64

	mu        sync.Mutex
	byToken   map[string]*Action
	byProject map[string]*Action
}

// New builds a broker for the state directory's socket path. Serve is the
// caller's, like the local API: Listen, then http.Serve with Handler.
func New(stateDir string) *Broker {
	return &Broker{
		socketPath: SocketPath(stateDir),
		byToken:    map[string]*Action{},
		byProject:  map[string]*Action{},
	}
}

func randomToken() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}

func (b *Broker) notify() {
	if b.OnChange != nil {
		b.OnChange()
	}
}

// Begin opens the bridge for one action of one project. A project that
// already runs one is refused: the write lock in front of every bridge makes
// that unreachable, so this is an invariant guard and not a surface, but a
// second action under the same name would answer questions to the wrong
// caller and is the one thing this may never do silently.
func (b *Broker) Begin(project, action string) *Action {
	return b.begin(project, action, false, "", "")
}

// BeginCommand is Begin for a caller outside the app: its questions are the
// ones that have to leave the app to be seen, and the command line and the
// working copy travel with them for the dialog to show. Everything else is
// Begin's, including the one action per project rule.
func (b *Broker) BeginCommand(project, action, command, dir string) *Action {
	return b.begin(project, action, true, command, dir)
}

func (b *Broker) begin(project, action string, external bool, command, dir string) *Action {
	a := &Action{
		broker:   b,
		token:    randomToken(),
		project:  project,
		action:   action,
		external: external,
		command:  command,
		dir:      dir,
		done:     make(chan struct{}),
		asked:    make(chan struct{}, 8),
		answered: make(chan struct{}, 8),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, taken := b.byProject[project]; taken {
		return nil
	}
	b.byToken[a.token] = a
	b.byProject[project] = a
	return a
}

// Find answers the browser's side of a project's running action.
func (b *Broker) Find(project string) *Action {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.byProject[project]
}

// Questions answers the standing questions of every running action, oldest
// first, which is the order the global dialog serves them in. Usually it is
// empty or holds one; two mean two devices started two actions in two
// projects and both hit a prompt.
func (b *Broker) Questions() []Question {
	b.mu.Lock()
	actions := make([]*Action, 0, len(b.byProject))
	for _, a := range b.byProject {
		actions = append(actions, a)
	}
	b.mu.Unlock()
	type parked struct {
		Question
		seq uint64
	}
	standing := make([]parked, 0, len(actions))
	for _, a := range actions {
		a.mu.Lock()
		if a.pending != nil {
			standing = append(standing, parked{Question: a.pending.Question, seq: a.pending.seq})
		}
		a.mu.Unlock()
	}
	sort.Slice(standing, func(i, j int) bool { return standing[i].seq < standing[j].seq })
	questions := make([]Question, 0, len(standing))
	for _, p := range standing {
		questions = append(questions, p.Question)
	}
	return questions
}

// Name answers what the action is called, the word the dialog and the
// notification carry as this server's truth ("push", "pull").
func (a *Action) Name() string { return a.action }

// Env is what the spawned git call carries so its helpers can call home.
func (a *Action) Env() []string {
	return []string{
		"DC_ASKPASS_SOCKET=" + a.broker.socketPath,
		"DC_ASKPASS_TOKEN=" + a.token,
	}
}

// Asked signals every question that arrives; Answered every answer that went
// back. The caller's watchdog stretches its deadline on them: a person gets
// their own time, and an answered action gets its full budget back.
func (a *Action) Asked() <-chan struct{}    { return a.asked }
func (a *Action) Answered() <-chan struct{} { return a.answered }

// Question is the pending question of this one action, or nothing.
func (a *Action) Question() *Question {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending == nil {
		return nil
	}
	q := a.pending.Question
	return &q
}

// Answer resolves the pending question. deny is the cancel button: the
// helper fails, the action ends in git's words, and Cancelled remembers why.
func (a *Action) Answer(id, text string, deny bool) bool {
	a.mu.Lock()
	if a.pending == nil || a.pending.ID != id {
		a.mu.Unlock()
		return false
	}
	q := a.pending
	a.pending = nil
	if deny {
		a.cancelled = true
	}
	a.mu.Unlock()
	q.reply <- answer{text: text, deny: deny}
	select {
	case a.answered <- struct{}{}:
	default:
	}
	a.broker.notify()
	return true
}

// Cancelled reports whether somebody pressed cancel on a question of this
// action, which is what turns the refusal message into its honest sentence.
func (a *Action) Cancelled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cancelled
}

// End closes the bridge: the maps forget the action and every helper still
// waiting is denied, so nothing blocks past the action it belonged to. It is
// the one call every handler defers, on paths that may already have ended the
// action themselves, so ending twice has to be the same as ending once rather
// than a panic on a closed channel.
func (a *Action) End() {
	a.ended.Do(func() {
		a.broker.mu.Lock()
		delete(a.broker.byToken, a.token)
		delete(a.broker.byProject, a.project)
		a.broker.mu.Unlock()
		a.mu.Lock()
		pending := a.pending
		a.pending = nil
		a.mu.Unlock()
		if pending != nil {
			pending.reply <- answer{deny: true}
		}
		close(a.done)
		// Ending took the standing question along, so every open dialog has to
		// hear it, whichever way the action ended: answered, refused, timed
		// out, or cancelled by the caller.
		if pending != nil {
			a.broker.notify()
		}
	})
}

// ask is the helper's side: park the question and wait for its answer or the
// action's end. One question at a time per action, the way ssh asks.
func (a *Action) ask(prompt string) (string, bool) {
	q := &question{
		Question: Question{
			ID:       randomToken(),
			Project:  a.project,
			Action:   a.action,
			Prompt:   prompt,
			External: a.external,
			Command:  a.command,
			Dir:      a.dir,
		},
		seq:   a.broker.seq.Add(1),
		reply: make(chan answer, 1),
	}
	a.mu.Lock()
	if a.pending != nil {
		// A second asker while one question stands would interleave two
		// dialogs; deny it, the action is already in trouble.
		a.mu.Unlock()
		return "", false
	}
	select {
	case <-a.done:
		a.mu.Unlock()
		return "", false
	default:
	}
	a.pending = q
	a.mu.Unlock()
	select {
	case a.asked <- struct{}{}:
	default:
	}
	a.broker.notify()
	// The end of the action counts as an answer of its own, and it has to be
	// waited for beside the reply: End may have read pending as nil a moment
	// before this question was parked, in which case nobody is left to deny
	// it and waiting on the reply alone would block until the helper's own
	// budget runs out, minutes after the action it belonged to.
	select {
	case got := <-q.reply:
		if got.deny {
			return "", false
		}
		return got.text, true
	case <-a.done:
		return "", false
	}
}

type askRequest struct {
	Token  string `json:"token"`
	Prompt string `json:"prompt"`
}

// clampPrompt cuts the reported line back to one question's worth. It counts
// runes, so a cut never lands inside a character and the dialog never has to
// render a broken one.
func clampPrompt(prompt string) string {
	runes := []rune(prompt)
	if len(runes) <= maxPrompt {
		return prompt
	}
	return string(runes[:maxPrompt]) + "…"
}

// Handler is the broker's whole HTTP surface: one endpoint the helper posts
// its prompt to and blocks on. Nothing here is ever logged, an answer only
// exists in the response body.
func (b *Broker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ask", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req askRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAskBody)).Decode(&req); err != nil || req.Token == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		b.mu.Lock()
		a := b.byToken[req.Token]
		b.mu.Unlock()
		if a == nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		text, ok := a.ask(clampPrompt(req.Prompt))
		if !ok {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"answer": text})
	})
	return mux
}
