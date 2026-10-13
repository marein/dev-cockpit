package sshagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/proctree"
	"github.com/marein/dev-cockpit/internal/statefile"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// The sockets live in a directory of their own that carries the permission,
// like the local API's. Short names, the whole path has to fit a socket
// address.
const (
	socketDirName = "ssh-agent"
	quietName     = "q"
	maxSocketPath = 100
)

// reapGrace keeps a fresh session socket open before its session carries the
// tag: the socket opens before tmux starts the session, the tag follows.
const reapGrace = time.Minute

// askWait is a person's window for one question of a session, the same the
// askpass bridge gives a git question.
const askWait = 2 * time.Minute

// sessionTag is the socket name of a session, the kind's letter and an id.
var sessionTag = regexp.MustCompile(`^[sc][0-9a-f]{16}$`)

func socketDir(stateDir string) string {
	dir := filepath.Join(filesystem.AbsDir(stateDir), socketDirName)
	if len(filepath.Join(dir, "a"+statefile.NewID())) <= maxSocketPath {
		return dir
	}
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join(os.TempDir(), "dev-cockpit-ssh-agent-"+hex.EncodeToString(sum[:8]))
}

func kindOfTag(tag string) Kind {
	if tag[0] == 'c' {
		return KindCoder
	}
	return KindShell
}

type listener struct {
	name    string
	path    string
	kind    Kind
	session bool
	quiet   bool
	// caller and ask belong to an action's socket, a session's caller is
	// resolved when it asks and its questions go through the service's asker.
	caller Caller
	ask    Asker
	ln     net.Listener
	born   time.Time
	askMu  sync.Mutex
}

func (s *Service) listen(l *listener) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return err
	}
	l.path = filepath.Join(s.dir, l.name)
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	ln, err := net.Listen("unix", l.path)
	if err != nil {
		return err
	}
	if err := os.Chmod(l.path, 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	l.ln = ln
	l.born = s.now()
	s.mu.Lock()
	s.listeners[l.name] = l
	s.mu.Unlock()
	go s.serve(l)
	return nil
}

func (s *Service) serve(l *listener) {
	for {
		c, err := l.ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			log.Printf("ssh agent %s: %v", l.name, err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go s.serveConn(l, c)
	}
}

func (s *Service) serveConn(l *listener, c net.Conn) {
	defer c.Close()
	w := &watchedConn{Conn: c}
	_ = agent.ServeAgent(&conn{s: s, l: l, w: w}, w)
}

func (s *Service) close(name string) {
	s.mu.Lock()
	l := s.listeners[name]
	delete(s.listeners, name)
	s.mu.Unlock()
	if l != nil {
		_ = l.ln.Close()
	}
}

// Start opens the quiet socket and listens again on the sockets of the
// sessions a previous process started, before the first page renders. Every
// socket file still in the directory belongs to a process that is gone.
func (s *Service) Start(sessions func() ([]string, error)) error {
	if !s.Active() {
		return nil
	}
	if entries, err := os.ReadDir(s.dir); err == nil {
		for _, e := range entries {
			if e.Type()&fs.ModeSocket != 0 {
				_ = os.Remove(filepath.Join(s.dir, e.Name()))
			}
		}
	}
	if err := s.listen(&listener{name: quietName, kind: KindUI, quiet: true}); err != nil {
		return err
	}
	if tags, err := sessions(); err == nil {
		s.Reconcile(tags)
	}
	return nil
}

// Run forgets the keys whose time is up and keeps the session sockets in step
// with the live sessions, until ctx ends. A list that failed changes nothing.
func (s *Service) Run(ctx context.Context, every time.Duration, sessions func() ([]string, error)) {
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
		s.expire()
		if tags, err := sessions(); err == nil {
			s.Reconcile(tags)
		}
	}
}

// Reconcile listens on the socket of every live session that has none, which
// after a restart is every session, and closes the sockets of the sessions
// that are gone. A session's process keeps the path in its environment for
// its whole life, so the path is the same in every process of the cockpit. A
// socket closed by mistake opens again on the next round.
func (s *Service) Reconcile(tags []string) {
	if !s.Active() {
		return
	}
	live := map[string]bool{}
	for _, tag := range tags {
		if sessionTag.MatchString(tag) {
			live[tag] = true
		}
	}
	var missing, gone []string
	s.mu.Lock()
	for tag := range live {
		if s.listeners[tag] == nil {
			missing = append(missing, tag)
		}
	}
	for name, l := range s.listeners {
		if l.session && !live[name] && s.now().Sub(l.born) > reapGrace {
			gone = append(gone, name)
		}
	}
	s.mu.Unlock()
	for _, name := range gone {
		s.close(name)
	}
	for _, tag := range missing {
		if err := s.listen(&listener{name: tag, kind: kindOfTag(tag), session: true}); err != nil {
			log.Printf("ssh agent: the socket of session %s: %v", tag, err)
		}
	}
}

// OpenSession opens the socket of a session about to start and answers the
// environment to start it with and the tag to mark it with. An agent that is
// off, or a socket that does not open, answers nothing, and the session
// starts the way it always did.
func (s *Service) OpenSession(kind Kind) (map[string]string, string) {
	if !s.Active() {
		return nil, ""
	}
	letter := "s"
	if kind == KindCoder {
		letter = "c"
	}
	l := &listener{name: letter + statefile.NewID(), kind: kind, session: true}
	if err := s.listen(l); err != nil {
		log.Printf("ssh agent: a %s socket: %v", kind, err)
		return nil, ""
	}
	return map[string]string{"SSH_AUTH_SOCK": l.path}, l.name
}

// OpenAction opens the socket of one git action the cockpit runs, for the
// caller it runs for, and answers the environment for the call and the
// function that closes the socket. Its questions go through ask, which parks
// them on the action, so they stand and end with it. An agent that is off
// answers no environment: the call keeps the host's agent.
func (s *Service) OpenAction(caller Caller, ask Asker) ([]string, func()) {
	if !s.Active() {
		return nil, func() {}
	}
	l := &listener{name: "a" + statefile.NewID(), kind: caller.Kind, caller: caller, ask: ask}
	if err := s.listen(l); err != nil {
		log.Printf("ssh agent: an action socket: %v", err)
		return nil, func() {}
	}
	return []string{"SSH_AUTH_SOCK=" + l.path}, func() { s.close(l.name) }
}

// QuietEnv is the environment of a git call nobody started: it reaches the
// keys that are open, for as long as the UI may sign without asking, and is
// refused at once for everything else.
func (s *Service) QuietEnv() []string {
	if !s.Active() {
		return nil
	}
	s.mu.Lock()
	l := s.listeners[quietName]
	s.mu.Unlock()
	if l == nil {
		return nil
	}
	return []string{"SSH_AUTH_SOCK=" + l.path}
}

// SessionCaller answers who a session socket belongs to, for a git call the
// cockpit runs on behalf of that session. Only a socket this agent serves
// for a session counts.
func (s *Service) SessionCaller(path string) (Caller, bool) {
	if !s.Active() || filepath.Dir(path) != s.dir {
		return Caller{}, false
	}
	s.mu.Lock()
	l := s.listeners[filepath.Base(path)]
	s.mu.Unlock()
	if l == nil || !l.session {
		return Caller{}, false
	}
	return s.callerOf(l), true
}

func (s *Service) callerOf(l *listener) Caller {
	if !l.session {
		return l.caller
	}
	s.mu.Lock()
	resolve := s.resolve
	s.mu.Unlock()
	if resolve != nil {
		if c, ok := resolve(l.name); ok {
			c.Kind = l.kind
			return c
		}
	}
	return Caller{Kind: l.kind}
}

func (s *Service) askerOf(l *listener) Asker {
	if l.ask != nil {
		return l.ask
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ask
}

func peerProcess(c net.Conn) ([]string, string) {
	pid := peerPID(c)
	if pid <= 0 {
		return nil, ""
	}
	tree := proctree.Capture()
	return tree.Cmdline(pid), tree.CWD(pid)
}

// conn is one client of one socket, the agent protocol's side of it.
type conn struct {
	s *Service
	l *listener
	w *watchedConn
}

// List offers the protected keys, none to a caller the policy refuses, and
// to the quiet socket only the keys it could sign with, so ssh goes on to
// its own files as it did without an agent.
func (c *conn) List() ([]*agent.Key, error) {
	policy := PolicyOf(c.s.get, c.l.kind)
	if policy == Deny {
		return nil, nil
	}
	var out []*agent.Key
	for _, k := range c.s.keys() {
		if !k.Protected {
			continue
		}
		if c.l.quiet && (policy != Allow || c.s.signer(k.Fingerprint) == nil) {
			continue
		}
		comment := k.Comment
		if comment == "" {
			comment = k.Path
		}
		out = append(out, &agent.Key{Format: k.public.Type(), Blob: k.public.Marshal(), Comment: comment})
	}
	return out, nil
}

func (c *conn) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	return c.s.sign(c, key, data, 0)
}

func (c *conn) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	return c.s.sign(c, key, data, flags)
}

func (c *conn) Add(agent.AddedKey) error       { return errUnsupported }
func (c *conn) Remove(ssh.PublicKey) error     { return errUnsupported }
func (c *conn) RemoveAll() error               { return errUnsupported }
func (c *conn) Lock([]byte) error              { return errUnsupported }
func (c *conn) Unlock([]byte) error            { return errUnsupported }
func (c *conn) Signers() ([]ssh.Signer, error) { return nil, errUnsupported }
func (c *conn) Extension(string, []byte) ([]byte, error) {
	return nil, agent.ErrExtensionUnsupported
}

// askContext bounds one question by the person's window and by the process
// that asked: a push stopped with Ctrl+C takes its question down at once.
func (c *conn) askContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), askWait)
	gone, stop := c.w.watch()
	go func() {
		select {
		case <-gone:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		cancel()
		stop()
	}
}

// watchedConn notices a client that hangs up while its request waits for a
// person. The agent protocol reads nothing while a request is handled, so the
// watch may read; a byte that arrives in the meantime is handed on.
type watchedConn struct {
	net.Conn
	peeked []byte
}

func (w *watchedConn) Read(p []byte) (int, error) {
	if len(w.peeked) > 0 {
		n := copy(p, w.peeked)
		w.peeked = w.peeked[n:]
		return n, nil
	}
	return w.Conn.Read(p)
}

func (w *watchedConn) watch() (<-chan struct{}, func()) {
	gone := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var b [1]byte
		n, err := w.Conn.Read(b[:])
		if n > 0 {
			w.peeked = append(w.peeked, b[:n]...)
		}
		if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			close(gone)
		}
	}()
	return gone, func() {
		_ = w.Conn.SetReadDeadline(time.Now())
		<-done
		_ = w.Conn.SetReadDeadline(time.Time{})
	}
}
