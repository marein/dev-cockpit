// Package sshagent runs the cockpit's ssh agents. It starts when the cockpit
// starts without SSH_AUTH_SOCK, the usual case for a service without a login
// session, and offers the passphrase protected default keys of the user. The
// keys are held by OpenSSH's ssh-agent, one process per kind of caller, and
// come into them with ssh-add: the cockpit never reads, decrypts or holds a
// private key. A key opens on its first use with a passphrase the person types
// in the browser, goes into the agent of every kind that may use it for a set
// time, and never reaches the disk.
//
// There is one socket per kind of caller at a fixed path in the state
// directory, so a session that outlives a restart of the cockpit keeps its
// socket. It is a relay to the private socket of the kind's ssh-agent that
// learns which process connected, offers the protected keys the agent does
// not hold yet, opens one when a caller signs with it, and keeps callers from
// adding, removing or locking keys. The shells and the coders get their
// kind's socket in their environment when they start, the turns of the
// assistants theirs, the git actions of the UI theirs. Every process of the
// user may open every socket, so the policy of a kind guards against an
// accident, never against intent: Allow adds a key as it is, Ask adds it with
// confirmation, so its agent asks the person on every use, Off offers no key.
// A key with confirmation serves every policy, the cockpit confirms it under
// Allow and refuses it under Off, so only a key without one has to go when
// its kind leaves Allow.
package sshagent

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind is a kind of caller, each with a socket, an agent and a policy of its
// own.
type Kind string

const (
	KindUI        Kind = "ui"
	KindShell     Kind = "shell"
	KindCoder     Kind = "coder"
	KindAssistant Kind = "assistant"
)

// Kinds lists the callers in the order the settings show them.
var Kinds = []Kind{KindUI, KindShell, KindCoder, KindAssistant}

// Policy is what the agent does when a caller of a kind wants a signature.
type Policy string

const (
	Allow Policy = "allow"
	Ask   Policy = "ask"
	// Deny offers the caller no key, ssh then asks for the passphrase
	// itself, so the settings call it Off.
	Deny Policy = "deny"
)

// TTLSettingKey holds how many minutes an opened key stays open.
const TTLSettingKey = "ssh-agent-ttl-minutes"

// PolicySettingKey is the settings key of one kind's policy.
func PolicySettingKey(k Kind) string { return "ssh-agent-policy-" + string(k) }

const (
	DefaultTTL = time.Hour
	MaxTTL     = 7 * 24 * time.Hour
)

// DefaultPolicy lets the UI's own git actions sign, somebody pressed a button
// for them, and asks for everything a terminal or an assistant runs.
func DefaultPolicy(k Kind) Policy {
	if k == KindUI {
		return Allow
	}
	return Ask
}

// ParsePolicy answers the policy a stored or posted value names.
func ParsePolicy(v string) (Policy, bool) {
	switch p := Policy(v); p {
	case Allow, Ask, Deny:
		return p, true
	}
	return "", false
}

// PolicyOf answers the policy of a kind, its default where nothing valid is stored.
func PolicyOf(get func(string) string, k Kind) Policy {
	if p, ok := ParsePolicy(get(PolicySettingKey(k))); ok {
		return p
	}
	return DefaultPolicy(k)
}

// ParseTTL reads a number of minutes between one and MaxTTL.
func ParseTTL(v string) (time.Duration, bool) {
	minutes, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || minutes < 1 {
		return 0, false
	}
	ttl := time.Duration(minutes) * time.Minute
	if ttl > MaxTTL {
		return 0, false
	}
	return ttl, true
}

// TTLOf answers how long an opened key stays open.
func TTLOf(get func(string) string) time.Duration {
	if ttl, ok := ParseTTL(get(TTLSettingKey)); ok {
		return ttl
	}
	return DefaultTTL
}

// Caller is a process connected to a kind's socket, as far as it can be read.
type Caller struct {
	Command []string
	Dir     string
}

// Request is one question for the person. Kind is the socket it came
// through, Callers every process connected to it, shown to the person and
// never decided by. Unlock asks for the passphrase, and typing it is the
// consent as well; without it the question only asks whether the caller may
// sign. Retry says the passphrase before did not open the key.
type Request struct {
	Kind    Kind
	Key     Key
	Callers []Caller
	Unlock  bool
	Retry   bool
}

// Asker carries a request to the person and answers the typed passphrase and
// whether the request was allowed. It returns once ctx ends at the latest. The
// passphrase belongs to the agent from then on, which clears it after use.
type Asker func(ctx context.Context, r Request) ([]byte, bool)

// Prompter hands a process the agent starts, ssh-agent or ssh-add, the
// environment that routes its SSH_ASKPASS questions to answer, until end is
// called. answer returns the line to print and whether there is one.
type Prompter func(answer func(prompt string) ([]byte, bool)) (env []string, end func())

// KeyStatus is a key with its state, for the settings page. Left is how long
// an open key stays open, counted when the status was read: a time of day
// would be read against the clock of whoever shows it.
type KeyStatus struct {
	Key
	Open bool
	Left time.Duration
}

// maxAttempts is how often one signature asks for the passphrase, the same
// number ssh itself allows.
const maxAttempts = 3

var (
	errDeclined        = errors.New("the person declined")
	errNotAsked        = errors.New("the key is locked and nobody can be asked")
	errNotOpened       = errors.New("no passphrase opened the key")
	errWrongPassphrase = errors.New("the passphrase did not open the key")
)

// tools are the OpenSSH programs the agent runs, plus the one that keeps an
// ssh-agent bound to this process.
type tools struct {
	agent, add, keygen, cat string
}

func findTools() (tools, bool) {
	var t tools
	for _, slot := range []struct {
		name string
		path *string
	}{{"ssh-agent", &t.agent}, {"ssh-add", &t.add}, {"ssh-keygen", &t.keygen}, {"cat", &t.cat}} {
		path, err := exec.LookPath(slot.name)
		if err != nil {
			return tools{}, false
		}
		*slot.path = path
	}
	return t, true
}

// Service is the agents of one cockpit. What it knows of a key is its public
// part and when it went into which agent.
type Service struct {
	dir      string
	sshDir   string
	external string
	tools    tools
	usable   bool
	get      func(string) string
	now      func() time.Time

	mu        sync.Mutex
	kinds     map[Kind]*kind
	keyLocks  map[string]*sync.Mutex
	listeners map[Kind]net.Listener
	probed    map[string]probe
	onChange  func()
	ask       Asker
	prompt    Prompter
	seq       uint64
}

// kind is what the service holds for one kind of caller. The fields below
// start and sign are guarded by the service's lock.
type kind struct {
	start sync.Mutex
	// sign lets one signature at a time through to the kind's agent, so a
	// question the agent asks belongs to the signature in flight.
	sign      sync.Mutex
	agent     *agent
	callers   map[*conn]uint64
	signing   *conn
	consent   string
	asking    string
	cancelAsk context.CancelFunc
}

// New builds the agents of a state directory over the keys in sshDir.
// external is SSH_AUTH_SOCK as the cockpit found it at start: set, it wins
// and the cockpit runs no agent, unless it names one of the cockpit's own
// sockets, which a cockpit started from one of its own terminals inherits.
func New(stateDir, sshDir, external string, get func(string) string) *Service {
	s := &Service{
		dir:       socketDir(stateDir),
		sshDir:    sshDir,
		get:       get,
		now:       time.Now,
		kinds:     map[Kind]*kind{},
		keyLocks:  map[string]*sync.Mutex{},
		listeners: map[Kind]net.Listener{},
		probed:    map[string]probe{},
	}
	s.tools, s.usable = findTools()
	for _, k := range Kinds {
		s.kinds[k] = &kind{callers: map[*conn]uint64{}}
	}
	if _, own := s.kindAt(external); !own {
		s.external = external
	}
	return s
}

// Active says the cockpit serves its own agents: nothing answered before it,
// and OpenSSH's tools are there.
func (s *Service) Active() bool { return s.external == "" && s.usable }

// External answers the host's agent socket that won, empty when none did.
func (s *Service) External() string { return s.external }

// SetOnChange sets what runs when a key was opened or forgotten.
func (s *Service) SetOnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// SetAsker sets how a question reaches the person. Until it is set, a
// signature that would have to ask is refused.
func (s *Service) SetAsker(fn Asker) {
	s.mu.Lock()
	s.ask = fn
	s.mu.Unlock()
}

// SetPrompter sets how the agent's processes reach the cockpit. Until it is
// set, no ssh-agent starts.
func (s *Service) SetPrompter(fn Prompter) {
	s.mu.Lock()
	s.prompt = fn
	s.mu.Unlock()
}

func (s *Service) asker() Asker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ask
}

func (s *Service) prompter() Prompter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prompt
}

func (s *Service) changed() {
	s.mu.Lock()
	fn := s.onChange
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (s *Service) keyLock(fingerprint string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.keyLocks[fingerprint]
	if lock == nil {
		lock = &sync.Mutex{}
		s.keyLocks[fingerprint] = lock
	}
	return lock
}

// holds says the kind's agent has a key that serves the kind's current
// policy, for the time that is still left.
func (s *Service) holds(k Kind, fingerprint string, policy Policy) bool {
	ttl := TTLOf(s.get)
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.kinds[k].agent
	if a == nil {
		return false
	}
	h, ok := a.held[fingerprint]
	return ok && h.serves(policy) && s.now().Before(h.end(ttl))
}

// Status answers every default key with its state. A key is open while any
// agent holds it.
func (s *Service) Status() []KeyStatus {
	ttl := TTLOf(s.get)
	keys := s.keys()
	out := make([]KeyStatus, 0, len(keys))
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, k := range keys {
		st := KeyStatus{Key: k}
		for _, kd := range s.kinds {
			if kd.agent == nil {
				continue
			}
			if h, ok := kd.agent.held[k.Fingerprint]; ok && k.Protected {
				if left := h.end(ttl).Sub(now); left > st.Left {
					st.Open, st.Left = true, left
				}
			}
		}
		out = append(out, st)
	}
	return out
}

// open asks for the passphrase of a key a caller wants to sign with and adds
// the key to the agent of every kind that may use it. One question per key at
// a time: a second caller waits for the first and finds the key open, so two
// pushes at once never ask for the same passphrase twice. opened says this
// call asked, so the signature that follows carries the consent the
// passphrase gave.
func (s *Service) open(c *conn, policy Policy, key Key) (opened bool, err error) {
	lock := s.keyLock(key.Fingerprint)
	lock.Lock()
	defer lock.Unlock()
	if s.holds(c.kind, key.Fingerprint, policy) {
		return false, nil
	}
	ask := s.asker()
	if ask == nil || s.prompter() == nil {
		return false, errNotAsked
	}
	ctx, cancel := c.askContext()
	defer cancel()
	req := Request{Kind: c.kind, Key: key, Callers: s.callers(c.kind), Unlock: true}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req.Retry = attempt > 0
		passphrase, ok := ask(ctx, req)
		if !ok {
			clear(passphrase)
			return false, errDeclined
		}
		err := s.addEverywhere(c.kind, key, passphrase)
		clear(passphrase)
		if errors.Is(err, errWrongPassphrase) {
			continue
		}
		return err == nil, err
	}
	return false, errNotOpened
}

// addEverywhere puts a key into the agent of the kind that asked, which tells
// a wrong passphrase, and then into the agent of every other kind, each under
// its own policy, so one passphrase serves them all, a kind switched on later
// included.
func (s *Service) addEverywhere(first Kind, key Key, passphrase []byte) error {
	if err := s.add(first, key, passphrase); err != nil {
		return err
	}
	for _, k := range Kinds {
		if k == first {
			continue
		}
		if err := s.add(k, key, passphrase); err != nil {
			log.Printf("ssh agent: %s stays locked for the %s callers: %v", key.Path, k, err)
		}
	}
	s.changed()
	return nil
}

// add runs ssh-add for one key into one kind's agent: for the time set, with
// confirmation unless the kind allows. ssh-add asks for the passphrase once, a
// second question means the first answer did not open the key.
func (s *Service) add(k Kind, key Key, passphrase []byte) error {
	confirm := PolicyOf(s.get, k) != Allow
	ttl := TTLOf(s.get)
	a, err := s.agentOf(k)
	if err != nil {
		return err
	}
	var mu sync.Mutex
	asked := 0
	env, end := s.prompter()(func(string) ([]byte, bool) {
		mu.Lock()
		defer mu.Unlock()
		asked++
		if asked > 1 {
			return nil, false
		}
		return bytes.Clone(passphrase), true
	})
	defer end()
	args := []string{"-t", strconv.FormatInt(int64(ttl/time.Second), 10)}
	if confirm {
		args = append(args, "-c")
	}
	at := s.now()
	_, err = s.run(s.tools.add, append(args, "--", key.Path), append(env, "SSH_AUTH_SOCK="+a.path))
	mu.Lock()
	wrong := asked > 1
	mu.Unlock()
	if wrong {
		return errWrongPassphrase
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.kinds[k].agent == a {
		a.held[key.Fingerprint] = held{blob: key.blob, confirm: confirm, at: at, until: at.Add(ttl)}
	}
	s.mu.Unlock()
	return nil
}

// Forget takes a key out of every agent. It answers false for a key that was
// not open.
func (s *Service) Forget(fingerprint string) bool {
	removed := false
	for _, k := range Kinds {
		if s.remove(k, fingerprint) {
			removed = true
		}
	}
	if removed {
		s.changed()
	}
	return removed
}

// Apply takes the settings as they are now to the agents: a key goes from an
// agent once its time is up, also by a time set shorter since, when it went
// in without confirmation and its kind no longer allows, and when its file is
// gone or holds another key, which no caller can reach any more. The tick
// runs it, and a save, so a changed policy holds at once.
func (s *Service) Apply() {
	ttl := TTLOf(s.get)
	type gone struct {
		kind        Kind
		fingerprint string
	}
	var drop []gone
	s.mu.Lock()
	holding := false
	for _, kd := range s.kinds {
		holding = holding || (kd.agent != nil && len(kd.agent.held) > 0)
	}
	s.mu.Unlock()
	if !holding {
		return
	}
	onDisk := map[string][]byte{}
	for _, k := range s.keys() {
		if k.Protected {
			onDisk[k.Fingerprint] = k.blob
		}
	}
	s.mu.Lock()
	now := s.now()
	for k, kd := range s.kinds {
		if kd.agent == nil {
			continue
		}
		policy := PolicyOf(s.get, k)
		for fp, h := range kd.agent.held {
			if !now.Before(h.end(ttl)) || !h.serves(policy) || !bytes.Equal(onDisk[fp], h.blob) {
				drop = append(drop, gone{k, fp})
			}
		}
	}
	s.mu.Unlock()
	for _, g := range drop {
		s.remove(g.kind, g.fingerprint)
	}
	if len(drop) > 0 {
		s.changed()
	}
}

// Run applies the settings in rounds, until ctx ends.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	if !s.Active() {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.Apply()
	}
}
