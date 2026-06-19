package tmux

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// controlRingMax bounds the in-memory delta buffer per session. A browser that
// falls further behind than this triggers a fresh snapshot instead of a delta.
const controlRingMax = 1 << 22 // 4 MiB

// controlCmdTimeout bounds how long we wait for a control-mode command reply.
const controlCmdTimeout = 10 * time.Second

// Control is a tmux control-mode client (`tmux -C attach-session`) for one
// session. It feeds raw pane output into an in-memory ring and answers capture
// requests on the same ordered channel, so a snapshot and the byte offset that
// follows it are taken atomically — no pipe-pane log and no snapshot/stream
// seam where output can be lost or duplicated.
type Control struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *bytes.Buffer

	sendMu sync.Mutex // serializes command writes so replies stay FIFO-ordered

	mu      sync.Mutex
	buf     []byte
	base    int64 // absolute offset of buf[0]
	paneID  string
	pending []*cmdReq
	exited  bool

	lastOutput time.Time
	outSeq     int64 // total bytes ever received (monotonic, ignores ring trim)

	// notify is a broadcast channel: it is closed (and replaced) on every new
	// output or on exit, so any number of waiting stream handlers wake at once.
	notify chan struct{}

	ready chan struct{}
}

type cmdReq struct {
	lines  [][]byte
	offset int64
	err    error
	isErr  bool
	done   chan struct{}
}

// StartControl spawns the control-mode client and blocks until it is ready.
func StartControl(name string) (*Control, error) {
	cmd := exec.Command("tmux", "-C", "attach-session", "-t", name)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Control{
		name:   name,
		cmd:    cmd,
		stdin:  stdin,
		stderr: stderr,
		notify: make(chan struct{}),
		ready:  make(chan struct{}),
	}
	go c.read(stdout)

	select {
	case <-c.ready:
	case <-time.After(controlCmdTimeout):
		c.Close()
		return nil, errors.New("Timed out attaching to terminal.")
	}
	c.mu.Lock()
	exited := c.exited
	c.mu.Unlock()
	if exited {
		msg := strings.TrimSpace(c.stderr.String())
		if msg == "" {
			msg = "Failed to attach to terminal."
		}
		return nil, errors.New(msg)
	}
	if lines, _, err := c.commandSync("display-message -p -t " + Target(name) + ` "#{pane_id}"`); err == nil && len(lines) > 0 {
		c.mu.Lock()
		c.paneID = strings.TrimSpace(string(lines[0]))
		c.mu.Unlock()
	}
	return c, nil
}

func (c *Control) read(stdout io.Reader) {
	rd := bufio.NewReader(stdout)
	var (
		inBlock bool
		mine    bool
		block   [][]byte
	)
	for {
		raw, err := rd.ReadString('\n')
		if err != nil {
			if len(raw) == 0 {
				break
			}
		}
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		if inBlock {
			if strings.HasPrefix(line, "%end ") || strings.HasPrefix(line, "%error ") {
				c.finishBlock(block, mine, strings.HasPrefix(line, "%error "))
				inBlock, mine, block = false, false, nil
			} else {
				block = append(block, []byte(line))
			}
			if err != nil {
				break
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "%begin "):
			inBlock = true
			mine = controlFlag(line) == "1"
			block = nil
		case strings.HasPrefix(line, "%output "):
			c.appendOutput(line)
		case line == "%exit" || strings.HasPrefix(line, "%exit "):
			c.markExited()
		case strings.HasPrefix(line, "%session-changed"):
			c.signalReady()
		}
		if err != nil {
			break
		}
	}
	c.markExited()
}

func (c *Control) signalReady() {
	select {
	case <-c.ready:
	default:
		close(c.ready)
	}
}

func (c *Control) finishBlock(lines [][]byte, mine, isErr bool) {
	if !mine {
		return
	}
	c.mu.Lock()
	var req *cmdReq
	if len(c.pending) > 0 {
		req = c.pending[0]
		c.pending = c.pending[1:]
	}
	offset := c.base + int64(len(c.buf))
	c.mu.Unlock()
	if req == nil {
		return
	}
	req.lines = lines
	req.offset = offset
	req.isErr = isErr
	if isErr {
		req.err = errors.New(strings.TrimSpace(string(bytes.Join(lines, []byte(" ")))))
	}
	close(req.done)
}

func (c *Control) appendOutput(line string) {
	rest := line[len("%output "):]
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return
	}
	pane, data := rest[:sp], rest[sp+1:]
	c.mu.Lock()
	if c.paneID != "" && pane != c.paneID {
		c.mu.Unlock()
		return
	}
	decoded := decodeControlData(data)
	c.buf = append(c.buf, decoded...)
	c.outSeq += int64(len(decoded))
	c.lastOutput = time.Now()
	if len(c.buf) > controlRingMax {
		drop := len(c.buf) - controlRingMax
		c.base += int64(drop)
		c.buf = append(c.buf[:0], c.buf[drop:]...)
	}
	c.signalLocked()
	c.mu.Unlock()
}

// signalLocked wakes every waiter by closing the current notify channel and
// installing a fresh one. Callers must hold c.mu.
func (c *Control) signalLocked() {
	close(c.notify)
	c.notify = make(chan struct{})
}

// Updated returns a channel that is closed on the next output or on exit.
// Subscribe (call Updated) before reading state, then select on the result, so
// no wake is missed between a read and the wait.
func (c *Control) Updated() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.notify
}

// Notify wakes all waiters explicitly (e.g. after a resize changed the pane).
func (c *Control) Notify() {
	c.mu.Lock()
	c.signalLocked()
	c.mu.Unlock()
}

// Exited reports whether the control client has detached or the session ended.
func (c *Control) Exited() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exited
}

func (c *Control) markExited() {
	c.mu.Lock()
	if c.exited {
		c.mu.Unlock()
		return
	}
	c.exited = true
	pending := c.pending
	c.pending = nil
	c.signalLocked()
	c.mu.Unlock()
	for _, req := range pending {
		req.err = errors.New("Terminal connection closed.")
		close(req.done)
	}
	c.signalReady()
}

// command registers a waiter and writes the command, keeping the pending queue
// in the same order as the writes so replies match up.
func (c *Control) command(cmd string) (*cmdReq, error) {
	req := &cmdReq{done: make(chan struct{})}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.Lock()
	if c.exited {
		c.mu.Unlock()
		return nil, errors.New("Terminal connection closed.")
	}
	c.pending = append(c.pending, req)
	c.mu.Unlock()
	if _, err := io.WriteString(c.stdin, cmd+"\n"); err != nil {
		return nil, err
	}
	return req, nil
}

func (c *Control) await(req *cmdReq) ([][]byte, int64, error) {
	select {
	case <-req.done:
		return req.lines, req.offset, req.err
	case <-time.After(controlCmdTimeout):
		return nil, 0, errors.New("Terminal command timed out.")
	}
}

func (c *Control) commandSync(cmd string) ([][]byte, int64, error) {
	req, err := c.command(cmd)
	if err != nil {
		return nil, 0, err
	}
	return c.await(req)
}

// Snapshot captures the current screen and the stream offset that immediately
// follows it. Bytes after the offset are delivered as deltas, so nothing is
// lost or shown twice across the snapshot boundary.
func (c *Control) Snapshot() ([]byte, int64, error) {
	capReq, err := c.command("capture-pane -p -e -t " + Target(c.name) + " -S 0 -E -")
	if err != nil {
		return nil, 0, err
	}
	curReq, err := c.command("display-message -p -t " + Target(c.name) + ` "#{cursor_x} #{cursor_y} #{cursor_flag}"`)
	if err != nil {
		return nil, 0, err
	}
	capLines, offset, err := c.await(capReq)
	if err != nil {
		return nil, 0, err
	}
	curLines, _, _ := c.await(curReq)
	var cursor []string
	if len(curLines) > 0 {
		cursor = strings.Fields(string(curLines[0]))
	}
	return buildSnapshot(bytes.Join(capLines, []byte("\r\n")), cursor), offset, nil
}

// HistorySize reports how many lines of scrollback sit above the visible pane.
func (c *Control) HistorySize() (int, error) {
	lines, _, err := c.commandSync("display-message -p -t " + Target(c.name) + ` "#{history_size}"`)
	if err != nil {
		return 0, err
	}
	if len(lines) == 0 {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(lines[0])))
	if err != nil {
		return 0, err
	}
	return n, nil
}

// CaptureWindow captures height lines of the pane starting off lines back in the
// scrollback (off 0 is the live screen), for history scrolling. It returns the
// frame and the stream offset that immediately follows it. The frame carries no
// cursor — a scrolled history view has none.
func (c *Control) CaptureWindow(off, height int) ([]byte, int64, error) {
	if height < 1 {
		height = 1
	}
	start := -off
	end := start + height - 1
	capReq, err := c.command(fmt.Sprintf("capture-pane -p -e -t %s -S %d -E %d", Target(c.name), start, end))
	if err != nil {
		return nil, 0, err
	}
	capLines, offset, err := c.await(capReq)
	if err != nil {
		return nil, 0, err
	}
	return buildSnapshot(bytes.Join(capLines, []byte("\r\n")), nil), offset, nil
}

// Delta returns the buffered bytes after offset and the new offset. reset is
// true when offset has fallen out of the ring and the caller must re-snapshot.
func (c *Control) Delta(offset int64) ([]byte, int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	end := c.base + int64(len(c.buf))
	if offset < c.base {
		return nil, offset, true
	}
	if offset >= end {
		return nil, end, false
	}
	out := make([]byte, end-offset)
	copy(out, c.buf[offset-c.base:])
	return out, end, false
}

// Resize drives the rendered pane size through this control client.
func (c *Control) Resize(cols, rows int) error {
	_, _, err := c.commandSync(fmt.Sprintf("refresh-client -C %d,%d", cols, rows))
	return err
}

// Settle waits for the repaint a resize triggers to begin and then go quiet, so
// a following Snapshot captures a finished frame instead of a half-drawn one.
// Phase one waits (up to startWait) for the program to start repainting; phase
// two waits for output to fall quiet, bounded by maxWait. A program that does
// not repaint on resize simply returns after startWait.
func (c *Control) Settle(startWait, quiet, maxWait time.Duration) {
	start := time.Now()
	c.mu.Lock()
	baseline := c.outSeq
	c.mu.Unlock()
	for {
		c.mu.Lock()
		seq := c.outSeq
		c.mu.Unlock()
		if seq > baseline {
			break
		}
		if time.Since(start) >= startWait {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	for {
		c.mu.Lock()
		last := c.lastOutput
		c.mu.Unlock()
		if time.Since(last) >= quiet || time.Since(start) >= maxWait {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// PaneSize reports the current pane dimensions.
func (c *Control) PaneSize() (Size, error) {
	lines, _, err := c.commandSync("display-message -p -t " + Target(c.name) + ` "#{pane_width} #{pane_height}"`)
	if err != nil {
		return Size{}, err
	}
	if len(lines) == 0 {
		return Size{}, errors.New("Failed to read terminal size.")
	}
	fields := strings.Fields(string(lines[0]))
	if len(fields) != 2 {
		return Size{}, errors.New("Failed to parse terminal size.")
	}
	cols, err := strconv.Atoi(fields[0])
	if err != nil {
		return Size{}, err
	}
	rows, err := strconv.Atoi(fields[1])
	if err != nil {
		return Size{}, err
	}
	return Size{Cols: cols, Rows: rows}, nil
}

// PaneModes are terminal modes the browser needs to reproduce wheel scrolling
// the way the program expects. They matter most when attaching to an already
// running program, whose mode-set sequences are not part of a screen snapshot,
// so the browser can't learn them by replaying the stream.
type PaneModes struct {
	MouseTracking bool // mouse reporting on (wheel -> mouse events, e.g. claude)
	MouseSGR      bool // SGR mouse encoding (mode 1006)
	AltScreen     bool // alternate screen active (full-screen TUI -> cursor keys)
	AppCursor     bool // application cursor keys (DECCKM: ESC O A/B vs ESC [ A/B)
}

// Modes reads the pane's current terminal modes from tmux. The browser keeps
// xterm's own mouse reporting disabled (to preserve text selection), so it uses
// these to synthesize the wheel input each program expects.
func (c *Control) Modes() PaneModes {
	lines, _, err := c.commandSync("display-message -p -t " + Target(c.name) +
		` "#{mouse_any_flag} #{mouse_sgr_flag} #{alternate_on} #{keypad_cursor_flag}"`)
	if err != nil || len(lines) == 0 {
		return PaneModes{}
	}
	f := strings.Fields(string(lines[0]))
	if len(f) != 4 {
		return PaneModes{}
	}
	return PaneModes{
		MouseTracking: f[0] == "1",
		MouseSGR:      f[1] == "1",
		AltScreen:     f[2] == "1",
		AppCursor:     f[3] == "1",
	}
}

// Close detaches the control client; the tmux session itself is untouched.
func (c *Control) Close() error {
	c.sendMu.Lock()
	_ = c.stdin.Close()
	c.sendMu.Unlock()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
	return nil
}

func controlFlag(line string) string {
	fields := strings.Fields(line)
	if len(fields) >= 4 {
		return fields[3]
	}
	return ""
}

// decodeControlData expands tmux's octal escapes (e.g. \015\012\033) in
// %output payloads back into raw bytes.
func decodeControlData(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			out = append(out, (s[i+1]-'0')<<6|(s[i+2]-'0')<<3|(s[i+3]-'0'))
			i += 4
			continue
		}
		out = append(out, s[i])
		i++
	}
	return out
}

func isOctal(b byte) bool { return b >= '0' && b <= '7' }

// buildSnapshot strips OSC/title noise and positions the cursor where tmux
// reports it. The client renders the visible cursor itself (renderer-independent
// overlay), so this only restores the cursor's cell position from the capture.
func buildSnapshot(rawGrid []byte, cursor []string) []byte {
	out := stripSnapshotCursor(stripOSC(rawGrid))
	if len(cursor) < 2 {
		return out
	}
	x, xErr := strconv.Atoi(cursor[0])
	y, yErr := strconv.Atoi(cursor[1])
	if xErr != nil || yErr != nil {
		return out
	}
	return append(out, []byte(fmt.Sprintf("\x1b[%d;%dH", y+1, x+1))...)
}

func stripSnapshotCursor(src []byte) []byte {
	return bytes.ReplaceAll(src, []byte("\x1b[7m \x1b[0m"), []byte(" "))
}
