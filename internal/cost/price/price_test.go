package price

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %.12f, want %.12f", what, got, want)
	}
}

func TestSnapshotPricesEveryModelWithAUsableRate(t *testing.T) {
	s := Snapshot()
	if len(s[Anthropic]) == 0 {
		t.Fatal("the snapshot holds no anthropic model")
	}
	for model, r := range s[Anthropic] {
		if !r.valid() {
			t.Errorf("%s has an unusable rate %+v", model, r)
		}
		if r.CacheWrite1h != 2*r.Input || math.Abs(r.CacheWrite5m-1.25*r.Input) > 1e-9 {
			t.Errorf("%s cache writes are not 1.25x and 2x the input price: %+v", model, r)
		}
	}
}

func TestLookupFoldsTheContextWindowAndTheDate(t *testing.T) {
	s := Snapshot()
	for _, name := range []string{"claude-opus-5-5", "claude-opus-5-5[1m]", " claude-opus-5-5 "} {
		if r, ok := s.Lookup(Anthropic, name); !ok || r.Input != 4 {
			t.Fatalf("%q = %+v, %v", name, r, ok)
		}
	}
	if r, ok := s.Lookup(Anthropic, "claude-haiku-4-5-20251001"); !ok || r.Input != 1 {
		t.Fatalf("dated haiku = %+v, %v", r, ok)
	}
	for _, name := range []string{"nemotron-3-ultra", "gpt-oss:120b", "<synthetic>", ""} {
		if _, ok := s.Lookup(Anthropic, name); ok {
			t.Fatalf("%q has a price", name)
		}
	}
	if _, ok := s.Lookup("openai", "claude-opus-5-5"); ok {
		t.Fatal("a model is priced under a provider that does not sell it")
	}
	if got := BaseModel("claude-opus-5[1m]"); got != "claude-opus-5" {
		t.Fatalf("BaseModel = %q", got)
	}
}

// The rates and the formula claude's own totals match to the cent: 1h cache
// writes at twice the input price, thinking inside output.
func TestCostPricesEveryClass(t *testing.T) {
	r, _ := Snapshot().Lookup(Anthropic, "claude-opus-5-5")
	tokens := Tokens{Input: 1000, Output: 2000, CacheRead: 100000, CacheWrite5m: 3000, CacheWrite1h: 4000, WebSearch: 2}
	want := (1000*4 + 2000*20 + 100000*0.2 + 3000*5 + 4000*8) / 1e6
	near(t, "standard", r.Cost(tokens, false), want+0.02)
	near(t, "fast", r.Cost(tokens, true), want*2+0.02)
	haiku, _ := Snapshot().Lookup(Anthropic, "claude-haiku-4-5")
	near(t, "fast without a fast mode", haiku.Cost(Tokens{Output: 1e6}, true), 5)
}

const feedFixture = `{
  "sample_spec": {"litellm_provider": "openai"},
  "claude-opus-5-5": {"litellm_provider": "anthropic", "input_cost_per_token": 4e-06, "output_cost_per_token": 2e-05,
    "cache_read_input_token_cost": 2e-07, "cache_creation_input_token_cost": 5e-06, "cache_creation_input_token_cost_above_1hr": 8e-06,
    "search_context_cost_per_query": {"search_context_size_medium": 0.01}, "provider_specific_entry": {"fast": 2.0}},
  "anthropic/claude-opus-5-5": {"litellm_provider": "anthropic", "input_cost_per_token": 1},
  "claude-sonnet-5-5": {"litellm_provider": "anthropic", "input_cost_per_token": 2e-06, "output_cost_per_token": 1e-05,
    "cache_read_input_token_cost": 2e-07},
  "gpt-x": {"litellm_provider": "openai", "input_cost_per_token": 1e-06, "output_cost_per_token": 1e-06, "cache_read_input_token_cost": 1e-07}
}`

func TestParseFeedReadsTheAnthropicEntries(t *testing.T) {
	table, err := parseFeed([]byte(feedFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(table) != 1 || len(table[Anthropic]) != 2 {
		t.Fatalf("table = %+v", table)
	}
	opus := table[Anthropic]["claude-opus-5-5"]
	near(t, "input", opus.Input, 4)
	near(t, "1h", opus.CacheWrite1h, 8)
	near(t, "search", opus.WebSearch, 10)
	near(t, "fast", opus.Fast, 2)
	sonnet := table[Anthropic]["claude-sonnet-5-5"]
	near(t, "5m from the input price", sonnet.CacheWrite5m, 2.5)
	near(t, "1h from the input price", sonnet.CacheWrite1h, 4)
	if _, err := parseFeed([]byte("not json")); err == nil {
		t.Fatal("a broken list parses")
	}
}

// fullFeed is a list that prices every model of the snapshot, opus 5.5 at a
// price that tells it apart.
func fullFeed(t testing.TB) []byte { return feedWithOpusAt(t, 3/1e6) }

// feedWithOpusAt is a list that prices every model of the snapshot, opus 5.5
// at the input price per token given, the unit of the list.
func feedWithOpusAt(t testing.TB, opusInput float64) []byte {
	t.Helper()
	feed := map[string]any{}
	for model, r := range Snapshot()[Anthropic] {
		input := r.Input / 1e6
		if model == "claude-opus-5-5" {
			input = opusInput
		}
		feed[model] = map[string]any{
			"litellm_provider":            "anthropic",
			"input_cost_per_token":        input,
			"output_cost_per_token":       r.Output / 1e6,
			"cache_read_input_token_cost": r.CacheRead / 1e6,
		}
	}
	raw, err := json.Marshal(feed)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newBook(t *testing.T, dir string, url string, c *clock, enabled *bool) *Book {
	t.Helper()
	b := NewBook(dir, func() bool { return *enabled })
	b.url, b.ollamaURL, b.now = url, "http://127.0.0.1:0/", c.Now
	return b
}

func TestBookKeepsItsScheduleAcrossRestarts(t *testing.T) {
	hits := 0
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if fail {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		w.Write(fullFeed(t))
	}))
	defer srv.Close()
	dir := t.TempDir()
	c := &clock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	on := true
	b := newBook(t, dir, srv.URL, c, &on)
	if r, _ := b.Table().Lookup(Anthropic, "claude-opus-5-5"); r.Input != 4 {
		t.Fatalf("a fresh install prices with %+v, not the snapshot", r)
	}
	b.Tick(context.Background())
	if hits != 1 {
		t.Fatalf("a refresh that never ran is not due at once: %d fetches", hits)
	}
	if r, _ := b.Table().Lookup(Anthropic, "claude-opus-5-5"); r.Input != 3 || r.WebSearch != 10 {
		t.Fatalf("the fetched table is not used, or lost the search price the list leaves out: %+v", r)
	}
	st := b.Status()
	if !st.FetchedAt.Equal(c.now) || !st.NextAt.Equal(c.now.Add(refreshEvery)) {
		t.Fatalf("status = %+v", st)
	}

	// A restart an hour later continues the schedule: nothing is due, and
	// the stored table prices.
	c.now = c.now.Add(time.Hour)
	b = newBook(t, dir, srv.URL, c, &on)
	b.Tick(context.Background())
	if hits != 1 {
		t.Fatalf("a restart fetched again: %d fetches", hits)
	}
	if r, _ := b.Table().Lookup(Anthropic, "claude-opus-5-5"); r.Input != 3 {
		t.Fatalf("a restart forgot the fetched table: %+v", r)
	}

	// Due and offline: the last good table stays, the next try is in an hour.
	c.now = c.now.Add(refreshEvery)
	fail = true
	b.Tick(context.Background())
	if hits != 2 {
		t.Fatalf("a due refresh did not run: %d fetches", hits)
	}
	if r, _ := b.Table().Lookup(Anthropic, "claude-opus-5-5"); r.Input != 3 {
		t.Fatalf("a failed refresh dropped the table: %+v", r)
	}
	if st := b.Status(); !st.NextAt.Equal(c.now.Add(retryAfter)) || !st.FetchedAt.Equal(c.now.Add(-refreshEvery-time.Hour)) {
		t.Fatalf("after a failure status = %+v", st)
	}
	var stored cacheFile
	statefile.Load(filepath.Join(dir, "cost", "prices.json"), &stored)
	if !stored.NextAt.Equal(c.now.Add(retryAfter)) || stored.Table == nil {
		t.Fatalf("stored = %+v", stored)
	}

	// Switched off, nothing is fetched even when due.
	on = false
	c.now = c.now.Add(2 * retryAfter)
	b.Tick(context.Background())
	if hits != 2 || b.Status().Enabled {
		t.Fatalf("the switch is ignored: %d fetches", hits)
	}
}

func TestBookRefusesAListThatLacksAKnownModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(feedFixture))
	}))
	defer srv.Close()
	c := &clock{now: time.Now()}
	on := true
	b := newBook(t, t.TempDir(), srv.URL, c, &on)
	b.Tick(context.Background())
	if !b.Status().FetchedAt.IsZero() {
		t.Fatal("a list without every known model was taken")
	}
	if r, _ := b.Table().Lookup(Anthropic, "claude-fable-5-1"); r.Input != 10 {
		t.Fatalf("the snapshot is gone: %+v", r)
	}
}

func TestBookFollowsNoRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fullFeed(t))
	}))
	defer target.Close()
	srv := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer srv.Close()
	c := &clock{now: time.Now()}
	on := true
	b := newBook(t, t.TempDir(), srv.URL, c, &on)
	b.Tick(context.Background())
	if !b.Status().FetchedAt.IsZero() {
		t.Fatal("a redirect was followed")
	}
}

func TestAnImplausiblePriceKeepsTheLastGoodTable(t *testing.T) {
	if _, err := parseFeed(feedWithOpusAt(t, 1e303)); err == nil {
		t.Fatal("a list pricing a token at 1e303 USD parses")
	}
	if _, err := parseFeed(feedWithOpusAt(t, 2*maxPerMillion/1e6)); err == nil {
		t.Fatal("a list above the bound parses")
	}
	table, err := parseFeed(feedWithOpusAt(t, 400/1e6))
	if err != nil {
		t.Fatalf("a dear but plausible list is refused: %v", err)
	}
	near(t, "a dear input", table[Anthropic]["claude-opus-5-5"].Input, 400)
	if (Rate{Input: 200, Output: 600, CacheRead: 1, CacheWrite5m: 1, CacheWrite1h: 1, Fast: 2}).valid() {
		t.Fatal("a price the fast factor carries past the bound is valid")
	}

	opusInput := 3 / 1e6
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(feedWithOpusAt(t, opusInput))
	}))
	defer srv.Close()
	dir := t.TempDir()
	c := &clock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	on := true
	b := newBook(t, dir, srv.URL, c, &on)
	b.Tick(context.Background())
	good := b.Status().FetchedAt
	opusInput = 1e303
	c.now = c.now.Add(refreshEvery)
	b.Tick(context.Background())
	if r, _ := b.Table().Lookup(Anthropic, "claude-opus-5-5"); r.Input != 3 || !b.Status().FetchedAt.Equal(good) {
		t.Fatalf("an implausible list replaced the last good one: %+v", r)
	}
	cost := b.Table()[Anthropic]["claude-opus-5-5"].Cost(Tokens{Input: math.MaxInt64, Output: math.MaxInt64}, true)
	if math.IsInf(cost, 0) || math.IsNaN(cost) {
		t.Fatalf("the largest call costs %v", cost)
	}

	stored := cacheFile{FetchedAt: good, Table: Table{Anthropic: {"claude-opus-5-5": {Input: 1e303, Output: 1, CacheRead: 1, CacheWrite5m: 1, CacheWrite1h: 1}}}}
	if err := statefile.Write(filepath.Join(dir, "cost", "prices.json"), 0o600, stored); err != nil {
		t.Fatal(err)
	}
	b = newBook(t, dir, srv.URL, c, &on)
	if r, _ := b.Table().Lookup(Anthropic, "claude-opus-5-5"); r.Input != 4 || !b.Status().FetchedAt.IsZero() {
		t.Fatalf("a stored implausible table prices: %+v", r)
	}
}

// The list leaves the web search price out on some rows, under the dated
// name the CLI reports among them: the built in price of that model stands
// in, found the way a call finds its rate.
func TestADatedRowWithoutAWebSearchPriceTakesTheBuiltInOne(t *testing.T) {
	base := Snapshot()
	top := Table{Anthropic: {"claude-haiku-4-5-20251001": {Input: 1, Output: 5, CacheRead: 0.1, CacheWrite5m: 1.25, CacheWrite1h: 2}}}
	r, ok := overlay(base, top).Lookup(Anthropic, "claude-haiku-4-5-20251001")
	want, _ := base.Lookup(Anthropic, "claude-haiku-4-5")
	if !ok || want.WebSearch == 0 || r.WebSearch != want.WebSearch {
		t.Fatalf("web search %v, want the built in %v", r.WebSearch, want.WebSearch)
	}
}
