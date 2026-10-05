package price

import (
	"context"
	"io"
	"log"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// ollamaPage is Ollama's pricing page as fetched on 2026-10-05.
func ollamaPage(t testing.TB) []byte {
	t.Helper()
	page, err := os.ReadFile(filepath.Join("testdata", "ollama-pricing.html"))
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestTheSnapshotHoldsTheOllamaPageBilledInClaudesClasses(t *testing.T) {
	s := Snapshot()[Ollama]
	scraped, err := parseOllama(ollamaPage(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 17 || !reflect.DeepEqual(s, scraped) {
		t.Fatalf("the snapshot is not the page:\n%+v\n%+v", s, scraped)
	}
	for model, r := range s {
		if !r.valid() || r.CacheWrite5m != r.Input || r.CacheWrite1h != r.Input || r.WebSearch != 0 || r.Fast != 0 {
			t.Errorf("%s is not billed the way Ollama bills: %+v", model, r)
		}
	}
	mistral := s["mistral-large-3"]
	if mistral.Input != 0.5 || mistral.CacheRead != 0.5 || mistral.Output != 1.5 || mistral.OffPeak != nil {
		t.Fatalf("a model without a cached price = %+v", mistral)
	}
	pro := s["deepseek-v4-pro"]
	if pro.OffPeak == nil || pro.OffPeak.Input != 0.66 || pro.OffPeak.CacheRead != 0.022 || pro.OffPeak.Output != 1.98 || pro.OffPeak.CacheWrite1h != 0.66 {
		t.Fatalf("deepseek-v4-pro off-peak = %+v", pro.OffPeak)
	}
	if s["gpt-oss:120b"].Input != 0.15 || s["gpt-oss:20b"].Input != 0.07 {
		t.Fatal("the two sizes of gpt-oss share a price")
	}
}

func TestOffPeakIsOutsideNoonToSixUTCOnWeekdays(t *testing.T) {
	off := Rate{Input: 1}
	r := Rate{Input: 2, OffPeak: &off}
	monday := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		at   time.Time
		want float64
	}{
		{monday.Add(11*time.Hour + 59*time.Minute + 59*time.Second), 1},
		{monday.Add(12 * time.Hour), 2},
		{monday.Add(17*time.Hour + 59*time.Minute), 2},
		{monday.Add(18 * time.Hour), 1},
		{monday.Add(4*24*time.Hour + 15*time.Hour), 2},
		{monday.Add(5*24*time.Hour + 15*time.Hour), 1},
		{monday.Add(6*24*time.Hour + 15*time.Hour), 1},
		{monday.Add(14 * time.Hour).In(time.FixedZone("-10", -10*3600)), 2},
	} {
		if got := r.At(c.at).Input; got != c.want {
			t.Errorf("%s = %v, want %v", c.at, got, c.want)
		}
	}
	if (Rate{Input: 3}).At(monday).Input != 3 {
		t.Fatal("a model without an off-peak rate changed off-peak")
	}
}

func TestOllamaNamesFindTheirPagePrice(t *testing.T) {
	s := Snapshot()
	for name, want := range map[string]float64{
		"nemotron-3-ultra":         0.1,
		"nemotron-3-super":         0.015,
		"gpt-oss:120b":             0.15,
		"gpt-oss:20b":              0.07,
		"gpt-oss:120b:cloud":       0.15,
		"gemma4:cloud":             0.14,
		"gemma4:31b":               0.14,
		"nemotron-3-nano:30b":      0.06,
		"mistral-large-3:675b":     0.5,
		"deepseek-v4-pro:0813":     1.32,
		"nemotron-3-ultra[1m]":     0.1,
		" nemotron-3-ultra:cloud ": 0.1,
	} {
		if r, ok := s.Lookup(Ollama, name); !ok || r.Input != want {
			t.Errorf("%q = %+v, %v, want input %v", name, r, ok, want)
		}
	}
	for _, name := range []string{"gpt-oss", "gpt-oss:7b", "gpt-oss:cloud", "qwen3:8b", "qwen2.5-coder:1.5b-base", "gemma4-31b", "", ":cloud", "claude-opus-5-5"} {
		if r, ok := s.Lookup(Ollama, name); ok {
			t.Errorf("%q has a price %+v", name, r)
		}
	}
	if _, ok := s.Lookup(Anthropic, "nemotron-3-ultra"); ok {
		t.Fatal("an Ollama model is priced as a Claude model")
	}
}

func TestParseOllamaReadsTheRealPage(t *testing.T) {
	rates, err := parseOllama(ollamaPage(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 17 {
		t.Fatalf("%d models: %v", len(rates), slices.Sorted(maps.Keys(rates)))
	}
	flash := rates["deepseek-v4.1-flash"]
	if flash.Input != 0.3 || flash.CacheRead != 0.006 || flash.Output != 1.2 || flash.OffPeak == nil || flash.OffPeak.Output != 0.6 {
		t.Fatalf("deepseek-v4.1-flash = %+v %+v", flash, flash.OffPeak)
	}
	if r := rates["nemotron-3-nano"]; r.CacheRead != 0.06 || r.OffPeak != nil {
		t.Fatalf("a dash is not the input price: %+v", r)
	}
	if _, ok := rates["deepseek-v4.1-flash (off-peak)"]; ok {
		t.Fatal("an off-peak row is a model of its own")
	}
}

// mutate rewrites the price table of the real page.
func mutate(t *testing.T, from, to string) []byte {
	t.Helper()
	page := string(ollamaPage(t))
	if !strings.Contains(page, from) {
		t.Fatalf("the page holds no %q", from)
	}
	return []byte(strings.ReplaceAll(page, from, to))
}

func TestParseOllamaToleratesTheSpellingOfOnePrice(t *testing.T) {
	page := mutate(t, `text-right">$0.14</td>`, "text-right\">\n   $ 0.14 \n</td>")
	page = []byte(strings.Replace(string(page), `>$0.05<`, `>&#36;0.05<`, 1))
	page = []byte(strings.Replace(string(page), `>-<`, `> &ndash; <`, 1))
	page = []byte(strings.Replace(string(page), `deepseek-v4-pro (Off-Peak)`, "deepseek-v4-pro  (off-peak)", 1))
	rates, err := parseOllama(page)
	if err != nil {
		t.Fatal(err)
	}
	if r := rates["gemma4"]; r.Input != 0.14 || r.CacheRead != 0.05 {
		t.Fatalf("gemma4 = %+v", r)
	}
	if r := rates["mistral-large-3"]; r.CacheRead != 0.5 {
		t.Fatalf("an en dash is not the input price: %+v", r)
	}
	if r := rates["deepseek-v4-pro"]; r.OffPeak == nil || r.OffPeak.Input != 0.66 {
		t.Fatalf("the off-peak row is lost: %+v", r)
	}
}

func TestParseOllamaRefusesWhatIsNoPriceTable(t *testing.T) {
	page := string(ollamaPage(t))
	cut := strings.Index(page, "nemotron-3-super")
	for what, body := range map[string][]byte{
		"no table":              mutate(t, "<table", "<div"),
		"another header":        mutate(t, ">Cached input<", ">Batch<"),
		"a price in words":      mutate(t, ">$0.14<", ">fourteen cents<"),
		"a hex price":           mutate(t, ">$0.14<", ">0x1p-3<"),
		"a model listed twice":  mutate(t, ">glm-5.2<", ">glm-5.3<"),
		"truncated in the rows": []byte(page[:cut]),
		"empty":                 nil,
		"not html":              []byte("\x00\xff{\"input\":1}"),
	} {
		if rates, err := parseOllama(body); err == nil {
			t.Errorf("%s parses into %d models", what, len(rates))
		}
	}
}

func TestParseOllamaFindsTheTableAmongOthers(t *testing.T) {
	page := mutate(t, `<div class="overflow-x-auto`, `<table><tr><th>Plan</th><th>Price</th></tr><tr><td>Pro</td><td>$20</td></tr></table><div class="overflow-x-auto`)
	rates, err := parseOllama(page)
	if err != nil || len(rates) != 17 {
		t.Fatalf("%d models, %v", len(rates), err)
	}
	swapped := strings.NewReplacer(">Input<", ">Output<", ">Output<", ">Input<").Replace(string(ollamaPage(t)))
	if rates, err := parseOllama([]byte(swapped)); err != nil || len(rates) != 17 || rates["gemma4"].Output != 0.14 {
		t.Fatalf("columns are read by position, not header: %v %+v", err, rates["gemma4"])
	}
}

func scrapedPage(t *testing.T) map[string]Rate {
	t.Helper()
	rates, err := parseOllama(ollamaPage(t))
	if err != nil {
		t.Fatal(err)
	}
	return rates
}

func TestAcceptOllamaRules(t *testing.T) {
	last := Snapshot()[Ollama]
	listed := slices.Collect(maps.Keys(last))
	if _, err := acceptOllama(scrapedPage(t), last, nil, listed); err != nil {
		t.Fatalf("the real page is refused: %v", err)
	}
	for what, change := range map[string]func(map[string]Rate){
		"not finite":      func(m map[string]Rate) { m["gemma4"] = Rate{Input: math.Inf(1), Output: 1, CacheRead: 1} },
		"not a number":    func(m map[string]Rate) { m["gemma4"] = Rate{Input: math.NaN(), Output: 1, CacheRead: 1} },
		"negative":        func(m map[string]Rate) { m["gemma4"] = Rate{Input: -0.14, Output: 0.4, CacheRead: 0.05} },
		"above the bound": func(m map[string]Rate) { m["new-model"] = Rate{Input: maxPerMillion, Output: 1, CacheRead: 1} },
		"off-peak bad": func(m map[string]Rate) {
			r := m["deepseek-v4-pro"]
			r.OffPeak = &Rate{Input: -1}
			m["deepseek-v4-pro"] = r
		},
		"most models gone": func(m map[string]Rate) {
			for _, k := range slices.Sorted(maps.Keys(m))[:4] {
				delete(m, k)
			}
		},
		"ten times dearer": func(m map[string]Rate) {
			r := m["kimi-k3"]
			r.Output *= 10.5
			m["kimi-k3"] = r
		},
		"ten times cheaper": func(m map[string]Rate) {
			r := m["gemma4"]
			r.CacheRead /= 11
			m["gemma4"] = r
		},
		"free now": func(m map[string]Rate) {
			r := m["gemma4"]
			r.Input = 0
			m["gemma4"] = r
		},
		"off-peak jumped": func(m map[string]Rate) {
			r := m["deepseek-v4-pro"]
			off := *r.OffPeak
			off.Output *= 20
			r.OffPeak = &off
			m["deepseek-v4-pro"] = r
		},
	} {
		scraped := scrapedPage(t)
		change(scraped)
		if _, err := acceptOllama(scraped, last, nil, listed); err == nil {
			t.Errorf("%s is accepted", what)
		}
	}

	few := map[string]Rate{}
	for _, k := range slices.Sorted(maps.Keys(last))[:4] {
		few[k] = last[k]
	}
	if _, err := acceptOllama(few, last, nil, nil); err == nil {
		t.Error("a page of four models is accepted")
	}

	// Within the rules: three models gone of seventeen, one new, one price
	// up nine times. The ones gone keep their last price.
	scraped := scrapedPage(t)
	for _, k := range []string{"glm-5.2", "kimi-k2.6", "minimax-m2.7"} {
		delete(scraped, k)
	}
	scraped["kimi-k4"] = Rate{Input: 5, Output: 20, CacheRead: 0.5, CacheWrite5m: 5, CacheWrite1h: 5}
	r := scraped["gemma4"]
	r.Output *= 9
	scraped["gemma4"] = r
	stored := map[string]Rate{"old-model": {Input: 1, Output: 1, CacheRead: 1, CacheWrite5m: 1, CacheWrite1h: 1}, "gemma4": last["gemma4"]}
	table, err := acceptOllama(scraped, last, stored, listed)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := table["old-model"]; !ok || table["kimi-k4"].Input != 5 || math.Abs(table["gemma4"].Output-3.6) > 1e-9 || len(table) != 16 {
		t.Fatalf("table = %+v", table)
	}
}

type ollamaServer struct {
	srv  *httptest.Server
	body atomic.Value
	hits atomic.Int32
	ua   atomic.Value
}

func newOllamaServer(t *testing.T, page []byte) *ollamaServer {
	t.Helper()
	o := &ollamaServer{}
	o.body.Store(page)
	o.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.hits.Add(1)
		o.ua.Store(r.UserAgent())
		w.Write(o.body.Load().([]byte))
	}))
	t.Cleanup(o.srv.Close)
	return o
}

// ollamaBook is a book whose LiteLLM list is never due, so a tick reads
// the Ollama page alone.
func ollamaBook(t *testing.T, dir string, o *ollamaServer, c *clock) *Book {
	t.Helper()
	b := NewBook(dir, func() bool { return true })
	b.url, b.ollamaURL, b.now = "http://127.0.0.1:0/", o.srv.URL, c.Now
	b.cache.NextAt = c.now.Add(100 * 365 * 24 * time.Hour)
	return b
}

func TestBookScrapesOllamaDailyAndKeepsTheLastGoodTable(t *testing.T) {
	var logs strings.Builder
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	page := mutate(t, ">$3.00<", ">$4.00<")
	o := newOllamaServer(t, page)
	dir := t.TempDir()
	c := &clock{now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	b := ollamaBook(t, dir, o, c)
	if r, _ := b.Table().Lookup(Ollama, "kimi-k3"); r.Input != 3 {
		t.Fatalf("a fresh install prices kimi-k3 with %+v, not the snapshot", r)
	}
	b.Tick(context.Background())
	if o.hits.Load() != 1 || o.ua.Load() != "dev-cockpit" {
		t.Fatalf("%d fetches, user agent %v", o.hits.Load(), o.ua.Load())
	}
	if r, _ := b.Table().Lookup(Ollama, "kimi-k3"); r.Input != 4 || r.CacheWrite5m != 4 {
		t.Fatalf("the scraped table is not used: %+v", r)
	}
	st := b.Status().Ollama
	if !st.FetchedAt.Equal(c.now) || !st.NextAt.Equal(c.now.Add(refreshEvery)) || st.Outcome != "Took 17 models" {
		t.Fatalf("status = %+v", st)
	}

	// A restart continues the schedule with the stored table.
	c.now = c.now.Add(time.Hour)
	b = ollamaBook(t, dir, o, c)
	b.Tick(context.Background())
	if o.hits.Load() != 1 {
		t.Fatalf("a restart fetched again: %d", o.hits.Load())
	}
	if r, _ := b.Table().Lookup(Ollama, "kimi-k3"); r.Input != 4 {
		t.Fatalf("a restart forgot the scraped table: %+v", r)
	}

	// A broken page keeps the last good table, retries in an hour and logs
	// its reason once.
	o.body.Store(mutate(t, ">$0.14<", ">$140.00<"))
	for range 3 {
		c.now = c.now.Add(refreshEvery)
		b.Tick(context.Background())
	}
	if o.hits.Load() != 4 {
		t.Fatalf("%d fetches", o.hits.Load())
	}
	if r, _ := b.Table().Lookup(Ollama, "kimi-k3"); r.Input != 4 {
		t.Fatalf("a refused page dropped the table: %+v", r)
	}
	if r, _ := b.Table().Lookup(Ollama, "gemma4"); r.Input != 0.14 {
		t.Fatalf("a refused page priced: %+v", r)
	}
	st = b.Status().Ollama
	if !st.NextAt.Equal(c.now.Add(retryAfter)) || !strings.HasPrefix(st.Outcome, "Kept the last table, ") || !strings.Contains(st.Outcome, "gemma4") {
		t.Fatalf("after a refusal status = %+v", st)
	}
	if n := strings.Count(logs.String(), "Ollama price refresh"); n != 1 {
		t.Fatalf("the reason was logged %d times:\n%s", n, logs.String())
	}
	if !b.Status().FetchedAt.IsZero() {
		t.Fatal("the Ollama scrape set the LiteLLM status")
	}

	// A page without a model of the last one keeps that model's price.
	o.body.Store(mutate(t, ">glm-5.2<", ">glm-5.4<"))
	c.now = c.now.Add(refreshEvery)
	b.Tick(context.Background())
	if r, ok := b.Table().Lookup(Ollama, "glm-5.2"); !ok || r.Input != 1.4 {
		t.Fatalf("a dropped model lost its price: %+v", r)
	}
	if _, ok := b.Table().Lookup(Ollama, "glm-5.4"); !ok {
		t.Fatal("a new model is not priced")
	}
	var stored cacheFile
	statefile.Load(filepath.Join(dir, "cost", "prices.json"), &stored)
	if stored.Ollama.Table["glm-5.2"].Input != 1.4 || slices.Contains(stored.Ollama.Listed, "glm-5.2") {
		t.Fatalf("stored = %+v", stored.Ollama)
	}
}

func TestABadStoredOllamaTableFallsBackToTheSnapshot(t *testing.T) {
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	dir := t.TempDir()
	path := filepath.Join(dir, "cost", "prices.json")
	for _, data := range []string{
		`{"ollama":{"table":{"gemma4":{"input":-1,"output":1,"cache_read":1,"cache_write_5m":1,"cache_write_1h":1}}}}`,
		`{"ollama":{"table":{"gemma4":{"input":1,"output":1,"cache_read":1,"cache_write_5m":1,"cache_write_1h":1,"off_peak":{"input":1e300}}}}}`,
		`{"ollama":`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		b := NewBook(dir, func() bool { return false })
		if r, ok := b.Table().Lookup(Ollama, "gemma4"); !ok || r.Input != 0.14 {
			t.Fatalf("%s prices gemma4 with %+v", data, r)
		}
		os.Remove(path + ".broken")
	}
}
