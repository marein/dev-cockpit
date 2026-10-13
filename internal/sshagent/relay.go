package sshagent

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/proctree"
)

// The messages of the agent protocol the relay reads. Everything else a
// caller sends is refused: the keys are the cockpit's to add and remove.
const (
	msgFailure             = 5
	msgRequestIdentities   = 11
	msgIdentitiesAnswer    = 12
	msgSignRequest         = 13
	msgRemoveIdentity      = 18
	msgRemoveAllIdentities = 19
	msgExtension           = 27
)

// maxMessage is ssh-agent's own bound for one message.
const maxMessage = 256 << 10

// askWait is a person's window for one question, the same the askpass bridge
// gives a git question, so a check nobody watches fails its signature after
// it. A variable only so a test sees that end without sitting out two
// minutes.
var askWait = 2 * time.Minute

var (
	failure           = []byte{msgFailure}
	errMessage        = errors.New("not a message of the agent protocol")
	fingerprintInLine = regexp.MustCompile(`SHA256:[A-Za-z0-9+/]{43}`)
)

func readMessage(r io.Reader) ([]byte, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n == 0 || n > maxMessage {
		return nil, errMessage
	}
	msg := make([]byte, n)
	if _, err := io.ReadFull(r, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func writeMessage(w io.Writer, msg []byte) error {
	_, err := w.Write(appendString(nil, msg))
	return err
}

func appendString(b, s []byte) []byte {
	return append(binary.BigEndian.AppendUint32(b, uint32(len(s))), s...)
}

func readString(b []byte) (s, rest []byte, ok bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	n := binary.BigEndian.Uint32(b)
	if uint64(n) > uint64(len(b)-4) {
		return nil, nil, false
	}
	return b[4 : 4+n], b[4+n:], true
}

type identity struct{ blob, comment []byte }

func parseIdentities(msg []byte) ([]identity, bool) {
	if len(msg) < 5 {
		return nil, false
	}
	n, b := binary.BigEndian.Uint32(msg[1:]), msg[5:]
	var ids []identity
	for i := uint32(0); i < n; i++ {
		blob, rest, ok := readString(b)
		if !ok {
			return nil, false
		}
		comment, rest, ok := readString(rest)
		if !ok {
			return nil, false
		}
		ids, b = append(ids, identity{blob, comment}), rest
	}
	return ids, true
}

func identitiesAnswer(ids []identity) []byte {
	msg := binary.BigEndian.AppendUint32([]byte{msgIdentitiesAnswer}, uint32(len(ids)))
	for _, id := range ids {
		msg = appendString(appendString(msg, id.blob), id.comment)
	}
	return msg
}

// conn is one caller on a kind's socket and its line to the kind's agent.
// pid is the process on the other end, shown in a question and never decided
// by.
type conn struct {
	kind Kind
	w    *watchedConn
	pid  int
	up   net.Conn
}

func (s *Service) serveConn(k Kind, nc net.Conn) {
	c := &conn{kind: k, w: &watchedConn{Conn: nc}, pid: proctree.PeerPID(nc)}
	s.mu.Lock()
	s.seq++
	s.kinds[k].callers[c] = s.seq
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.kinds[k].callers, c)
		s.mu.Unlock()
		_ = nc.Close()
		if c.up != nil {
			_ = c.up.Close()
		}
	}()
	for {
		msg, err := readMessage(c.w)
		if err != nil {
			return
		}
		if err := writeMessage(c.w, s.answer(c, msg)); err != nil {
			return
		}
	}
}

// answer is the relay's whole rule. Under Off the kind sees no key. Otherwise
// the list of keys and the extensions go to the agent as they are, the list
// with the protected keys it does not hold yet added, and a signature with
// such a key opens it first.
func (s *Service) answer(c *conn, msg []byte) []byte {
	policy := PolicyOf(s.get, c.kind)
	switch {
	case msg[0] == msgRequestIdentities && policy == Deny:
		return identitiesAnswer(nil)
	case policy == Deny:
		return failure
	case msg[0] == msgRequestIdentities:
		reply, err := s.forward(c, msg)
		if err != nil {
			return failure
		}
		return s.offer(reply)
	case msg[0] == msgSignRequest:
		return s.sign(c, policy, msg)
	case msg[0] == msgExtension:
		reply, err := s.forward(c, msg)
		if err != nil {
			return failure
		}
		return reply
	}
	return failure
}

// forward hands one message to the kind's agent and answers its reply. A
// line that broke is dropped, the next message dials again, which starts the
// agent anew where it is gone.
func (s *Service) forward(c *conn, msg []byte) ([]byte, error) {
	if c.up == nil {
		a, err := s.agentOf(c.kind)
		if err != nil {
			return nil, err
		}
		if c.up, err = net.DialTimeout("unix", a.path, dialWait); err != nil {
			return nil, err
		}
	}
	err := writeMessage(c.up, msg)
	if err == nil {
		var reply []byte
		if reply, err = readMessage(c.up); err == nil {
			return reply, nil
		}
	}
	_ = c.up.Close()
	c.up = nil
	return nil, err
}

// offer adds the protected keys the agent does not hold to its list, so ssh
// tries them and a signature can open one.
func (s *Service) offer(reply []byte) []byte {
	if reply[0] != msgIdentitiesAnswer {
		return reply
	}
	ids, ok := parseIdentities(reply)
	if !ok {
		return failure
	}
	listed := map[string]bool{}
	for _, id := range ids {
		listed[string(id.blob)] = true
	}
	for _, k := range s.keys() {
		if !k.Protected || listed[string(k.blob)] {
			continue
		}
		comment := k.Comment
		if comment == "" {
			comment = k.Path
		}
		ids = append(ids, identity{blob: k.blob, comment: []byte(comment)})
	}
	return identitiesAnswer(ids)
}

// sign opens the key a signature names where the kind's agent does not hold
// it under the kind's policy, and then lets the signature through, one per
// kind at a time. A key this caller opened carries the consent the
// passphrase gave, so the agent's confirmation of this one signature does
// not ask a second time.
func (s *Service) sign(c *conn, policy Policy, msg []byte) []byte {
	blob, _, ok := readString(msg[1:])
	if !ok {
		return failure
	}
	consent := ""
	if key, ok := s.offered(blob); ok && !s.holds(c.kind, key.Fingerprint, policy) {
		opened, err := s.open(c, policy, key)
		if err != nil {
			return failure
		}
		if opened {
			consent = key.Fingerprint
		}
	}
	kd := s.kinds[c.kind]
	kd.sign.Lock()
	defer kd.sign.Unlock()
	s.mu.Lock()
	kd.signing, kd.consent = c, consent
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		kd.signing, kd.consent = nil, ""
		s.mu.Unlock()
	}()
	reply, err := s.forward(c, msg)
	if err != nil {
		return failure
	}
	return reply
}

// confirm answers the question a kind's agent asks before it uses a key that
// went in with confirmation. It belongs to the signature in flight on the
// kind's socket; with none in flight something reached the agent around the
// relay, and it is refused. The consent of a passphrase typed for this
// signature answers it, so does a kind that allows, under Ask the person
// answers, seeing every caller connected to the socket.
func (s *Service) confirm(k Kind, prompt string) ([]byte, bool) {
	fingerprint := fingerprintInLine.FindString(prompt)
	kd := s.kinds[k]
	s.mu.Lock()
	c := kd.signing
	consented := c != nil && fingerprint != "" && kd.consent == fingerprint
	kd.consent = ""
	s.mu.Unlock()
	policy := PolicyOf(s.get, k)
	switch {
	case c == nil || policy == Deny:
		return nil, false
	case consented || policy == Allow:
		return []byte("yes"), true
	}
	ask := s.asker()
	if ask == nil {
		return nil, false
	}
	ctx, cancel := c.askContext()
	defer cancel()
	s.mu.Lock()
	kd.asking, kd.cancelAsk = fingerprint, cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		kd.asking, kd.cancelAsk = "", nil
		s.mu.Unlock()
	}()
	passphrase, ok := ask(ctx, Request{Kind: k, Key: s.keyOf(fingerprint), Callers: s.callers(k)})
	clear(passphrase)
	if !ok {
		return nil, false
	}
	return []byte("yes"), true
}

// callers reads the processes connected to a kind's socket, in the order
// they connected, each once.
func (s *Service) callers(k Kind) []Caller {
	type joined struct {
		pid int
		seq uint64
	}
	var conns []joined
	s.mu.Lock()
	for c, seq := range s.kinds[k].callers {
		if c.pid > 0 {
			conns = append(conns, joined{c.pid, seq})
		}
	}
	s.mu.Unlock()
	sort.Slice(conns, func(i, j int) bool { return conns[i].seq < conns[j].seq })
	tree := proctree.Capture()
	seen := map[int]bool{}
	var out []Caller
	for _, j := range conns {
		if seen[j.pid] {
			continue
		}
		seen[j.pid] = true
		if command := tree.Cmdline(j.pid); len(command) > 0 {
			out = append(out, Caller{Command: command, Dir: tree.CWD(j.pid)})
		}
	}
	return out
}

// askContext bounds one question by the person's window and by the caller
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

// watchedConn notices a caller that hangs up while its request waits for a
// person. The relay reads nothing while it handles a request, so the watch
// may read; a byte that arrives in the meantime is handed on.
type watchedConn struct {
	net.Conn
	mu     sync.Mutex
	peeked []byte
}

func (w *watchedConn) Read(p []byte) (int, error) {
	w.mu.Lock()
	if len(w.peeked) > 0 {
		n := copy(p, w.peeked)
		w.peeked = w.peeked[n:]
		w.mu.Unlock()
		return n, nil
	}
	w.mu.Unlock()
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
			w.mu.Lock()
			w.peeked = append(w.peeked, b[:n]...)
			w.mu.Unlock()
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
