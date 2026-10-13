// Package sshagent is the cockpit's own ssh agent. It runs when the cockpit
// starts without SSH_AUTH_SOCK, the usual case for a service without a login
// session, and offers the passphrase protected default keys of the user. A key
// is opened on its first use with a passphrase the person types in the
// browser, and stays open in memory for a set time. It never reaches the disk.
//
// Every caller reaches the agent on a socket of its own: one per shell and
// coder session, named in the session's environment and tagged on the tmux
// session, so a restarted cockpit listens on it again, one per git action of
// the UI, and one quiet socket for the git calls nobody started, which never
// asks. The socket says who signs, and the policy of that kind of caller says
// whether the agent signs, asks first or refuses. All sockets belong to one
// user, so the policy names the caller, it does not isolate one from another.
package sshagent

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Kind is a kind of caller, each with a policy of its own.
type Kind string

const (
	KindUI    Kind = "ui"
	KindShell Kind = "shell"
	KindCoder Kind = "coder"
)

// Kinds lists the callers in the order the settings show them.
var Kinds = []Kind{KindUI, KindShell, KindCoder}

// Policy is what the agent does when a caller of a kind wants a signature.
type Policy string

const (
	Allow Policy = "allow"
	Ask   Policy = "ask"
	Deny  Policy = "deny"
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
// for them, and asks for everything a terminal runs.
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

// Caller is who wants a signature: the kind its socket was opened for, and
// the name and project the person knows it by, both empty where unknown.
type Caller struct {
	Kind    Kind
	Name    string
	Project string
}

// Key is one default key file of the user.
type Key struct {
	Path        string
	Type        string
	Fingerprint string
	Comment     string
	// Protected says the file needs a passphrase. Only those are the agent's,
	// ssh reads a key without one itself, as it always did.
	Protected bool
	public    ssh.PublicKey
}

// Request is one question for the person. Command and Dir describe the
// process that wants the signature, as far as it can be read. Unlock asks for
// the passphrase, and typing it is the consent as well; without it the
// question only asks whether the caller may sign. Retry says the passphrase
// before did not open the key.
type Request struct {
	Caller  Caller
	Key     Key
	Command []string
	Dir     string
	Unlock  bool
	Retry   bool
}

// Asker carries a request to the person and answers the typed passphrase and
// whether the request was allowed. It returns once ctx ends at the latest,
// unless the question belongs to an action that ends it itself.
type Asker func(ctx context.Context, r Request) (string, bool)

// KeyStatus is a key with its state, for the settings page.
type KeyStatus struct {
	Key
	Open    bool
	Expires time.Time
}

// keyFiles are the identities ssh tries when nothing else is configured,
// without the hardware backed ones, which no agent can sign for.
var keyFiles = []string{"id_rsa", "id_ecdsa", "id_ed25519"}

// maxKeyFile bounds what is read as a key file, a real one is a few KiB.
const maxKeyFile = 64 << 10

// maxAttempts is how often one signature asks for the passphrase, the same
// number ssh itself allows.
const maxAttempts = 3

var (
	errDenied      = errors.New("the policy refuses this caller")
	errDeclined    = errors.New("the person declined")
	errNotAsked    = errors.New("the key is locked and this caller cannot ask")
	errUnknownKey  = errors.New("not a key of this agent")
	errNotOpened   = errors.New("no passphrase opened the key")
	errChanged     = errors.New("the key file changed")
	errUnsupported = errors.New("this agent only holds the user's own keys")
)

type openKey struct {
	signer ssh.Signer
	at     time.Time
}

// Service is the agent. Everything it opens stays in memory.
type Service struct {
	dir      string
	sshDir   string
	external string
	get      func(string) string
	now      func() time.Time

	mu        sync.Mutex
	open      map[string]*openKey
	keyLocks  map[string]*sync.Mutex
	listeners map[string]*listener
	onChange  func()
	resolve   func(tag string) (Caller, bool)
	ask       Asker
}

// New builds the agent of a state directory over the keys in sshDir. external
// is SSH_AUTH_SOCK as the cockpit found it at start: set, it wins and this
// agent stays off.
func New(stateDir, sshDir, external string, get func(string) string) *Service {
	return &Service{
		dir:       socketDir(stateDir),
		sshDir:    sshDir,
		external:  external,
		get:       get,
		now:       time.Now,
		open:      map[string]*openKey{},
		keyLocks:  map[string]*sync.Mutex{},
		listeners: map[string]*listener{},
	}
}

// Active says the cockpit serves its own agent.
func (s *Service) Active() bool { return s.external == "" }

// External answers the host's agent socket that won, empty when none did.
func (s *Service) External() string { return s.external }

// SetOnChange sets what runs when a key was opened or forgotten.
func (s *Service) SetOnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// SetResolver sets how a session's tag is turned into the caller's name.
func (s *Service) SetResolver(fn func(tag string) (Caller, bool)) {
	s.mu.Lock()
	s.resolve = fn
	s.mu.Unlock()
}

// SetAsker sets how a session's question reaches the person. Until it is set,
// a session that would have to ask is refused.
func (s *Service) SetAsker(fn Asker) {
	s.mu.Lock()
	s.ask = fn
	s.mu.Unlock()
}

func (s *Service) changed() {
	s.mu.Lock()
	fn := s.onChange
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// keys reads the default key files as they are on disk right now.
func (s *Service) keys() []Key {
	if s.sshDir == "" {
		return nil
	}
	var keys []Key
	for _, name := range keyFiles {
		if k, ok := readKey(filepath.Join(s.sshDir, name)); ok {
			keys = append(keys, k)
		}
	}
	return keys
}

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxKeyFile))
}

// readKey takes the public key from the private key file where its format
// carries one, because a .pub next to it may be stale, and from the .pub
// where it does not, the older PEM files.
func readKey(path string) (Key, bool) {
	data, err := readFile(path)
	if err != nil {
		return Key{}, false
	}
	pub, comment := readPublic(path + ".pub")
	k := Key{Path: path}
	var missing *ssh.PassphraseMissingError
	raw, err := ssh.ParseRawPrivateKey(data)
	switch {
	case err == nil:
		signer, err := ssh.NewSignerFromKey(raw)
		if err != nil {
			return Key{}, false
		}
		pub = signer.PublicKey()
	case errors.As(err, &missing):
		k.Protected = true
		if missing.PublicKey != nil {
			if pub == nil || !bytes.Equal(pub.Marshal(), missing.PublicKey.Marshal()) {
				comment = ""
			}
			pub = missing.PublicKey
		}
	default:
		return Key{}, false
	}
	if pub == nil {
		return Key{}, false
	}
	if strings.HasPrefix(pub.Type(), "sk-") {
		k.Protected = false
	}
	k.public = pub
	k.Type = pub.Type()
	k.Fingerprint = ssh.FingerprintSHA256(pub)
	k.Comment = comment
	return k, true
}

func readPublic(path string) (ssh.PublicKey, string) {
	data, err := readFile(path)
	if err != nil {
		return nil, ""
	}
	pub, comment, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return nil, ""
	}
	return pub, comment
}

// protected answers the agent's key a public key names.
func (s *Service) protected(pub ssh.PublicKey) (Key, bool) {
	want := pub.Marshal()
	for _, k := range s.keys() {
		if k.Protected && bytes.Equal(k.public.Marshal(), want) {
			return k, true
		}
	}
	return Key{}, false
}

// signer answers the open signer of a key, nil while it is locked or its time
// is up. An expired key is left for expire, which says so.
func (s *Service) signer(fingerprint string) ssh.Signer {
	ttl := TTLOf(s.get)
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.open[fingerprint]
	if o == nil || s.now().Sub(o.at) >= ttl {
		return nil
	}
	return o.signer
}

func (s *Service) keep(fingerprint string, signer ssh.Signer) {
	s.mu.Lock()
	s.open[fingerprint] = &openKey{signer: signer, at: s.now()}
	s.mu.Unlock()
	s.changed()
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

// Forget closes one open key. It answers false for a key that was not open.
func (s *Service) Forget(fingerprint string) bool {
	s.mu.Lock()
	_, ok := s.open[fingerprint]
	delete(s.open, fingerprint)
	s.mu.Unlock()
	if ok {
		s.changed()
	}
	return ok
}

// expire forgets the keys whose time is up, and the ones whose file is gone
// or holds another key now, which no request can reach any more.
func (s *Service) expire() {
	ttl := TTLOf(s.get)
	s.mu.Lock()
	if len(s.open) == 0 {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	onDisk := map[string]bool{}
	for _, k := range s.keys() {
		if k.Protected {
			onDisk[k.Fingerprint] = true
		}
	}
	changed := false
	s.mu.Lock()
	for fp, o := range s.open {
		if !onDisk[fp] || s.now().Sub(o.at) >= ttl {
			delete(s.open, fp)
			changed = true
		}
	}
	s.mu.Unlock()
	if changed {
		s.changed()
	}
}

// Status answers every default key with its state.
func (s *Service) Status() []KeyStatus {
	ttl := TTLOf(s.get)
	keys := s.keys()
	out := make([]KeyStatus, 0, len(keys))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		st := KeyStatus{Key: k}
		if o := s.open[k.Fingerprint]; o != nil && k.Protected {
			if expires := o.at.Add(ttl); s.now().Before(expires) {
				st.Open = true
				st.Expires = expires
			}
		}
		out = append(out, st)
	}
	return out
}

// sign is one signature request of a caller, from the policy to the key.
func (s *Service) sign(c *conn, pub ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	policy := PolicyOf(s.get, c.l.kind)
	if policy == Deny {
		return nil, errDenied
	}
	k, ok := s.protected(pub)
	if !ok {
		return nil, errUnknownKey
	}
	if signer := s.signer(k.Fingerprint); signer != nil && policy == Allow {
		return signWith(signer, data, flags)
	}
	ask := s.askerOf(c.l)
	if c.l.quiet || ask == nil {
		return nil, errNotAsked
	}
	c.l.askMu.Lock()
	defer c.l.askMu.Unlock()
	ctx, cancel := c.askContext()
	defer cancel()
	req := Request{Caller: s.callerOf(c.l), Key: k}
	req.Command, req.Dir = peerProcess(c.w.Conn)
	signer, err := s.consent(ctx, ask, req, policy)
	if err != nil {
		return nil, err
	}
	return signWith(signer, data, flags)
}

// consent gets the person's yes and the open key. One question per key at a
// time: a second caller waits for the first and finds the key open, so two
// pushes at once never ask for the same passphrase twice.
func (s *Service) consent(ctx context.Context, ask Asker, req Request, policy Policy) (ssh.Signer, error) {
	lock := s.keyLock(req.Key.Fingerprint)
	lock.Lock()
	defer lock.Unlock()
	if signer := s.signer(req.Key.Fingerprint); signer != nil {
		if policy == Allow {
			return signer, nil
		}
		if _, ok := ask(ctx, req); !ok {
			return nil, errDeclined
		}
		return signer, nil
	}
	data, err := readFile(req.Key.Path)
	if err != nil {
		return nil, err
	}
	req.Unlock = true
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req.Retry = attempt > 0
		passphrase, ok := ask(ctx, req)
		if !ok {
			return nil, errDeclined
		}
		signer, err := openSigner(data, passphrase, req.Key.public)
		if errors.Is(err, errNotOpened) {
			continue
		}
		if err != nil {
			return nil, err
		}
		s.keep(req.Key.Fingerprint, signer)
		return signer, nil
	}
	return nil, errNotOpened
}

func openSigner(data []byte, passphrase string, want ssh.PublicKey) (ssh.Signer, error) {
	raw, err := ssh.ParseRawPrivateKeyWithPassphrase(data, []byte(passphrase))
	if err != nil {
		return nil, errNotOpened
	}
	signer, err := ssh.NewSignerFromKey(raw)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(signer.PublicKey().Marshal(), want.Marshal()) {
		return nil, errChanged
	}
	return signer, nil
}

func signWith(signer ssh.Signer, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	if flags == 0 {
		return signer.Sign(rand.Reader, data)
	}
	algorithmSigner, ok := signer.(ssh.AlgorithmSigner)
	if !ok {
		return nil, fmt.Errorf("the key cannot sign with flags %d", flags)
	}
	switch flags {
	case agent.SignatureFlagRsaSha256:
		return algorithmSigner.SignWithAlgorithm(rand.Reader, data, ssh.KeyAlgoRSASHA256)
	case agent.SignatureFlagRsaSha512:
		return algorithmSigner.SignWithAlgorithm(rand.Reader, data, ssh.KeyAlgoRSASHA512)
	}
	return nil, fmt.Errorf("unsupported signature flags %d", flags)
}
