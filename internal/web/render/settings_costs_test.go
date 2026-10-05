package render

import (
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
	"github.com/marein/dev-cockpit/internal/cost/price"
)

func TestRatePrice(t *testing.T) {
	for v, want := range map[float64]string{0: "", 5: "$5.00", 12.5: "$12.50", 0.3: "$0.30", 0.075: "$0.075", 0.09999999999999999: "$0.10", 0.19999999999999998: "$0.20", 3.75: "$3.75"} {
		if got := ratePrice(v); got != want {
			t.Errorf("ratePrice(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestSettingsCostsNamesNoPastOrSwitchedOffRefresh(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	rates := []cost.ModelRate{
		{Coder: "claude", Model: "opus", Priced: true, Rate: price.Rate{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10}},
		{Coder: "claude", Model: "nemotron"},
	}
	d := NewSettingsCosts(price.Status{Enabled: true, NextAt: now.Add(-time.Minute)}, rates, 3, now)
	if d.FetchedAt != "" || d.NextAt != "" {
		t.Fatalf("snapshot with a due refresh = %+v", d)
	}
	if d.Models[0].Input != "$5.00" || d.Models[0].WebSearch != "" || d.Models[1].Priced || d.Models[1].Input != "" {
		t.Fatalf("models = %+v", d.Models)
	}
	d = NewSettingsCosts(price.Status{Enabled: false, FetchedAt: now.Add(-time.Hour), NextAt: now.Add(time.Hour)}, nil, 3, now)
	if d.FetchedAt != "2026-10-02T11:00:00Z" || d.NextAt != "" {
		t.Fatalf("switched off = %+v", d)
	}
	d = NewSettingsCosts(price.Status{Enabled: true, NextAt: now.Add(time.Hour)}, nil, 3, now)
	if d.NextAt != "2026-10-02T13:00:00Z" {
		t.Fatalf("scheduled = %+v", d)
	}
}

func TestTheMonthsKeptAreNamedOldestFirst(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for n, want := range map[int]string{
		2:  "September and October",
		3:  "August, September and October",
		11: "December, January, February, March, April, May, June, July, August, September and October",
		13: "October 2025 to October 2026",
	} {
		if got := keptMonths(n, now); got != want {
			t.Errorf("keptMonths(%d) = %q, want %q", n, got, want)
		}
	}
	if d := NewSettingsCosts(price.Status{}, nil, 3, now); d.KeptMonths != "August, September and October" || d.MinRetention != cost.MinRetention {
		t.Fatalf("settings %+v", d)
	}
}

func TestSettingsCostsListTheOllamaTableAndItsScrape(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	got := NewOllamaPrices(price.Snapshot()[price.Ollama])
	if len(got) != 17 || got[0].Model != "deepseek-v4-pro" {
		t.Fatalf("prices = %+v", got)
	}
	if got[0].OffPeak != "$0.66 / $0.022 / $1.98" || got[0].Cached != "$0.044" {
		t.Fatalf("deepseek-v4-pro = %+v", got[0])
	}
	for _, p := range got {
		if p.Model == "mistral-large-3" && (p.Cached != "$0.50" || p.OffPeak != "") {
			t.Fatalf("mistral-large-3 = %+v", p)
		}
	}
	s := price.Status{Enabled: true, Ollama: price.Scrape{FetchedAt: now.Add(-time.Hour), NextAt: now.Add(time.Hour), Outcome: "Took 17 models"}}
	d := NewSettingsCosts(s, nil, 3, now)
	if d.OllamaFetchedAt != "2026-10-05T11:00:00Z" || d.OllamaNextAt != "2026-10-05T13:00:00Z" || d.OllamaOutcome != "Took 17 models" || d.FetchedAt != "" {
		t.Fatalf("ollama status = %+v", d)
	}
	s.Enabled = false
	if d := NewSettingsCosts(s, nil, 3, now); d.OllamaNextAt != "" {
		t.Fatalf("switched off names a next fetch: %+v", d)
	}
}
