package sshagent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

// The sockets live in a directory of their own that carries the permission,
// like the local API's. The whole path of the longest one, an agent's, has to
// fit a socket address.
const (
	socketDirName = "ssh-agent"
	maxSocketPath = 100
	longestSocket = agentsDirName + "/" + string(KindAssistant) + "-01234567"
)

func socketDir(stateDir string) string {
	dir := filepath.Join(filesystem.AbsDir(stateDir), socketDirName)
	if len(filepath.Join(dir, longestSocket)) <= maxSocketPath {
		return dir
	}
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join(os.TempDir(), "dev-cockpit-ssh-agent-"+hex.EncodeToString(sum[:8]))
}

// Socket answers the path of a kind's socket, empty while the cockpit serves
// no agent of its own. It is the same in every process of the cockpit.
func (s *Service) Socket(k Kind) string {
	if !s.Active() {
		return ""
	}
	return filepath.Join(s.dir, string(k))
}

// KindOf answers the kind whose socket a path names, for a caller that hands
// its SSH_AUTH_SOCK on.
func (s *Service) KindOf(path string) (Kind, bool) {
	if !s.Active() {
		return "", false
	}
	return s.kindAt(path)
}

func (s *Service) kindAt(path string) (Kind, bool) {
	if path == "" {
		return "", false
	}
	for _, k := range Kinds {
		if filepath.Clean(path) == filepath.Join(s.dir, string(k)) {
			return k, true
		}
	}
	return "", false
}

// Start closes this process to the user's other processes, passphrases pass
// through it, and listens on the socket of every kind, before the first
// session starts. Every socket of a kind still in the directory belongs to a
// process that is gone. The agents start on their first use. Where the
// directory cannot be made the cockpit's own, it runs no agent.
func (s *Service) Start() {
	if !s.Active() {
		return
	}
	if err := protectMemory(); err != nil {
		log.Printf("ssh agent: the memory of this process stays readable for the user's other processes: %v", err)
	}
	if err := s.prepareDir(); err != nil {
		log.Printf("ssh agent: off, %v", err)
		s.usable = false
		return
	}
	for _, k := range Kinds {
		if err := s.listen(k); err != nil {
			log.Printf("ssh agent: the %s socket: %v", k, err)
		}
	}
}

func (s *Service) prepareDir() error {
	if err := filesystem.PrivateDir(s.dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Type()&fs.ModeSocket != 0 {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
	agents := filepath.Join(s.dir, agentsDirName)
	if err := filesystem.PrivateDir(agents); err != nil {
		return err
	}
	sweepAgents(agents)
	return nil
}

func (s *Service) listen(k Kind) error {
	path := filepath.Join(s.dir, string(k))
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	s.mu.Lock()
	s.listeners[k] = ln
	s.mu.Unlock()
	go s.serve(k, ln)
	return nil
}

func (s *Service) serve(k Kind, ln net.Listener) {
	for {
		c, err := ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			log.Printf("ssh agent %s: %v", k, err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go s.serveConn(k, c)
	}
}
