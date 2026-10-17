package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
)

// opencodeModelsNote is the line under every select over opencode's list.
const opencodeModelsNote = "List from opencode models, cached ten minutes."

// modelEntry is one model of `opencode models --verbose`: the provider/model
// name a turn is started under, the bound the metadata printed under that
// name puts on what a turn may send, and the effort levels it takes.
type modelEntry struct {
	Name string
	// Window is the prompt bound out of the model's metadata, see window,
	// zero where the block named none or could not be read, which reads as
	// unknown.
	Window int
	// Variants are the levels the model takes, see variants, none where the
	// block named none.
	Variants []string
}

// modelMetadata is what is read out of the block under a name: the two limits
// the window is resolved from and the variants, the costs and capabilities
// beside them are opencode's business.
type modelMetadata struct {
	Limit struct {
		Context int `json:"context"`
		Input   int `json:"input"`
	} `json:"limit"`
	Variants map[string]json.RawMessage `json:"variants"`
}

// ModelRepository implements coder.ModelKeeper, the one list the New coder
// dialog and the assistant's selects both read.
func (p *Coder) ModelRepository() coder.ModelRepository { return p.models }

// cliModels are the CLI's own names: what `opencode models` printed the last
// time it ran, one provider/model per line, out of the cache that refreshes
// itself, each with the variants it names.
func (p *Coder) cliModels() []coder.Model {
	if p.modelCache == nil {
		return nil
	}
	entries := p.modelCache.entries(time.Now())
	models := make([]coder.Model, len(entries))
	for i, entry := range entries {
		models[i] = coder.Model{Name: entry.Name, Efforts: entry.Variants}
	}
	return models
}

// modelWindow is the bound a model's prompt is measured against, out of the
// same cached list the selects read (window, over its metadata), zero for a
// name the list does not hold and for a list that never came back, both of
// which read as unknown.
func (p *Coder) modelWindow(name string) int {
	if p.modelCache == nil {
		return 0
	}
	return p.modelCache.window(name, time.Now())
}

// modelVariants is modelList.variants over the coder's own list, an unread
// list where none stands.
func (p *Coder) modelVariants(name string) ([]string, bool, error) {
	if p.modelCache == nil {
		return nil, false, errVariantsUnknown
	}
	return p.modelCache.variants(name)
}

// Efforts implements coder.EffortKeeper: every level a listed model takes.
// opencode's levels are the variants each model names in `opencode models
// --verbose`, so a level stands here as soon as one model takes it, and the
// model a start or a turn names is what decides, see modelList.variants.
func (p *Coder) Efforts() []string {
	if p.modelCache == nil {
		return nil
	}
	return p.modelCache.efforts(time.Now())
}

// effortRank orders levels the way the CLIs order theirs, from the least to
// the most; a provider's own word stands behind them, alphabetically.
var effortRank = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

func sortEfforts(levels []string) {
	rank := func(level string) int {
		if i := slices.Index(effortRank, level); i >= 0 {
			return i
		}
		return len(effortRank)
	}
	slices.SortFunc(levels, func(a, b string) int {
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		return strings.Compare(a, b)
	})
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

// modelListMaxOutput caps what one run may print. A verbose list is about one
// and a half kilobytes per model (37928 bytes for 26 models, measured on
// 1.18.32), so this holds a few thousand models, and an output that reaches
// it is not a list.
const modelListMaxOutput = 4 << 20

// modelList is what `opencode models --verbose` printed, kept for a while:
// the command boots opencode's JavaScript runtime and takes over a second,
// which no page render may wait for. So a read answers what is cached and,
// once the list is older than modelListTTL or was never fetched, starts one
// refresh in the background; the next read past that sees the fresh list. A
// refresh that failed keeps what stood and waits the same span before the
// next try, so a CLI that keeps failing costs one process every ten minutes
// and never one per read. Every refresh runs under modelListTimeout, so it
// always comes back and the refreshing flag is cleared on every way out. The
// one run serves the names the selects show and the windows the context ring
// is measured against alike, so the two cannot come out of different lists.
type modelList struct {
	list    func(context.Context) ([]modelEntry, error)
	timeout time.Duration

	mu         sync.Mutex
	cached     []modelEntry
	at         time.Time
	refreshing bool
}

func newModelList(list func(context.Context) ([]modelEntry, error)) *modelList {
	return &modelList{list: list, timeout: modelListTimeout}
}

// entries answers the cached list and starts a refresh where one is due. It
// never waits for one: a select that opens before the first fetch came back
// holds the default entry and the typed way past the list, and fills on the
// next open.
func (m *modelList) entries(now time.Time) []modelEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.refreshing && (m.at.IsZero() || now.Sub(m.at) >= modelListTTL) {
		m.refreshing = true
		go m.refresh()
	}
	return append([]modelEntry(nil), m.cached...)
}

// window is the bound the named model's prompt is measured against: zero for
// a name the list does not hold, for a list that never came back and for a
// model whose metadata named no bound, all of which read as unknown. The name
// is the provider/model form the list prints, compared as it stands.
func (m *modelList) window(name string, now time.Time) int {
	for _, entry := range m.entries(now) {
		if entry.Name == name {
			return entry.Window
		}
	}
	return 0
}

// efforts is every level a cached model takes, in effortRank's order.
func (m *modelList) efforts(now time.Time) []string {
	var levels []string
	for _, entry := range m.entries(now) {
		for _, level := range entry.Variants {
			if !slices.Contains(levels, level) {
				levels = append(levels, level)
			}
		}
	}
	sortEfforts(levels)
	return levels
}

// variants answers the levels the named model takes and whether the list
// holds the model at all. A model the cached list does not hold is looked up
// once more in a list fetched here and waited on, unlike entries: a start or
// a turn that carries a level has to know whether its model takes it, where
// a page only shows what stands, and the cached list can be the catalog
// opencode answered with before it refreshed its own (1.19.0 answers its
// first run under a fresh cache directory out of the catalog it ships, and
// the next one out of the catalog it fetched meanwhile, other free models).
//
// It answers an error where the list could not be read at all, so a level is
// never refused as one the model lacks while nobody knows what it takes.
func (m *modelList) variants(name string) ([]string, bool, error) {
	if levels, ok := variantsIn(m.entries(time.Now()), name); ok {
		return levels, true, nil
	}
	entries, err := m.fetchNow()
	if err != nil {
		return nil, false, err
	}
	levels, ok := variantsIn(entries, name)
	return levels, ok, nil
}

func variantsIn(entries []modelEntry, name string) ([]string, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry.Variants, true
		}
	}
	return nil, false
}

// fetchNow is one refresh waited on, under the same deadline.
func (m *modelList) fetchNow() ([]modelEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	entries, err := m.list(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cached, m.at = entries, time.Now()
	return append([]modelEntry(nil), entries...), nil
}

func (m *modelList) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	entries, err := m.list(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshing = false
	m.at = time.Now()
	if err == nil {
		m.cached = entries
	}
}

// listModels runs `opencode models --verbose` under the context's deadline
// with its output capped and reads its lines. The verbose form costs the same
// run as the plain one, measured on 1.18.32, and is the only place the
// installed CLI says how much context a model holds.
func listModels(ctx context.Context) ([]modelEntry, error) {
	return listModelsWith(ctx, "opencode", "models", "--verbose")
}

// listModelsWith is listModels over any program, so a test can hand it one
// that never ends. The deadline ends the process, and the grace after it is
// for the pipes a child of the program may still hold.
func listModelsWith(ctx context.Context, name string, args ...string) ([]modelEntry, error) {
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

// parseModelList reads `opencode models --verbose`: one provider/model per
// line, each followed by its metadata as one pretty printed JSON object, from
// the line that opens the brace in its first column to the line that is
// nothing but the closing one, the braces nested inside standing indented. A
// block printed on one line is that line. A block is read for the name
// directly above it and for nothing else, so a block under a line that is no
// name is read past instead of landing on the model before it. Everything
// else is dropped: a blank line, a warning opencode prints on the way, a name
// no turn could carry. A block that cannot be read leaves its model without a
// bound rather than the list without the model, so a select still offers what
// the ring cannot measure. The plain list, names without blocks, reads the
// same way with every bound unknown. The order is opencode's own, which puts
// a provider's models together.
func parseModelList(text string) []modelEntry {
	var out []modelEntry
	var block []string
	inBlock := false
	// owner is whether the newest entry is still waiting for its block.
	owner := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case inBlock:
			block = append(block, line)
			if line != "}" {
				continue
			}
			inBlock = false
			if owner {
				out[len(out)-1].read(strings.Join(block, "\n"))
				owner = false
			}
		case strings.HasPrefix(line, "{"):
			block = append(block[:0], line)
			if strings.HasSuffix(strings.TrimSpace(line), "}") {
				if owner {
					out[len(out)-1].read(line)
					owner = false
				}
				continue
			}
			inBlock = true
		default:
			owner = false
			name, err := assistant.CleanModel(line)
			if err != nil || name == "" || !strings.Contains(name, "/") {
				continue
			}
			out = append(out, modelEntry{Name: name})
			owner = true
		}
	}
	return out
}

// read fills an entry out of the metadata block under its name. A block that
// cannot be read leaves the entry as it was, without a bound and without
// variants.
func (e *modelEntry) read(block string) {
	var meta modelMetadata
	if err := json.Unmarshal([]byte(block), &meta); err != nil {
		return
	}
	e.Window = meta.window()
	e.Variants = meta.variants()
}

// variants are the keys of a block's `variants` object, each a level the
// model takes behind `opencode run --variant` and in the terminal, every one
// read under the levels rule and a key it refuses dropped, in effortRank's
// order (1.19.0 names none, minimal, low, medium, high, xhigh and max, a model
// some of them and some none at all).
func (m modelMetadata) variants() []string {
	var levels []string
	for key := range m.Variants {
		if level, err := assistant.CleanEffort(key); err == nil && level != "" {
			levels = append(levels, level)
		}
	}
	sortEfforts(levels)
	return levels
}

// window is the bound one metadata block puts on a prompt: `limit.input`
// where the block names one, else `limit.context`, else zero, which reads as
// unknown. That is the bound opencode's own compaction check reads (1.18.32,
// its isOverflow measures a turn's tokens against `limit.input` less a
// reserve where the model names one, and against `limit.context` less the
// output allowance otherwise; the reserve and the allowance are its margins
// and stay out of the bound, the way claude's autocompact margin stays out
// of its window). `limit.input` is what a provider caps the prompt at below
// the whole window, and for the github-copilot provider it is number for
// number copilot's own `max_prompt_tokens`, the bound the copilot rows of
// the table in `internal/assistant` carry. The TUI's own percent reads
// against `limit.context`, which for a model with an input bound stands
// well past the point where the session compacts. A zero input reads as
// none, the way opencode reads it.
func (m modelMetadata) window() int {
	if m.Limit.Input > 0 {
		return m.Limit.Input
	}
	if m.Limit.Context > 0 {
		return m.Limit.Context
	}
	return 0
}
