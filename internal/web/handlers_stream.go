package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// maxStreamFrameBytes caps how much output is coalesced into a single SSE delta,
// so a burst is still chunked rather than buffered without bound.
const maxStreamFrameBytes = 256 * 1024

func (s *Server) handleSessionStream(c *gin.Context) {
	id := c.Param("id")
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
	stream, cols, rows, err := s.sessions.OpenStream(id, query.Get("cols"), query.Get("rows"))
	if err != nil {
		_ = writeSSEvent(w, "session-error", err.Error())
		return
	}
	defer stream.Close()

	if err := writeSSEvent(w, "terminal-size", sizePayload(cols, rows)); err != nil {
		return
	}
	// An empty snapshot tells the client to reset its terminal; the repaint the
	// agent triggers on attach then arrives as the first deltas.
	if err := writeSSEvent(w, "snapshot", ""); err != nil {
		return
	}

	heartbeat := time.NewTicker(s.cfg.StreamHeartbeatInterval)
	defer heartbeat.Stop()

	ctx := c.Request.Context()
	out := stream.Output()
	lastActivity := time.Now()
	for {
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
		case chunk, ok := <-out:
			if !ok {
				_ = writeSSEvent(w, "session-error", "Session has ended.")
				return
			}
			batch, ended := coalesce(ctx, out, chunk, s.cfg.StreamMinFrameInterval)
			if len(batch) > 0 {
				if err := writeSSEvent(w, "delta", encodeBase64(batch)); err != nil {
					return
				}
				lastActivity = time.Now()
			}
			if ended {
				_ = writeSSEvent(w, "session-error", "Session has ended.")
				return
			}
		}
	}
}

// coalesce batches bytes arriving within one frame interval into a single delta,
// so chatty output becomes one event per frame instead of a flood. It returns
// the batch and whether the stream ended while collecting.
func coalesce(ctx context.Context, out <-chan []byte, first []byte, window time.Duration) (batch []byte, ended bool) {
	batch = append(batch, first...)
	timer := time.NewTimer(window)
	defer timer.Stop()
	for {
		select {
		case more, ok := <-out:
			if !ok {
				return batch, true
			}
			batch = append(batch, more...)
			if len(batch) >= maxStreamFrameBytes {
				return batch, false
			}
		case <-timer.C:
			return batch, false
		case <-ctx.Done():
			return batch, false
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
