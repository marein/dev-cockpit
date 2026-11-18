package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/marein/dev-cockpit/internal/proctree"
	"github.com/marein/dev-cockpit/plugin"
)

// Supervision timing. A plugin has startBudget to answer HTTP on its socket.
// A running one is probed every probeEvery, each probe waiting probeTimeout,
// and ended after probeMisses misses in a row. A restart backs off from
// firstBackoff, doubling up to maxBackoff, reset by a run of resetAfter.
// stopGrace is the wait from SIGTERM to SIGKILL. Variables, so tests can
// shorten them.
var (
	startBudget  = 15 * time.Second
	probeEvery   = 10 * time.Second
	probeTimeout = 3 * time.Second
	firstBackoff = time.Second
	maxBackoff   = time.Minute
	resetAfter   = time.Minute
	stopGrace    = 3 * time.Second
)

const (
	probeMisses = 2
	logLines    = 200
)

// probePath is the liveness probe. Any HTTP answer counts, a 404 included.
const probePath = "/.dev-cockpit/probe"

// runEnv carries a per run nonce every process of a plugin's group inherits.
// After a cockpit restart it proves that the group a pid file names is still
// the plugin's.
const runEnv = "DEV_COCKPIT_PLUGIN_RUN"

// supervisor runs one external plugin's process in its own process group,
// probes it and restarts it with backoff. It also serves the callback socket
// and proxies the plugin's routes.
type supervisor struct {
	host     *Host
	id       string
	name     string
	dir      string
	run      string
	runDir   string
	env      []string
	log      *ring
	callback http.Handler

	mu        sync.Mutex
	state     State
	reason    string
	token     string
	transport *http.Transport
	streams   map[chan plugin.Event]struct{}
	cancel    context.CancelFunc
	done      chan struct{}
}

func newSupervisor(h *Host, m *Manifest, dir, runDir, pluginStateDir string) *supervisor {
	s := &supervisor{
		host:   h,
		id:     m.ID,
		name:   m.Name,
		dir:    dir,
		run:    filepath.Join(dir, m.Run),
		runDir: runDir,
		env:    pluginEnv(h, m, dir, runDir, pluginStateDir),
		log:    &ring{},
		state:  StateStarting,
	}
	s.callback = s.callbackHandler(pluginStateDir)
	return s
}

// begin starts the supervision. halt ends it with the process group and
// returns once both are gone.
func (s *supervisor) begin() {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.done = make(chan struct{})
	s.mu.Unlock()
	go s.supervise(ctx)
}

func (s *supervisor) halt() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (s *supervisor) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == StateRunning
}

func (s *supervisor) snapshot() (State, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.reason
}

func (s *supervisor) set(state State, reason string) {
	s.mu.Lock()
	moved := s.state != state || s.reason != reason
	s.state, s.reason = state, reason
	s.mu.Unlock()
	if moved {
		if state == StateCrashed {
			log.Printf("plugin %s: %s", s.id, reason)
		}
		s.host.changed()
	}
}

func (s *supervisor) supervise(ctx context.Context) {
	defer close(s.done)
	cockpitSocket := filepath.Join(s.runDir, "c")
	_ = os.Remove(cockpitSocket)
	listener, err := net.Listen("unix", cockpitSocket)
	if err != nil {
		s.set(StateCrashed, "Its callback socket could not be opened.")
		<-ctx.Done()
		return
	}
	server := &http.Server{Handler: s.callback, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		_ = server.Close()
		_ = os.Remove(cockpitSocket)
	}()
	backoff := firstBackoff
	for {
		began := time.Now()
		reason := s.once(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(began) > resetAfter {
			backoff = firstBackoff
		}
		s.set(StateCrashed, reason)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, maxBackoff)
	}
}

// once runs the plugin's process one time and answers why it ended, "" when
// the supervision was stopped.
func (s *supervisor) once(ctx context.Context) string {
	socket := filepath.Join(s.runDir, "p")
	_ = os.Remove(socket)
	nonce := randomHex(8)
	s.mu.Lock()
	s.token = randomHex(32)
	token := s.token
	s.mu.Unlock()
	s.set(StateStarting, "")
	cmd := exec.Command(s.run)
	cmd.Dir = s.dir
	cmd.Env = append(slices.Clone(s.env), "DEV_COCKPIT_TOKEN="+token, runEnv+"="+nonce)
	cmd.Stdout = s.log
	cmd.Stderr = s.log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return fmt.Sprintf("It could not start: %v.", err)
	}
	group := cmd.Process.Pid
	pidFile := filepath.Join(s.runDir, "pid")
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d %s\n", group, nonce)), 0o600); err != nil {
		log.Printf("plugin %s: its pid file could not be written: %v", s.id, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	transport := plugTransport(socket)
	defer func() {
		s.mu.Lock()
		s.transport = nil
		s.mu.Unlock()
		transport.CloseIdleConnections()
		_ = os.Remove(pidFile)
		_ = os.Remove(socket)
	}()

	deadline := time.Now().Add(startBudget)
	for probe(ctx, transport) != nil {
		if time.Now().After(deadline) {
			endGroup(group, exited)
			return fmt.Sprintf("It did not answer HTTP on its socket within %s.", startBudget)
		}
		select {
		case err := <-exited:
			endGroup(group, nil)
			return exitReason(err)
		case <-ctx.Done():
			endGroup(group, exited)
			return ""
		case <-time.After(100 * time.Millisecond):
		}
	}
	s.mu.Lock()
	s.transport = transport
	s.mu.Unlock()
	s.set(StateRunning, "")

	ticker := time.NewTicker(probeEvery)
	defer ticker.Stop()
	misses := 0
	for {
		select {
		case err := <-exited:
			endGroup(group, nil)
			return exitReason(err)
		case <-ctx.Done():
			endGroup(group, exited)
			return ""
		case <-ticker.C:
			if probe(ctx, transport) == nil {
				misses = 0
				continue
			}
			if misses++; misses >= probeMisses {
				endGroup(group, exited)
				return "It stopped answering and was terminated."
			}
		}
	}
}

// plugTransport dials the plugin's socket. Only the response header has a
// timeout, a body may stream indefinitely.
func plugTransport(socket string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", socket)
		},
		ResponseHeaderTimeout:  30 * time.Second,
		MaxIdleConnsPerHost:    4,
		IdleConnTimeout:        30 * time.Second,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 64 << 10,
	}
}

// probe asks the plugin once whether it answers HTTP.
func probe(ctx context.Context, transport *http.Transport) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://plugin"+probePath, nil)
	if err != nil {
		return err
	}
	res, err := transport.RoundTrip(req)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

// endGroup ends a plugin's process group through termGroup, then waits up to
// stopGrace for the leader's Wait on exited, so a stop takes only as long as
// the plugin needs. Pass exited nil once it was read, it answers only once.
func endGroup(group int, exited <-chan error) {
	termGroup(group)
	if exited != nil {
		select {
		case <-exited:
		case <-time.After(stopGrace):
		}
	}
}

// termGroup sends a process group SIGTERM, and SIGKILL when a member is left
// after stopGrace.
func termGroup(group int) {
	_ = syscall.Kill(-group, syscall.SIGTERM)
	deadline := time.Now().Add(stopGrace)
	for syscall.Kill(-group, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(-group, syscall.SIGKILL)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func exitReason(err error) string {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return "It exited."
	case errors.As(err, &exit):
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return fmt.Sprintf("It was killed by signal %s.", status.Signal())
		}
		return fmt.Sprintf("It exited with status %d.", exit.ExitCode())
	case errors.Is(err, exec.ErrWaitDelay):
		return "It exited, but a child process kept its output open."
	default:
		return fmt.Sprintf("It ended: %v.", err)
	}
}

// Sweep ends the plugin groups an earlier cockpit process on the same state
// directory left running. The serving unit keeps children across a restart
// and a self-update execs the new image without ending them, so a remaining
// pid file names a group that would otherwise run twice. A group is ended
// only when its leader still carries the run's nonce, or when the leader is
// gone: the remaining members then hold the group id, no other process can.
func (h *Host) Sweep() {
	root := runRoot(h.cfg.StateDir)
	dirs, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var tree *proctree.Tree
	for _, d := range dirs {
		pidFile := filepath.Join(root, d.Name(), "pid")
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		fields := strings.Fields(string(raw))
		if len(fields) != 2 {
			_ = os.Remove(pidFile)
			continue
		}
		group, err := strconv.Atoi(fields[0])
		if err != nil || group <= 1 {
			_ = os.Remove(pidFile)
			continue
		}
		if syscall.Kill(group, 0) == nil {
			if tree == nil {
				tree = proctree.Capture()
			}
			leader, err := syscall.Getpgid(group)
			if err != nil || leader != group || tree.Environ(group)[runEnv] != fields[1] {
				_ = os.Remove(pidFile)
				continue
			}
		}
		log.Printf("plugin %s: ending what the previous cockpit process left running", d.Name())
		endOrphan(group)
		_ = os.Remove(pidFile)
	}
}

func endOrphan(group int) {
	// After a self-update the leader may be an unwaited child of this
	// process. Reap it, as a zombie it would keep its group alive and the
	// stop would wait out the grace for every plugin before the cockpit
	// listens.
	go func() { _, _ = syscall.Wait4(group, nil, 0, nil) }()
	termGroup(group)
}

// ring keeps the last logLines lines of a plugin's output, each cut to
// maxLogLine bytes.
type ring struct {
	mu      sync.Mutex
	lines   []string
	partial []byte
}

const maxLogLine = 1000

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	data := append(r.partial, p...)
	for {
		i := slices.Index(data, '\n')
		if i < 0 {
			break
		}
		r.add(data[:i])
		data = data[i+1:]
	}
	if len(data) > maxLogLine {
		r.add(data)
		data = nil
	}
	r.partial = append([]byte(nil), data...)
	return len(p), nil
}

func (r *ring) add(line []byte) {
	if len(line) > maxLogLine {
		line = line[:maxLogLine]
	}
	text := strings.ToValidUTF8(strings.TrimRight(string(line), "\r"), "?")
	r.lines = append(r.lines, text)
	if len(r.lines) > logLines {
		r.lines = slices.Delete(r.lines, 0, len(r.lines)-logLines)
	}
}

// Tail answers the last n lines.
func (r *ring) Tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	lines := r.lines
	if len(r.partial) > 0 {
		lines = append(slices.Clone(lines), string(r.partial))
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return slices.Clone(lines)
}
