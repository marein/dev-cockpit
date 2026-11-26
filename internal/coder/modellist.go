package coder

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ModelListTimeout bounds one run of a CLI's list command. A command may
// boot a runtime or reach a provider, and a run that never comes back would
// otherwise hold the refreshing flag for the life of the serve process, with
// the child leaked and every select serving the stale list forever. Past the
// deadline the run is a failed refresh like any other, and the next read
// after the TTL tries again.
const ModelListTimeout = 30 * time.Second

// ModelList is what a CLI's list command answered, kept for a while: such a
// command can take over a second, which no page render may wait for. So a
// read answers what is cached and, once the list is older than its TTL or was
// never fetched, starts one refresh in the background; the next read past
// that sees the fresh list. A refresh that failed keeps what stood and waits
// the same span before the next try, so a CLI that keeps failing costs one
// process per TTL and never one per read. Every refresh runs under its
// timeout, so it always comes back and the refreshing flag is cleared on
// every way out. T is whatever one entry of the coder's list carries.
type ModelList[T any] struct {
	fetch   func(context.Context) ([]T, error)
	ttl     time.Duration
	timeout time.Duration

	mu         sync.Mutex
	cached     []T
	at         time.Time
	refreshing bool
}

func NewModelList[T any](ttl time.Duration, fetch func(context.Context) ([]T, error)) *ModelList[T] {
	return &ModelList[T]{fetch: fetch, ttl: ttl, timeout: ModelListTimeout}
}

// Entries answers the cached list and starts a refresh where one is due. It
// never waits for one: a select that opens before the first fetch came back
// holds the default entry and the typed way past the list, and fills on the
// next open.
func (m *ModelList[T]) Entries(now time.Time) []T {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.refreshing && (m.at.IsZero() || now.Sub(m.at) >= m.ttl) {
		m.refreshing = true
		go m.refresh()
	}
	return append([]T(nil), m.cached...)
}

func (m *ModelList[T]) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	entries, err := m.fetch(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshing = false
	m.at = time.Now()
	if err == nil {
		m.cached = entries
	}
}

// RunListCommand runs a CLI's list command under the context's deadline with
// its output capped at maxOutput and answers what it printed. A run that
// printed nothing and failed answers what it said on standard error, else its
// exit. The grace after the deadline is for the pipes a child of the command
// may still hold.
func RunListCommand(ctx context.Context, maxOutput int, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	out := &cappedBuffer{max: maxOutput}
	errOut := &cappedBuffer{max: maxOutput}
	cmd.Stdout = out
	cmd.Stderr = errOut
	err := cmd.Run()
	line := strings.Join(append([]string{name}, args...), " ")
	if ctx.Err() != nil {
		return "", errors.New(line + " did not answer in time")
	}
	if out.overflow {
		return "", errors.New(line + " printed more than a list")
	}
	if err != nil && strings.TrimSpace(out.buf.String()) == "" {
		if message := strings.TrimSpace(errOut.buf.String()); message != "" {
			return "", errors.New(message)
		}
		return "", err
	}
	return out.buf.String(), nil
}

// cappedBuffer keeps the first max bytes written to it and notes that more
// came, so a process that prints without end is read to the cap and no
// further while its output keeps draining.
type cappedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
			c.overflow = true
		} else {
			c.buf.Write(p)
		}
	} else if len(p) > 0 {
		c.overflow = true
	}
	return len(p), nil
}
