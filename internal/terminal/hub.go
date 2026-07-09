package terminal

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/tmux"
)

// scrollLineStep is how many history lines one "line-up"/"line-down" scroll
// action moves. A wheel notch maps to a single step, so one notch scrolls
// exactly one line.
const scrollLineStep = 1

// Hub tracks the active browser streams per tmux session and owns the
// control-mode client backing each one. All state is guarded by mu.
type Hub struct {
	cfg config.Config

	mu      sync.Mutex
	streams map[string]*hubState
}

type hubState struct {
	refs       int
	ctl        *tmux.Control
	generation int64
	snapshot   []byte
	offset     int64
	cols       int
	rows       int
	scrollOff  int  // lines scrolled back into history; 0 is the live view
	frozen     bool // showing a history frame; live deltas are paused
}

// Attachment is one active browser stream against a tmux session.
type Attachment struct {
	Session       string
	Offset        int64
	Generation    int64
	Snapshot      []byte
	Cols          int
	Rows          int
	MouseTracking bool // program has mouse reporting on (wheel -> mouse events)
	MouseSGR      bool // program uses SGR mouse encoding (mode 1006)
	AltScreen     bool // alternate screen active (wheel -> cursor keys)
	AppCursor     bool // application cursor keys mode (DECCKM)
}

func NewHub(cfg config.Config) *Hub {
	return &Hub{cfg: cfg, streams: map[string]*hubState{}}
}

// attach opens (or reuses) the control client and returns a fresh snapshot
// together with the stream offset that immediately follows it.
func (h *Hub) Attach(name, rawCols, rawRows string) (Attachment, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil {
		ctl, err := tmux.StartControl(name)
		if err != nil {
			return Attachment{}, err
		}
		st = &hubState{ctl: ctl}
		h.streams[name] = st
	}
	fail := func(err error) (Attachment, error) {
		if st.refs == 0 {
			if st.ctl != nil {
				st.ctl.Close()
			}
			delete(h.streams, name)
		}
		return Attachment{}, err
	}
	if strings.TrimSpace(rawCols) != "" || strings.TrimSpace(rawRows) != "" {
		cols, rows, err := ValidateDimensions(h.cfg, rawCols, rawRows)
		if err != nil {
			return fail(err)
		}
		cur, err := st.ctl.PaneSize()
		if err != nil {
			return fail(err)
		}
		if cols != cur.Cols || rows != cur.Rows {
			if err := st.ctl.Resize(cols, rows); err != nil {
				return fail(err)
			}
			// Let the program finish repainting at the new size before capturing.
			// TUIs (e.g. Claude Code) repaint in bursts with short pauses, so we
			// wait for output to begin and then for a real quiet window, else the
			// snapshot is a half-drawn frame and the rest leaks in as deltas.
			st.ctl.Settle(300*time.Millisecond, 200*time.Millisecond, 2500*time.Millisecond)
		}
	}
	size, err := st.ctl.PaneSize()
	if err != nil {
		return fail(err)
	}
	snapshot, offset, err := st.ctl.Snapshot()
	if err != nil {
		return fail(err)
	}
	st.generation++
	st.snapshot = snapshot
	st.offset = offset
	st.cols = size.Cols
	st.rows = size.Rows
	st.frozen = false
	st.scrollOff = 0
	st.refs++
	// Wake any already-connected handlers so they pick up the new generation
	// (and the possibly changed size) without waiting for output.
	st.ctl.Notify()
	return h.attachmentLocked(name, st), nil
}

// refresh returns a new snapshot when another browser reset this stream.
func (h *Hub) Refresh(name string, generation int64) (Attachment, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil || st.generation == generation {
		return Attachment{}, false
	}
	return h.attachmentLocked(name, st), true
}

// resnapshot recaptures the screen when a browser has fallen out of the delta
// ring. It bumps the generation so other streams realign too.
func (h *Hub) Resnapshot(name string) (Attachment, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil || st.ctl == nil {
		return Attachment{}, false
	}
	snapshot, offset, err := st.ctl.Snapshot()
	if err != nil {
		return Attachment{}, false
	}
	st.generation++
	st.snapshot = snapshot
	st.offset = offset
	return h.attachmentLocked(name, st), true
}

// delta returns the buffered bytes after offset. reset is true when the caller
// must re-snapshot because offset fell out of the ring.
func (h *Hub) Delta(name string, offset int64) ([]byte, int64, bool) {
	h.mu.Lock()
	var ctl *tmux.Control
	frozen := false
	if st := h.streams[name]; st != nil {
		ctl = st.ctl
		frozen = st.frozen
	}
	h.mu.Unlock()
	if ctl == nil {
		return nil, offset, false
	}
	// A frozen stream is showing a history frame; withhold live deltas so the
	// view stays put until the user scrolls back to the bottom.
	if frozen {
		return nil, offset, false
	}
	return ctl.Delta(offset)
}

// resumeLive returns a frozen history view to the live bottom. It is a no-op
// when the stream is already live, so it is cheap to call before every keystroke.
func (h *Hub) ResumeLive(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil || st.ctl == nil || !st.frozen {
		return
	}
	snapshot, offset, err := st.ctl.Snapshot()
	if err != nil {
		return
	}
	st.frozen = false
	st.scrollOff = 0
	st.snapshot = snapshot
	st.offset = offset
	st.generation++
	st.ctl.Notify()
}

// scroll moves the stream's history view by one action and republishes the
// resulting frame to all connected browsers via a generation bump. Scrolling to
// the bottom returns to the live view and resumes deltas.
func (h *Hub) Scroll(name, action string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil || st.ctl == nil {
		return false
	}
	height := st.rows
	if height <= 0 {
		if size, err := st.ctl.PaneSize(); err == nil {
			height = size.Rows
		}
	}
	if height <= 0 {
		height = 1
	}
	hist, err := st.ctl.HistorySize()
	if err != nil {
		return false
	}
	page := height - 1
	if page < 1 {
		page = 1
	}
	off := st.scrollOff
	switch action {
	case "up":
		off += page
	case "down":
		off -= page
	case "line-up":
		off += scrollLineStep
	case "line-down":
		off -= scrollLineStep
	case "top":
		off = hist
	case "bottom":
		off = 0
	default:
		return false
	}
	if off < 0 {
		off = 0
	}
	if off > hist {
		off = hist
	}
	st.scrollOff = off
	if off == 0 {
		snapshot, offset, err := st.ctl.Snapshot()
		if err != nil {
			return false
		}
		st.frozen = false
		st.snapshot = snapshot
		st.offset = offset
	} else {
		snapshot, offset, err := st.ctl.CaptureWindow(off, height)
		if err != nil {
			return false
		}
		st.frozen = true
		st.snapshot = snapshot
		st.offset = offset
	}
	st.generation++
	st.ctl.Notify()
	return true
}

// updated returns the wake channel for a stream and whether it is still live.
func (h *Hub) Updated(name string) (<-chan struct{}, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil || st.ctl == nil {
		return nil, false
	}
	return st.ctl.Updated(), true
}

// control returns the live control client backing a stream, or nil when no
// browser is attached (or it has exited). Input uses it to send keystrokes over
// the persistent connection instead of forking the tmux CLI per key.
func (h *Hub) Control(name string) *tmux.Control {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil || st.ctl == nil || st.ctl.Exited() {
		return nil
	}
	return st.ctl
}

// exited reports whether the control client for a stream has ended.
func (h *Hub) Exited(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil || st.ctl == nil {
		return true
	}
	return st.ctl.Exited()
}

// resize drives the rendered size of an actively streamed session and
// republishes a fresh snapshot at that size. The republish matters twice: the
// cached size feeds the terminal-size event of every later resync (a stale
// cache would shrink the browser back), and programs that do not repaint on
// SIGWINCH (a plain shell prompt) would otherwise leave the old, smaller frame
// on screen until some other resync happens. Same settle-then-capture dance as
// Attach: TUIs repaint in bursts, a silent shell just lets the wait run out.
func (h *Hub) Resize(name string, cols, rows int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil || st.ctl == nil {
		return nil
	}
	if err := st.ctl.Resize(cols, rows); err != nil {
		return err
	}
	st.ctl.Settle(300*time.Millisecond, 200*time.Millisecond, 2500*time.Millisecond)
	size, err := st.ctl.PaneSize()
	if err != nil {
		return err
	}
	snapshot, offset, err := st.ctl.Snapshot()
	if err != nil {
		return err
	}
	st.generation++
	st.snapshot = snapshot
	st.offset = offset
	st.cols = size.Cols
	st.rows = size.Rows
	st.frozen = false
	st.scrollOff = 0
	st.ctl.Notify()
	return nil
}

// detach releases one browser stream and closes the control client after the
// last one.
func (h *Hub) Detach(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil {
		return
	}
	st.refs--
	if st.refs > 0 {
		return
	}
	delete(h.streams, name)
	if st.ctl != nil {
		st.ctl.Close()
	}
}

// clear drops the stream state and closes the control client (the session is
// gone).
func (h *Hub) Clear(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil {
		return
	}
	delete(h.streams, name)
	if st.ctl != nil {
		st.ctl.Close()
	}
}

func (h *Hub) attachmentLocked(name string, st *hubState) Attachment {
	var m tmux.PaneModes
	if st.ctl != nil {
		m = st.ctl.Modes()
	}
	return Attachment{
		Session:       name,
		Offset:        st.offset,
		Generation:    st.generation,
		Snapshot:      st.snapshot,
		Cols:          st.cols,
		Rows:          st.rows,
		MouseTracking: m.MouseTracking,
		MouseSGR:      m.MouseSGR,
		AltScreen:     m.AltScreen,
		AppCursor:     m.AppCursor,
	}
}

func ValidateDimensions(cfg config.Config, rawCols, rawRows string) (int, int, error) {
	cols, err := strconv.Atoi(strings.TrimSpace(rawCols))
	if err != nil {
		return 0, 0, errors.New("Terminal size must be numeric.")
	}
	rows, err := strconv.Atoi(strings.TrimSpace(rawRows))
	if err != nil {
		return 0, 0, errors.New("Terminal size must be numeric.")
	}
	if cols < cfg.MinTerminalCols {
		return 0, 0, errors.New("Terminal size is too small.")
	}
	if rows < cfg.MinTerminalRows {
		rows = cfg.MinTerminalRows
	}
	if cols > cfg.MaxTerminalCols {
		cols = cfg.MaxTerminalCols
	}
	if rows > cfg.MaxTerminalRows {
		rows = cfg.MaxTerminalRows
	}
	return cols, rows, nil
}
