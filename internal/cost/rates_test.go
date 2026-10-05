package cost

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/cost/price"
)

type pricedSource struct {
	fakeSource
	rates map[string]price.Rate
}

func (p *pricedSource) Rate(model string) (price.Rate, bool) {
	r, ok := p.rates[model]
	return r, ok
}

func TestModelRatesNameEverySeenModelOnceWithItsPriceNow(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	opus := price.Rate{Input: 5, Output: 25}
	a := &pricedSource{
		fakeSource: fakeSource{entries: []Entry{
			{Session: "x", At: now, Model: "opus", USD: 1},
			{Session: "y", At: now.Add(-48 * time.Hour), Model: "opus", USD: 1},
			{Session: "y", At: now, Model: "nemotron", Tokens: Tokens{Input: 1}, Unpriced: true},
		}},
		rates: map[string]price.Rate{"opus": opus},
	}
	b := &fakeSource{coder: "opencode", entries: []Entry{{Session: "z", At: now, Model: "gpt", USD: 2}}}
	s := newTestService(t, t.TempDir(), owners{}, &now, a, b)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	got := s.ModelRates()
	want := []ModelRate{
		{Coder: "claude", Model: "nemotron"},
		{Coder: "claude", Model: "opus", Rate: opus, Priced: true},
		{Coder: "opencode", Model: "gpt"},
	}
	if len(got) != len(want) {
		t.Fatalf("rates = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rate %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// repricingSource prices a model from the moment the test names a rate for
// it, at that rate per token, and counts what it was asked.
type repricingSource struct {
	fakeSource
	rates map[string]float64
	asked int
}

func (p *repricingSource) Reprice(model string, at time.Time, tokens Tokens) (float64, bool) {
	p.asked++
	r, ok := p.rates[model]
	return r * float64(tokens.Input+tokens.CacheRead), ok
}

// A row booked without a list price is priced once its model has one, on
// the hourly maintenance, merged into a priced row of its bucket, and never
// again after.
func TestUnpricedRowsArePricedOnceTheirModelHasAPrice(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	src := &repricingSource{rates: map[string]float64{}}
	src.entries = []Entry{
		{Session: "s1", At: now.Add(-3 * time.Hour), Model: "nemotron", Tokens: Tokens{Input: 100}, Unpriced: true},
		{Session: "s1", At: now.Add(-40 * 24 * time.Hour), Model: "nemotron", Tokens: Tokens{Input: 50, CacheRead: 50}, Unpriced: true},
		{Session: "s1", At: now.Add(-time.Hour), Model: "local", Tokens: Tokens{Input: 7}, Unpriced: true},
	}
	dir := t.TempDir()
	s := newTestService(t, dir, owners{}, &now, src)
	note(t, s.Collect())
	if r := buildReport(s.rows(), now, Range30d); r.Unpriced != 1 || total(r) != 0 {
		t.Fatalf("before a price: unpriced %d total %v", r.Unpriced, total(r))
	}

	src.rates["nemotron"] = 0.01
	src.entries = []Entry{{Session: "s1", At: now.Add(-3 * time.Hour), Model: "nemotron", Tokens: Tokens{Input: 10}, USD: 0.1}}
	now = now.Add(maintainEvery)
	note(t, s.Collect())
	unpriced, usd := 0, 0.0
	for _, r := range s.rows() {
		usd += r.USD
		if r.Unpriced {
			unpriced++
			if r.Model != "local" {
				t.Fatalf("a priced model is still unpriced: %+v", r)
			}
		}
	}
	if unpriced != 1 || !near(usd, 1+1+0.1) || len(s.rows()) != 3 {
		t.Fatalf("after the price: %d unpriced, %v USD, rows %+v", unpriced, usd, s.rows())
	}

	before, err := os.ReadFile(filepath.Join(dir, "cost", "rows-2026-10.json"))
	if err != nil {
		t.Fatal(err)
	}
	src.rates["nemotron"] = 1
	src.asked = 0
	now = now.Add(maintainEvery)
	note(t, s.Collect())
	after, _ := os.ReadFile(filepath.Join(dir, "cost", "rows-2026-10.json"))
	if string(before) != string(after) || src.asked != 1 {
		t.Fatalf("a second run changed the books or asked for priced rows (%d asks)", src.asked)
	}
}
