package price

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/netguard"
	"github.com/marein/dev-cockpit/internal/statefile"
)

// RefreshSettingKey switches the daily refresh, "off" keeps the cockpit
// offline and prices with the last good table.
const RefreshSettingKey = "cost-price-refresh"

// feedURL is LiteLLM's public price list, the one ccusage reads too. It
// carries every provider's list price per token.
const feedURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

const (
	// refreshEvery is how old a table may get. Providers change prices
	// rarely and announce new models days ahead, a day is soon enough.
	refreshEvery = 24 * time.Hour
	// retryAfter is the wait after a failed fetch: an offline host tries
	// again within the hour it comes back, without hammering while it is not.
	retryAfter = time.Hour
	// checkEvery is how soon a due fetch or a flipped switch takes effect.
	checkEvery = time.Minute
	// feedLimit caps the body, the list is about 3 MB today.
	feedLimit = 32 << 20
)

// Status says where the prices come from right now.
type Status struct {
	Enabled bool
	// FetchedAt is the last successful refresh, zero while the embedded
	// snapshot is all there is.
	FetchedAt time.Time
	// NextAt is when the next refresh is due.
	NextAt time.Time
}

type cacheFile struct {
	FetchedAt time.Time `json:"fetched_at,omitzero"`
	NextAt    time.Time `json:"next_at,omitzero"`
	Table     Table     `json:"table,omitempty"`
}

// Book owns the price table: the embedded snapshot overlaid with the last
// table a refresh fetched, and the schedule of the next refresh. Both are
// stored together, so a restart continues the schedule instead of fetching
// on every start.
type Book struct {
	path    string
	url     string
	client  *http.Client
	enabled func() bool
	now     func() time.Time

	mu    sync.Mutex
	cache cacheFile
	table Table
}

// NewBook reads the stored table. enabled is asked before every refresh.
func NewBook(stateDir string, enabled func() bool) *Book {
	b := &Book{
		path:    filepath.Join(stateDir, "cost", "prices.json"),
		url:     feedURL,
		client:  feedClient(),
		enabled: enabled,
		now:     time.Now,
	}
	statefile.Load(b.path, &b.cache)
	if !b.cache.Table.plausible() {
		log.Printf("cost: the stored price table holds an implausible price, pricing with the embedded one")
		b.cache = cacheFile{}
	}
	b.table = overlay(Snapshot(), b.cache.Table)
	return b
}

// feedClient follows no redirect and refuses link local targets, the rules
// of every outgoing request of the cockpit.
func feedClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 10 * time.Second, Control: netguard.RefuseLinkLocal}).DialContext,
		},
	}
}

// overlay is base with the valid rates of top over it. A model the binary
// knows and the stored table does not stays priced. The list leaves the web
// search price out on some models, which the provider bills on every model,
// so base's price stands in there, found the way a call finds its rate: a
// dated name takes the price of its undated model.
func overlay(base, top Table) Table {
	out := Table{}
	for provider, models := range base {
		out[provider] = map[string]Rate{}
		for model, r := range models {
			out[provider][model] = r
		}
	}
	for provider, models := range top {
		if out[provider] == nil {
			out[provider] = map[string]Rate{}
		}
		for model, r := range models {
			if !r.valid() {
				continue
			}
			if b, ok := base.Lookup(provider, model); ok && r.WebSearch == 0 {
				r.WebSearch = b.WebSearch
			}
			out[provider][model] = r
		}
	}
	return out
}

// Table is the table to price with now. It is never changed in place, a
// refresh replaces it.
func (b *Book) Table() Table {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.table
}

// Status answers the page and the docs where the prices stand.
func (b *Book) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Status{Enabled: b.enabled(), FetchedAt: b.cache.FetchedAt, NextAt: b.cache.NextAt}
}

// Run refreshes whenever one is due, the first check right away.
func (b *Book) Run(ctx context.Context) {
	t := time.NewTicker(checkEvery)
	defer t.Stop()
	for {
		b.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick fetches if the switch is on and the schedule says so. A refresh that
// never ran is due at once.
func (b *Book) Tick(ctx context.Context) {
	if !b.enabled() {
		return
	}
	b.mu.Lock()
	due := !b.now().Before(b.cache.NextAt)
	b.mu.Unlock()
	if !due {
		return
	}
	table, err := b.fetch(ctx)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if err != nil {
		log.Printf("cost: price refresh: %v", err)
		b.cache.NextAt = now.Add(retryAfter)
	} else {
		b.cache.FetchedAt = now
		b.cache.NextAt = now.Add(refreshEvery)
		b.cache.Table = table
		b.table = overlay(Snapshot(), table)
	}
	statefile.Save(b.path, 0o600, b.cache)
}

func (b *Book) fetch(ctx context.Context) (Table, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("price list answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, feedLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > feedLimit {
		return nil, errors.New("price list is larger than expected")
	}
	table, err := parseFeed(body)
	if err != nil {
		return nil, err
	}
	if !table.covers(Snapshot()) {
		return nil, errors.New("price list lacks a model of the embedded table, kept the last good one")
	}
	return table, nil
}

type feedEntry struct {
	Provider       string   `json:"litellm_provider"`
	Input          *float64 `json:"input_cost_per_token"`
	Output         *float64 `json:"output_cost_per_token"`
	CacheRead      *float64 `json:"cache_read_input_token_cost"`
	CacheWrite5m   *float64 `json:"cache_creation_input_token_cost"`
	CacheWrite1h   *float64 `json:"cache_creation_input_token_cost_above_1hr"`
	SearchPerQuery *struct {
		Medium *float64 `json:"search_context_size_medium"`
	} `json:"search_context_cost_per_query"`
	Specific *struct {
		Fast *float64 `json:"fast"`
	} `json:"provider_specific_entry"`
}

// feedProviders maps the feed's provider names onto the table's.
var feedProviders = map[string]string{"anthropic": Anthropic}

// parseFeed reads LiteLLM's list into a table of the providers it knows.
// Entries under a routed name (provider/model) are left out, the plain name
// is the one a CLI reports. Where the list leaves out a cache write price the
// provider's documented multiple of the input price stands in, 1.25 for five
// minutes and 2 for an hour. One implausible price of a known provider makes
// the whole list invalid, the last good table stays.
func parseFeed(body []byte) (Table, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("price list does not parse: %w", err)
	}
	out := Table{}
	for model, entry := range raw {
		if strings.Contains(model, "/") {
			continue
		}
		var e feedEntry
		if json.Unmarshal(entry, &e) != nil {
			continue
		}
		provider, ok := feedProviders[e.Provider]
		if !ok || e.Input == nil || e.Output == nil || e.CacheRead == nil {
			continue
		}
		perM := func(v float64) float64 { return v * 1e6 }
		r := Rate{
			Input:        perM(*e.Input),
			Output:       perM(*e.Output),
			CacheRead:    perM(*e.CacheRead),
			CacheWrite5m: perM(*e.Input) * 1.25,
			CacheWrite1h: perM(*e.Input) * 2,
		}
		if e.CacheWrite5m != nil {
			r.CacheWrite5m = perM(*e.CacheWrite5m)
		}
		if e.CacheWrite1h != nil {
			r.CacheWrite1h = perM(*e.CacheWrite1h)
		}
		if e.SearchPerQuery != nil && e.SearchPerQuery.Medium != nil {
			r.WebSearch = *e.SearchPerQuery.Medium * 1e3
		}
		if e.Specific != nil && e.Specific.Fast != nil {
			r.Fast = *e.Specific.Fast
		}
		if !r.plausible() {
			return nil, fmt.Errorf("price list prices %s implausibly, kept the last good one", model)
		}
		if !r.valid() {
			continue
		}
		if out[provider] == nil {
			out[provider] = map[string]Rate{}
		}
		out[provider][model] = r
	}
	return out, nil
}
