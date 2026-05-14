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
// pipe-pane log rotation backing them. All state is guarded by mu.
type streamHub struct {
	cfg  config.Config
	tmux *tmux.Client
	log  *tmux.StreamLog

	mu      sync.Mutex
	streams map[string]*streamState
}

type streamState struct {
	refs       int
	path       string
	generation int64
	snapshot   []byte
	cols       int
	rows       int
}

// StreamAttachment is one active browser stream against a tmux session.
type StreamAttachment struct {
	Session    string
	Path       string
	Offset     int64
	Generation int64
	Snapshot   []byte
	Cols       int
	Rows       int
}

func newStreamHub(cfg config.Config, t *tmux.Client, log *tmux.StreamLog) *streamHub {
	return &streamHub{cfg: cfg, tmux: t, log: log, streams: map[string]*streamState{}}
}

// attach rotates the active stream log and returns the initial snapshot.
func (h *streamHub) attach(name, rawCols, rawRows string) (StreamAttachment, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil {
		st = &streamState{}
		h.streams[name] = st
	}
	if err := h.resetLocked(name, st, rawCols, rawRows); err != nil {
		if st.refs == 0 {
			delete(h.streams, name)
		}
		return StreamAttachment{}, err
	}
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

// detach releases one browser stream and stops live logging after the last one.
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
	_ = h.tmux.StopPipe(name)
	h.log.Remove(name)
}

// clear drops the stream state without touching tmux (the session is gone).
func (h *streamHub) clear(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.streams, name)
}

func (h *streamHub) resetLocked(name string, st *streamState, rawCols, rawRows string) error {
	if st.path != "" {
		_ = h.tmux.StopPipe(name)
	}
	if _, err := h.log.Truncate(name); err != nil {
		return err
	}
	if strings.TrimSpace(rawCols) != "" || strings.TrimSpace(rawRows) != "" {
		if err := h.resizeForSnapshot(name, rawCols, rawRows); err != nil {
			h.log.Remove(name)
			return err
		}
	}
	snapshot, err := h.tmux.CapturePane(name)
	if err != nil {
		h.log.Remove(name)
		return err
	}
	size, err := h.tmux.PaneSize(name)
	if err != nil {
		h.log.Remove(name)
		return err
	}
	path, err := h.log.Truncate(name)
	if err != nil {
		return err
	}
	if err := h.tmux.StartPipe(name, path); err != nil {
		h.log.Remove(name)
		return err
	}
	st.path = path
	st.generation++
	st.snapshot = snapshot
	st.cols = size.Cols
	st.rows = size.Rows
	return nil
}

// resizeForSnapshot bounces the window size so full-screen programs repaint
// before the snapshot is captured.
func (h *streamHub) resizeForSnapshot(name, rawCols, rawRows string) error {
	cols, rows, err := validateDimensions(h.cfg, rawCols, rawRows)
	if err != nil {
		return err
	}
	altCols := cols - 1
	if altCols < h.cfg.MinTerminalCols {
		altCols = cols + 1
	}
	if altCols != cols {
		if err := h.tmux.Resize(name, altCols, rows); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := h.tmux.Resize(name, cols, rows); err != nil {
		return err
	}
	time.Sleep(150 * time.Millisecond)
	return nil
}

func (h *streamHub) attachmentLocked(name string, st *streamState) StreamAttachment {
	return StreamAttachment{
		Session:    name,
		Path:       st.path,
		Offset:     0,
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
