package session

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/tmux"
)

// streamHub tracks the active browser streams per tmux session and owns the
// control-mode client backing each one. All state is guarded by mu.
type streamHub struct {
	cfg config.Config

	mu      sync.Mutex
	streams map[string]*streamState
}

type streamState struct {
	refs       int
	ctl        *tmux.Control
	generation int64
	snapshot   []byte
	offset     int64
	cols       int
	rows       int
}

// StreamAttachment is one active browser stream against a tmux session.
type StreamAttachment struct {
	Session    string
	Offset     int64
	Generation int64
	Snapshot   []byte
	Cols       int
	Rows       int
}

func newStreamHub(cfg config.Config) *streamHub {
	return &streamHub{cfg: cfg, streams: map[string]*streamState{}}
}

// attach opens (or reuses) the control client and returns a fresh snapshot
// together with the stream offset that immediately follows it.
func (h *streamHub) attach(name, rawCols, rawRows string) (StreamAttachment, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil {
		ctl, err := tmux.StartControl(name)
		if err != nil {
			return StreamAttachment{}, err
		}
		st = &streamState{ctl: ctl}
		h.streams[name] = st
	}
	fail := func(err error) (StreamAttachment, error) {
		if st.refs == 0 {
			if st.ctl != nil {
				st.ctl.Close()
			}
			delete(h.streams, name)
		}
		return StreamAttachment{}, err
	}
	if strings.TrimSpace(rawCols) != "" || strings.TrimSpace(rawRows) != "" {
		cols, rows, err := validateDimensions(h.cfg, rawCols, rawRows)
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
	st.refs++
	return h.attachmentLocked(name, st), nil
}

// refresh returns a new snapshot when another browser reset this stream.
func (h *streamHub) refresh(name string, generation int64) (StreamAttachment, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil || st.generation == generation {
		return StreamAttachment{}, false
	}
	return h.attachmentLocked(name, st), true
}

// resnapshot recaptures the screen when a browser has fallen out of the delta
// ring. It bumps the generation so other streams realign too.
func (h *streamHub) resnapshot(name string) (StreamAttachment, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil || st.ctl == nil {
		return StreamAttachment{}, false
	}
	snapshot, offset, err := st.ctl.Snapshot()
	if err != nil {
		return StreamAttachment{}, false
	}
	st.generation++
	st.snapshot = snapshot
	st.offset = offset
	return h.attachmentLocked(name, st), true
}

// delta returns the buffered bytes after offset. reset is true when the caller
// must re-snapshot because offset fell out of the ring.
func (h *streamHub) delta(name string, offset int64) ([]byte, int64, bool) {
	h.mu.Lock()
	var ctl *tmux.Control
	if st := h.streams[name]; st != nil {
		ctl = st.ctl
	}
	h.mu.Unlock()
	if ctl == nil {
		return nil, offset, false
	}
	return ctl.Delta(offset)
}

// resize drives the rendered size of an actively streamed session.
func (h *streamHub) resize(name string, cols, rows int) error {
	h.mu.Lock()
	var ctl *tmux.Control
	if st := h.streams[name]; st != nil {
		ctl = st.ctl
	}
	h.mu.Unlock()
	if ctl == nil {
		return nil
	}
	return ctl.Resize(cols, rows)
}

// detach releases one browser stream and closes the control client after the
// last one.
func (h *streamHub) detach(name string) {
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
func (h *streamHub) clear(name string) {
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

func (h *streamHub) attachmentLocked(name string, st *streamState) StreamAttachment {
	return StreamAttachment{
		Session:    name,
		Offset:     st.offset,
		Generation: st.generation,
		Snapshot:   st.snapshot,
		Cols:       st.cols,
		Rows:       st.rows,
	}
}

func validateDimensions(cfg config.Config, rawCols, rawRows string) (int, int, error) {
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
