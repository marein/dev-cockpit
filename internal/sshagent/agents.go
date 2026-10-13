package sshagent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// agentsDirName is where the private sockets of the ssh-agents live, below
// the callers' sockets. Only the cockpit talks to them.
const agentsDirName = "agents"

const (
	dialWait  = 2 * time.Second
	startWait = 5 * time.Second
	// talkWait bounds a request of the cockpit's own to an agent, which
	// answers nothing else while a confirmation of it waits for the person.
	talkWait = 5 * time.Second
)

// agent is one ssh-agent of a kind. It runs cat as its command, with the
// write end of cat's stdin held here: whichever way the cockpit ends, cat
// reads the end of its input, and ssh-agent, which checks its parent every
// ten seconds, ends after it and takes its keys and its socket along.
type agent struct {
	path    string
	stdin   io.Closer
	end     func()
	held    map[string]held
	retired bool
}

// held is a key the cockpit put into an agent: its public part, whether the
// agent confirms each use, and its time, the one ssh-agent was given, which a
// time set shorter since cuts.
type held struct {
	blob      []byte
	confirm   bool
	at, until time.Time
}

// serves says the key may stay under a policy: one the agent signs with
// unasked only where the kind allows.
func (h held) serves(p Policy) bool { return h.confirm || p == Allow }

func (h held) end(ttl time.Duration) time.Time {
	if cut := h.at.Add(ttl); cut.Before(h.until) {
		return cut
	}
	return h.until
}

// agentOf answers the running agent of a kind. It starts one where none
// runs yet, or where the one before stopped answering.
func (s *Service) agentOf(k Kind) (*agent, error) {
	kd := s.kinds[k]
	kd.start.Lock()
	defer kd.start.Unlock()
	s.mu.Lock()
	a := kd.agent
	s.mu.Unlock()
	if a != nil {
		if c, err := net.DialTimeout("unix", a.path, dialWait); err == nil {
			_ = c.Close()
			return a, nil
		}
		s.retire(k, a)
	}
	a, err := s.startAgent(k)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	kd.agent = a
	s.mu.Unlock()
	return a, nil
}

// startAgent starts a kind's ssh-agent at a fresh socket. A confirmation it
// asks for reaches confirm through SSH_ASKPASS.
func (s *Service) startAgent(k Kind) (*agent, error) {
	prompt := s.prompter()
	if prompt == nil {
		return nil, errNotAsked
	}
	name := make([]byte, 4)
	_, _ = rand.Read(name)
	path := filepath.Join(s.dir, agentsDirName, string(k)+"-"+hex.EncodeToString(name))
	env, end := prompt(func(line string) ([]byte, bool) { return s.confirm(k, line) })
	cmd := exec.Command(s.tools.agent, "-a", path, s.tools.cat)
	cmd.Env = append(childEnv(), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		end()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		end()
		return nil, err
	}
	go func() { _ = cmd.Wait() }()
	for deadline := time.Now().Add(startWait); ; time.Sleep(20 * time.Millisecond) {
		if c, err := net.DialTimeout("unix", path, dialWait); err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			_ = stdin.Close()
			end()
			return nil, fmt.Errorf("ssh-agent did not open %s", path)
		}
	}
	return &agent{path: path, stdin: stdin, end: end, held: map[string]held{}}, nil
}

// retire lets an agent go with the keys it holds: its socket goes at once,
// the process within seconds.
func (s *Service) retire(k Kind, a *agent) {
	s.mu.Lock()
	if s.kinds[k].agent == a {
		s.kinds[k].agent = nil
	}
	first := !a.retired
	a.retired = true
	had := len(a.held) > 0
	a.held = map[string]held{}
	s.mu.Unlock()
	if !first {
		return
	}
	_ = a.stdin.Close()
	_ = os.Remove(a.path)
	a.end()
	if had {
		s.changed()
	}
}

// remove takes one key out of a kind's agent. A confirmation the agent waits
// for about that key ends first, the agent answers nothing else meanwhile.
// An agent that does not answer is let go with everything it holds.
func (s *Service) remove(k Kind, fingerprint string) bool {
	s.mu.Lock()
	kd := s.kinds[k]
	a := kd.agent
	var h held
	ok := false
	if a != nil {
		h, ok = a.held[fingerprint]
		delete(a.held, fingerprint)
	}
	if ok && kd.asking == fingerprint && kd.cancelAsk != nil {
		kd.cancelAsk()
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	if _, err := talk(a.path, appendString([]byte{msgRemoveIdentity}, h.blob)); err != nil {
		log.Printf("ssh agent: the %s agent did not forget a key, it ends: %v", k, err)
		s.retire(k, a)
	}
	return true
}

// talk sends one request of the cockpit's own to an agent and answers its
// reply. A failure in the reply is the agent's answer, not an error: a key
// it let go already is gone either way.
func talk(path string, msg []byte) ([]byte, error) {
	c, err := net.DialTimeout("unix", path, dialWait)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(talkWait))
	if err := writeMessage(c, msg); err != nil {
		return nil, err
	}
	return readMessage(c)
}

// sweepAgents clears what the agents of a process before left behind. A
// socket that still answers belongs to an agent whose cockpit just ended: it
// forgets every key at once and ends within seconds, its socket with it. A
// socket that does not answer is a leftover.
func sweepAgents(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type()&fs.ModeSocket == 0 {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if _, err := talk(path, []byte{msgRemoveAllIdentities}); err != nil {
			_ = os.Remove(path)
		}
	}
}
