package term

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// ErrNotRunning is returned when no live agent backs a session key.
var ErrNotRunning = errors.New("session is not running")

// Client is the serve-side handle to the per-provider set of session agents.
type Client struct {
	provider string
	dir      string
	self     string
}

// SpawnConfig describes a new agent to launch.
type SpawnConfig struct {
	Key     string
	Workdir string
	Command string
	Env     map[string]string
}

// NewClient resolves the runtime directory and the path of the current binary
// (used to spawn agent subprocesses).
func NewClient(provider string) (*Client, error) {
	dir, err := RuntimeDir(provider)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &Client{provider: provider, dir: dir, self: self}, nil
}

// Spawn launches a detached agent for the session and blocks until its socket
// is accepting connections.
func (c *Client) Spawn(cfg SpawnConfig) error {
	args := []string{
		"session-agent",
		"--provider", c.provider,
		"--key", cfg.Key,
		"--workdir", cfg.Workdir,
		"--command", cfg.Command,
	}
	for k, v := range cfg.Env {
		args = append(args, "--env", k+"="+v)
	}
	cmd := exec.Command(c.self, args...)
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if logf, err := os.OpenFile(c.dir+"/"+cfg.Key+".log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		cmd.Stdout = logf
		cmd.Stderr = logf
		defer logf.Close()
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap if it dies while serve is alive

	deadline := time.Now().Add(10 * time.Second)
	for {
		if conn, err := net.Dial("unix", socketPath(c.dir, cfg.Key)); err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("Timed out starting the session.")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Stream is a live attachment to a session: raw program output arrives on Output
// until the session ends, at which point the channel is closed.
type Stream struct {
	conn net.Conn
	out  chan []byte
}

// Output yields raw PTY bytes; it is closed when the session ends or the stream
// is closed.
func (s *Stream) Output() <-chan []byte { return s.out }

// Close detaches this stream. The session keeps running.
func (s *Stream) Close() { _ = s.conn.Close() }

// OpenStream attaches to a session, sets the requested size, and asks the
// program to repaint so the new viewport converges on the current screen.
func (c *Client) OpenStream(key string, cols, rows int) (*Stream, error) {
	conn, err := c.dial(key)
	if err != nil {
		return nil, err
	}
	if cols > 0 && rows > 0 {
		if err := writeFrame(conn, frameResize, resizePayload(cols, rows)); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	if err := writeFrame(conn, frameRedraw, nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	s := &Stream{conn: conn, out: make(chan []byte, 256)}
	go s.readLoop()
	return s, nil
}

func (s *Stream) readLoop() {
	defer close(s.out)
	for {
		typ, payload, err := readFrame(s.conn)
		if err != nil {
			return
		}
		if typ == frameOutput && len(payload) > 0 {
			s.out <- payload
		}
	}
}

// Send delivers a batch of input frames over a single connection.
func (c *Client) Send(key string, frames []InFrame) error {
	conn, err := c.dial(key)
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, f := range frames {
		if err := writeFrame(conn, f.typ, f.data); err != nil {
			return err
		}
	}
	return nil
}

// Resize sets the program's terminal size.
func (c *Client) Resize(key string, cols, rows int) error {
	conn, err := c.dial(key)
	if err != nil {
		return err
	}
	defer conn.Close()
	return writeFrame(conn, frameResize, resizePayload(cols, rows))
}

// Kill terminates the agent (and the program inside it).
func (c *Client) Kill(key string) error {
	pid, ok := c.readPID(key)
	if !ok {
		return ErrNotRunning
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	return nil
}

// Rename moves a session's socket and pid files to a new key. The listening
// agent keeps serving under the new path.
func (c *Client) Rename(oldKey, newKey string) error {
	if oldKey == newKey {
		return nil
	}
	if err := os.Rename(socketPath(c.dir, oldKey), socketPath(c.dir, newKey)); err != nil {
		return err
	}
	_ = os.Rename(pidPath(c.dir, oldKey), pidPath(c.dir, newKey))
	return nil
}

func (c *Client) dial(key string) (net.Conn, error) {
	conn, err := net.Dial("unix", socketPath(c.dir, key))
	if err != nil {
		return nil, ErrNotRunning
	}
	return conn, nil
}

func (c *Client) readPID(key string) (int, bool) {
	data, err := os.ReadFile(pidPath(c.dir, key))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(string(trimSpace(data)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}

// InFrame is one queued input action for Send.
type InFrame struct {
	typ  byte
	data []byte
}

// KeyFrame sends a named key (mode-aware translation happens in the agent).
func KeyFrame(name string) InFrame { return InFrame{typ: frameKey, data: []byte(name)} }

// TextFrame writes literal bytes to the program.
func TextFrame(text string) InFrame { return InFrame{typ: frameInput, data: []byte(text)} }

// PasteFrame pastes text (bracketed when the program asked for it).
func PasteFrame(text string) InFrame { return InFrame{typ: framePaste, data: []byte(text)} }
