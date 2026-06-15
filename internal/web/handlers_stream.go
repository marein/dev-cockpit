package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/tmux"
)

func (s *Server) handleSessionStream(c *gin.Context) {
	id := c.Param("id")
	running, err := s.sessions.ResolveRunning(id)
	if err != nil {
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
	attached, err := s.sessions.AttachStream(id, query.Get("cols"), query.Get("rows"))
	if err != nil {
		_ = writeSSEvent(w, "session-error", err.Error())
		return
	}
	defer s.sessions.DetachStream(attached.Session)
	offset := attached.Offset
	generation := attached.Generation
	if err := writeSSEvent(w, "terminal-size", sizePayload(attached.Cols, attached.Rows)); err != nil {
		return
	}
	if err := writeSSEvent(w, "snapshot", encodeBase64(attached.Snapshot)); err != nil {
		return
	}

	ticker := time.NewTicker(s.cfg.StreamFrameInterval)
	defer ticker.Stop()
	heartbeat := time.NewTicker(s.cfg.StreamHeartbeatInterval)
	defer heartbeat.Stop()
	checkAlive := time.NewTicker(1 * time.Second)
	defer checkAlive.Stop()

	// One filter per connection: it carries escape-sequence state across
	// delta reads and is reset whenever the stream restarts from a snapshot.
	var oscFilter tmux.OSCFilter

	lastActivity := time.Now()
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if refreshed, ok := s.sessions.RefreshStream(attached.Session, generation); ok {
				offset = refreshed.Offset
				generation = refreshed.Generation
				oscFilter.Reset()
				if err := writeSSEvent(w, "terminal-size", sizePayload(refreshed.Cols, refreshed.Rows)); err != nil {
					return
				}
				if err := writeSSEvent(w, "snapshot", encodeBase64(refreshed.Snapshot)); err != nil {
					return
				}
				lastActivity = time.Now()
				continue
			}
			delta, newOffset, reset := s.sessions.StreamDelta(attached.Session, offset)
			if reset {
				if snap, ok := s.sessions.Resnapshot(attached.Session); ok {
					offset = snap.Offset
					generation = snap.Generation
					oscFilter.Reset()
					if err := writeSSEvent(w, "terminal-size", sizePayload(snap.Cols, snap.Rows)); err != nil {
						return
					}
					if err := writeSSEvent(w, "snapshot", encodeBase64(snap.Snapshot)); err != nil {
						return
					}
					lastActivity = time.Now()
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
				}
			}
		case <-heartbeat.C:
			if time.Since(lastActivity) >= s.cfg.StreamHeartbeatInterval {
				if err := writeSSEKeepalive(w); err != nil {
					return
				}
				lastActivity = time.Now()
			}
		case <-checkAlive.C:
			if !s.sessions.HasSession(running.TmuxSession) {
				_ = writeSSEvent(w, "session-error", "Session has ended.")
				return
			}
		}
	}
}

func sizePayload(cols, rows int) string {
	return `{"cols":` + strconv.Itoa(cols) + `,"rows":` + strconv.Itoa(rows) + `}`
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
