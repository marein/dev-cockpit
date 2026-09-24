package opencode

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
)

// opencodeModelsNote is the line under every select over opencode's list.
const opencodeModelsNote = "List from opencode models, cached ten minutes."

// ModelRepository implements coder.ModelKeeper, the one list the New coder
// dialog and the assistant's selects both read.
func (p *Coder) ModelRepository() coder.ModelRepository { return p.models }

// cliModels are the CLI's own names: what `opencode models` printed the last
// time it ran, one provider/model per line, out of the cache that refreshes
// itself.
func (p *Coder) cliModels() []string {
	if p.modelCache == nil {
		return nil
	}
	return p.modelCache.names(time.Now())
}

// modelListTTL is how long what `opencode models` printed is trusted before
// the next read starts a refresh.
const modelListTTL = 10 * time.Minute

// modelListTimeout bounds one run of `opencode models`. The command boots a
// JavaScript runtime and may reach a provider, and a run that never comes back
// would otherwise hold the refreshing flag for the life of the serve process,
// with the child leaked and every select serving the stale list forever. Past
// the deadline the run is a failed refresh like any other, and the next read
// after the TTL tries again.
const modelListTimeout = 30 * time.Second

// modelListMaxOutput caps what one run may print. A list is a few kilobytes,
// and an output that reaches this is not one.
const modelListMaxOutput = 256 << 10

// modelList is what `opencode models` printed, kept for a while: the command
// boots opencode's JavaScript runtime and takes over a second, which no page
// render may wait for. So a read answers what is cached and, once the list is
// older than modelListTTL or was never fetched, starts one refresh in the
// background; the next read past that sees the fresh list. A refresh that
// failed keeps what stood and waits the same span before the next try, so a
// CLI that keeps failing costs one process every ten minutes and never one per
// read. Every refresh runs under modelListTimeout, so it always comes back and
// the refreshing flag is cleared on every way out.
type modelList struct {
	list    func(context.Context) ([]string, error)
	timeout time.Duration

	mu         sync.Mutex
	cached     []string
	at         time.Time
	refreshing bool
}

func newModelList(list func(context.Context) ([]string, error)) *modelList {
	return &modelList{list: list, timeout: modelListTimeout}
}

// names answers the cached list and starts a refresh where one is due. It
// never waits for one: a select that opens before the first fetch came back
// holds the default entry and the typed way past the list, and fills on the
// next open.
func (m *modelList) names(now time.Time) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.refreshing && (m.at.IsZero() || now.Sub(m.at) >= modelListTTL) {
		m.refreshing = true
		go m.refresh()
	}
	return append([]string(nil), m.cached...)
}

func (m *modelList) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	names, err := m.list(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshing = false
	m.at = time.Now()
	if err == nil {
		m.cached = names
	}
}

// listModels runs `opencode models` under the context's deadline with its
// output capped and reads its lines.
func listModels(ctx context.Context) ([]string, error) {
	return listModelsWith(ctx, "opencode", "models")
}

// listModelsWith is listModels over any program, so a test can hand it one
// that never ends. The deadline ends the process, and the grace after it is
// for the pipes a child of the program may still hold.
func listModelsWith(ctx context.Context, name string, args ...string) ([]string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	out := &cappedBuffer{max: modelListMaxOutput}
	errOut := &cappedBuffer{max: modelListMaxOutput}
	cmd.Stdout = out
	cmd.Stderr = errOut
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, errors.New("opencode models did not answer in time")
	}
	if out.overflow {
		return nil, errors.New("opencode models printed more than a list")
	}
	if err != nil && strings.TrimSpace(out.buf.String()) == "" {
		if message := strings.TrimSpace(errOut.buf.String()); message != "" {
			return nil, errors.New(message)
		}
		return nil, err
	}
	return parseModelList(out.buf.String()), nil
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

// parseModelList reads one provider/model per line and drops everything
// else: a blank line, a warning opencode prints on the way, a name no turn
// could carry. The order is opencode's own, which puts a provider's models
// together.
func parseModelList(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		name, err := assistant.CleanModel(line)
		if err != nil || name == "" || !strings.Contains(name, "/") {
			continue
		}
		out = append(out, name)
	}
	return out
}
