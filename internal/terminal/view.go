package terminal

import (
	"sync"

	"github.com/marein/dev-cockpit/internal/tmux"
)

// View is a viewer that only reads a stream, a guest of a shared project. It
// holds a reference on the control client like a browser does and nothing
// else: it never resizes the pane, never bumps the generation and never drops
// the history frame a scrolled back browser stands on, so the browsers of the
// owner do not notice it come or go. It captures the live screen on its own
// and follows the output from there, whether the shared view is frozen or not.
type View struct {
	hub  *Hub
	name string
	// seenCols and seenRows are the size the shared view had at the last
	// capture. A change of it is a resize by a browser of the owner, and the
	// output after it no longer fits the screen this viewer holds.
	seenCols, seenRows int
	once               sync.Once
}

// Watch opens a View on the session. Without a browser attached it starts the
// control client, the size it then reads is what the shared view holds.
func (h *Hub) Watch(name string) (*View, Attachment, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[name]
	if st == nil {
		ctl, err := tmux.StartControl(name)
		if err != nil {
			return nil, Attachment{}, err
		}
		st = &hubState{ctl: ctl}
		h.streams[name] = st
	}
	a, err := captureLive(st.ctl, name)
	if err != nil {
		if st.refs == 0 {
			st.ctl.Close()
			delete(h.streams, name)
		}
		return nil, Attachment{}, err
	}
	if st.refs == 0 {
		st.cols, st.rows = a.Cols, a.Rows
	}
	st.refs++
	return &View{hub: h, name: name, seenCols: st.cols, seenRows: st.rows}, a, nil
}

// Delta returns the live output after offset, also while the shared view is
// frozen on a history frame.
func (v *View) Delta(offset int64) ([]byte, int64, bool) {
	ctl := v.control()
	if ctl == nil {
		return nil, offset, false
	}
	return ctl.Delta(offset)
}

// Resync answers a fresh capture when a browser of the owner resized the pane
// since the last one, and nothing otherwise.
func (v *View) Resync() (Attachment, bool) {
	h := v.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[v.name]
	if st == nil || st.ctl == nil || (st.cols == v.seenCols && st.rows == v.seenRows) {
		return Attachment{}, false
	}
	return v.captureLocked(st)
}

// Snapshot captures the live screen for a viewer that fell out of the delta
// ring, and leaves the shared view alone.
func (v *View) Snapshot() (Attachment, bool) {
	h := v.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[v.name]
	if st == nil || st.ctl == nil {
		return Attachment{}, false
	}
	return v.captureLocked(st)
}

// Updated returns the wake channel of the stream and whether it still lives.
func (v *View) Updated() (<-chan struct{}, bool) { return v.hub.Updated(v.name) }

// Modes reads the pane's current terminal modes.
func (v *View) Modes() (tmux.PaneModes, bool) { return v.hub.Modes(v.name) }

// Exited reports whether the control client has ended.
func (v *View) Exited() bool { return v.hub.Exited(v.name) }

// Close gives the reference back, once.
func (v *View) Close() { v.once.Do(func() { v.hub.Detach(v.name) }) }

func (v *View) control() *tmux.Control {
	v.hub.mu.Lock()
	defer v.hub.mu.Unlock()
	if st := v.hub.streams[v.name]; st != nil {
		return st.ctl
	}
	return nil
}

func (v *View) captureLocked(st *hubState) (Attachment, bool) {
	a, err := captureLive(st.ctl, v.name)
	if err != nil {
		return Attachment{}, false
	}
	v.seenCols, v.seenRows = st.cols, st.rows
	return a, true
}

// captureLive reads the pane's size, screen and modes without writing any of
// it into the shared view.
func captureLive(ctl *tmux.Control, name string) (Attachment, error) {
	size, err := ctl.PaneSize()
	if err != nil {
		return Attachment{}, err
	}
	snapshot, offset, err := ctl.Snapshot()
	if err != nil {
		return Attachment{}, err
	}
	m := ctl.Modes()
	return Attachment{
		Session:       name,
		Offset:        offset,
		Snapshot:      snapshot,
		Cols:          size.Cols,
		Rows:          size.Rows,
		MouseTracking: m.MouseTracking,
		MouseSGR:      m.MouseSGR,
		AltScreen:     m.AltScreen,
		AppCursor:     m.AppCursor,
	}, nil
}
