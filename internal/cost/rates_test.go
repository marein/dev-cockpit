package cost

import (
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
