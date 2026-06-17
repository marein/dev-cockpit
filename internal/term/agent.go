package term

import (
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// AgentConfig configures a session agent process.
type AgentConfig struct {
	Provider string            // owning provider id (selects the runtime dir)
	Key      string            // session key; names the socket and pid files
	Workdir  string            // initial working directory for the program
	Command  string            // shell command run via `bash -lc`
	Env      map[string]string // extra environment for the program (e.g. provider env)
}

// clientSendBuffer bounds how far one client may fall behind the live output
// before it is dropped (which makes the browser reconnect and repaint) instead
// of stalling the PTY and every other client.
const clientSendBuffer = 256

// agent owns one PTY, the program running inside it, and the unix socket that
// serve (and the attach CLI) connect to.
type agent struct {
	cfg    AgentConfig
	master *os.File
	cmd    *exec.Cmd
	ln     net.Listener
	modes  modeTracker

	mu    sync.Mutex
	conns map[*clientConn]struct{}

	sockPath string
	pidPath  string
}

// clientConn is one connected reader (serve stream, or the attach CLI). Output
// is queued on send and written by a dedicated goroutine so a slow reader cannot
// block the PTY pump.
type clientConn struct {
	conn net.Conn
	send chan []byte
}

// RunAgent starts the program in a PTY and serves it until the program exits or
// a termination signal arrives. It blocks for the lifetime of the session.
func RunAgent(cfg AgentConfig) error {
	dir, err := RuntimeDir(cfg.Provider)
	if err != nil {
		return err
	}
	a := &agent{
		cfg:      cfg,
		conns:    map[*clientConn]struct{}{},
		sockPath: socketPath(dir, cfg.Key),
		pidPath:  pidPath(dir, cfg.Key),
	}
	return a.run()
}

func (a *agent) run() error {
	master, slave, err := openPTY()
	if err != nil {
		return err
	}
	a.master = master
	// A sane starting size; the first attaching client resizes immediately.
	_ = setWinsize(master, 80, 24)

	cmd := exec.Command("bash", "-lc", a.cfg.Command)
	cmd.Dir = a.cfg.Workdir
	// xterm.js is the renderer, so advertise that. A real terminal always sets
	// TERM (tmux did too); without it ncurses tools misbehave.
	cmd.Env = ensureEnv(mergeEnv(os.Environ(), a.cfg.Env), "TERM", "xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		_ = master.Close()
		return err
	}
	_ = slave.Close()
	a.cmd = cmd

	if err := a.listen(); err != nil {
		_ = a.killProgram()
		_ = master.Close()
		return err
	}
	defer a.cleanup()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		_ = a.killProgram()
	}()

	go a.acceptLoop()
	a.pumpOutput() // returns when the PTY hits EOF (program exited)
	_, _ = cmd.Process.Wait()
	return nil
}

func (a *agent) listen() error {
	// Replace any stale socket from a crashed predecessor.
	_ = os.Remove(a.sockPath)
	ln, err := net.Listen("unix", a.sockPath)
	if err != nil {
		return err
	}
	a.ln = ln
	if err := os.WriteFile(a.pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	return nil
}

func (a *agent) acceptLoop() {
	for {
		conn, err := a.ln.Accept()
		if err != nil {
			return
		}
		cc := &clientConn{conn: conn, send: make(chan []byte, clientSendBuffer)}
		a.register(cc)
		go a.writeLoop(cc)
		go a.readLoop(cc)
	}
}

// readLoop applies input frames from one client until it disconnects.
func (a *agent) readLoop(cc *clientConn) {
	defer a.unregister(cc)
	for {
		typ, payload, err := readFrame(cc.conn)
		if err != nil {
			return
		}
		a.handleFrame(typ, payload)
	}
}

// writeLoop drains queued output to one client.
func (a *agent) writeLoop(cc *clientConn) {
	for chunk := range cc.send {
		if err := writeFrame(cc.conn, frameOutput, chunk); err != nil {
			a.unregister(cc)
			return
		}
	}
}

func (a *agent) handleFrame(typ byte, payload []byte) {
	switch typ {
	case frameInput:
		_, _ = a.master.Write(payload)
	case frameKey:
		appCursor, _ := a.modes.snapshot()
		if b, ok := KeyBytes(string(payload), appCursor); ok {
			_, _ = a.master.Write(b)
		}
	case framePaste:
		_, bracketed := a.modes.snapshot()
		_, _ = a.master.Write(bracketedPaste(string(payload), bracketed))
	case frameResize:
		if cols, rows, ok := parseResize(payload); ok {
			_ = setWinsize(a.master, cols, rows)
		}
	case frameRedraw:
		a.redraw()
	}
}

// redraw forces the foreground program to repaint by toggling the window size,
// which delivers SIGWINCH. Full-screen TUIs redraw the whole screen in response,
// so a freshly attached client converges on the correct frame without a capture.
func (a *agent) redraw() {
	cols, rows, err := getWinsize(a.master)
	if err != nil || cols <= 0 || rows <= 0 {
		return
	}
	_ = setWinsize(a.master, cols, rows+1)
	// A brief gap so a line-based shell processes the first SIGWINCH (and
	// repaints) before the second, instead of coalescing them into a no-op.
	// Full-screen TUIs repaint on either edge regardless.
	time.Sleep(15 * time.Millisecond)
	_ = setWinsize(a.master, cols, rows)
}

func (a *agent) pumpOutput() {
	buf := make([]byte, 32*1024)
	for {
		n, err := a.master.Read(buf)
		if n > 0 {
			a.modes.feed(buf[:n])
			a.broadcast(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// broadcast fans one output chunk out to every client. A client whose buffer is
// full is dropped (its browser reconnects and repaints) rather than allowed to
// stall the pump.
func (a *agent) broadcast(chunk []byte) {
	cp := make([]byte, len(chunk))
	copy(cp, chunk)
	var drop []*clientConn
	a.mu.Lock()
	for cc := range a.conns {
		select {
		case cc.send <- cp:
		default:
			drop = append(drop, cc)
		}
	}
	a.mu.Unlock()
	for _, cc := range drop {
		a.unregister(cc)
	}
}

func (a *agent) register(cc *clientConn) {
	a.mu.Lock()
	a.conns[cc] = struct{}{}
	a.mu.Unlock()
}

func (a *agent) unregister(cc *clientConn) {
	a.mu.Lock()
	if _, ok := a.conns[cc]; !ok {
		a.mu.Unlock()
		return
	}
	delete(a.conns, cc)
	close(cc.send)
	a.mu.Unlock()
	_ = cc.conn.Close()
}

// killProgram terminates the program with SIGHUP — the hangup a closing
// terminal delivers, which even SIGTERM-ignoring interactive shells obey — and
// escalates to SIGKILL if it has not exited shortly after. Setsid made the
// program a process-group leader, so the whole tree is signalled.
func (a *agent) killProgram() error {
	if a.cmd == nil || a.cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(a.cmd.Process.Pid)
	if err != nil {
		_ = a.cmd.Process.Signal(syscall.SIGHUP)
		return nil
	}
	_ = syscall.Kill(-pgid, syscall.SIGHUP)
	go func() {
		time.Sleep(3 * time.Second)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}()
	return nil
}

func (a *agent) cleanup() {
	if a.ln != nil {
		_ = a.ln.Close()
	}
	_ = os.Remove(a.sockPath)
	_ = os.Remove(a.pidPath)
	if a.master != nil {
		_ = a.master.Close()
	}
}

func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	out := make([]string, len(base), len(base)+len(extra))
	copy(out, base)
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}

// ensureEnv appends key=value only if key is not already set.
func ensureEnv(env []string, key, value string) []string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return env
		}
	}
	return append(env, prefix+value)
}
