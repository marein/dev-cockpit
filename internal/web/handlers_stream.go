package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/terminal"
	"github.com/local/dev-cockpit/internal/tmux"
)

// terminalStream is the streaming surface shared by coder sessions and shells.
type terminalStream interface {
	Resolve(id string) error
	AttachStream(id, cols, rows string) (terminal.Attachment, error)
	DetachStream(name string)
	RefreshStream(name string, generation int64) (terminal.Attachment, bool)
	StreamDelta(name string, offset int64) ([]byte, int64, bool)
	StreamUpdated(name string) (<-chan struct{}, bool)
	StreamExited(name string) bool
	Resnapshot(name string) (terminal.Attachment, bool)
}

func (s *Server) handleCoderStream(c *gin.Context) {
	id := c.Param("id")
	co, _, err := s.resolveRunning(id)
	if err != nil {
		c.String(http.StatusNotFound, err.Error())
		return
	}
	s.streamTerminal(c, co, id)
}

func (s *Server) handleShellStream(c *gin.Context) {
	s.streamTerminal(c, s.shells, c.Param("id"))
}

func (s *Server) streamTerminal(c *gin.Context, src terminalStream, id string) {
	if err := src.Resolve(id); err != nil {
		c.String(http.StatusNotFound, err.Error())
		return
	}
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, ok := w.(http.Flusher); !ok {
		c.String(http.StatusInternalServerError, "streaming unsupported")
		return
	}
	if err := writeSSERetry(w, 1*time.Second); err != nil {
		return
	}

	query := c.Request.URL.Query()
	s.updateTerminalTheme(query.Get("bg"), query.Get("fg"))
	attached, err := src.AttachStream(id, query.Get("cols"), query.Get("rows"))
	if err != nil {
		_ = writeSSEvent(w, "terminal-error", userFacingError(c, err))
		return
	}
	defer src.DetachStream(attached.Session)
	offset := attached.Offset
	generation := attached.Generation
	if err := writeSSEvent(w, "terminal-size", sizePayload(attached)); err != nil {
		return
	}
	if err := writeSSEvent(w, "snapshot", encodeBase64(attached.Snapshot)); err != nil {
		return
	}

	heartbeat := time.NewTicker(s.cfg.StreamHeartbeatInterval)
	defer heartbeat.Stop()

	// One filter per connection: it carries escape-sequence state across
	// delta reads and is reset whenever the stream restarts from a snapshot.
	var oscFilter tmux.OSCFilter

	lastActivity := time.Now()
	var lastFrame time.Time
	ctx := c.Request.Context()
	for {
		// Subscribe before reading so no wake is missed between the read below
		// and the wait at the end of the loop.
		updated, live := src.StreamUpdated(attached.Session)
		if !live {
			_ = writeSSEvent(w, "terminal-ended", "Terminal has ended.")
			return
		}

		// Another browser reset or resized this stream: resync from its snapshot.
		if refreshed, ok := src.RefreshStream(attached.Session, generation); ok {
			offset = refreshed.Offset
			generation = refreshed.Generation
			oscFilter.Reset()
			if err := writeSSEvent(w, "terminal-size", sizePayload(refreshed)); err != nil {
				return
			}
			if err := writeSSEvent(w, "snapshot", encodeBase64(refreshed.Snapshot)); err != nil {
				return
			}
			lastActivity = time.Now()
			lastFrame = time.Now()
			continue
		}

		delta, newOffset, reset := src.StreamDelta(attached.Session, offset)
		if reset {
			if snap, ok := src.Resnapshot(attached.Session); ok {
				offset = snap.Offset
				generation = snap.Generation
				oscFilter.Reset()
				if err := writeSSEvent(w, "terminal-size", sizePayload(snap)); err != nil {
					return
				}
				if err := writeSSEvent(w, "snapshot", encodeBase64(snap.Snapshot)); err != nil {
					return
				}
				lastActivity = time.Now()
				lastFrame = time.Now()
			}
			continue
		}
		if len(delta) > 0 {
			offset = newOffset
			lastActivity = time.Now()
			if out := oscFilter.Filter(delta); len(out) > 0 {
				if err := writeSSEvent(w, "delta", encodeBase64(out)); err != nil {
					return
				}
				lastFrame = time.Now()
			}
		}

		// The session ended: flush any final bytes, then close the stream.
		if src.StreamExited(attached.Session) {
			if d, n, _ := src.StreamDelta(attached.Session, offset); len(d) > 0 {
				offset = n
				if out := oscFilter.Filter(d); len(out) > 0 {
					_ = writeSSEvent(w, "delta", encodeBase64(out))
				}
			}
			_ = writeSSEvent(w, "terminal-ended", "Terminal has ended.")
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if time.Since(lastActivity) >= s.cfg.StreamHeartbeatInterval {
				if err := writeSSEKeepalive(w); err != nil {
					return
				}
				lastActivity = time.Now()
			}
		case <-updated:
			// Coalesce bursts: cap the frame rate so chatty output is batched
			// into one delta per frame instead of a flood of tiny events.
			if wait := s.cfg.StreamMinFrameInterval - time.Since(lastFrame); wait > 0 {
				t := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					t.Stop()
					return
				case <-t.C:
				}
			}
		}
	}
}

func sizePayload(a terminal.Attachment) string {
	return `{"cols":` + strconv.Itoa(a.Cols) +
		`,"rows":` + strconv.Itoa(a.Rows) +
		`,"mouseTracking":` + strconv.FormatBool(a.MouseTracking) +
		`,"mouseSgr":` + strconv.FormatBool(a.MouseSGR) +
		`,"altScreen":` + strconv.FormatBool(a.AltScreen) +
		`,"appCursor":` + strconv.FormatBool(a.AppCursor) + `}`
}

func writeSSERetry(w http.ResponseWriter, retry time.Duration) error {
	if _, err := fmt.Fprintf(w, "retry: %d\n\n", retry.Milliseconds()); err != nil {
		return err
	}
	return flushSSE(w)
}

func writeSSEvent(w http.ResponseWriter, event, data string) error {
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	for _, line := range strings.Split(data, "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(w, "\n"); err != nil {
		return err
	}
	return flushSSE(w)
}

func writeSSEKeepalive(w http.ResponseWriter) error {
	if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
		return err
	}
	return flushSSE(w)
}

func flushSSE(w http.ResponseWriter) error {
	return http.NewResponseController(w).Flush()
}
