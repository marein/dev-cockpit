// Package price answers what a model call costs at a provider's public list
// price. Tables are keyed by provider and model. The embedded snapshot is
// written by hand from the providers' own pricing pages and is what a fresh
// or offline install prices with; Book refreshes it once a day from a public
// price list and keeps the last good table in the state directory.
package price

import (
	_ "embed"
	"encoding/json"
	"math"
	"regexp"
	"strings"
)

// Anthropic is the provider of the claude models.
const Anthropic = "anthropic"

// Tokens is what one call or one bucket used, per price class. Output holds
// the thinking tokens, a provider bills them as output.
type Tokens struct {
	Input        int64 `json:"input,omitempty"`
	Output       int64 `json:"output,omitempty"`
	CacheRead    int64 `json:"cache_read,omitempty"`
	CacheWrite5m int64 `json:"cache_write_5m,omitempty"`
	CacheWrite1h int64 `json:"cache_write_1h,omitempty"`
	WebSearch    int64 `json:"web_search,omitempty"`
}

// Add sums b into t.
func (t *Tokens) Add(b Tokens) {
	t.Input += b.Input
	t.Output += b.Output
	t.CacheRead += b.CacheRead
	t.CacheWrite5m += b.CacheWrite5m
	t.CacheWrite1h += b.CacheWrite1h
	t.WebSearch += b.WebSearch
}

// Rate is one model's list price. Token prices are USD per million tokens,
// the unit of the pricing pages, web search is USD per thousand requests.
// Fast is the factor of the fast mode, zero where the model has none.
type Rate struct {
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheRead    float64 `json:"cache_read"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	WebSearch    float64 `json:"web_search,omitempty"`
	Fast         float64 `json:"fast,omitempty"`
}

// Cost prices one usage. The fast factor applies to every token class: the
// cache prices are multiples of the input price and follow it.
func (r Rate) Cost(t Tokens, fast bool) float64 {
	tokens := float64(t.Input)*r.Input +
		float64(t.Output)*r.Output +
		float64(t.CacheRead)*r.CacheRead +
		float64(t.CacheWrite5m)*r.CacheWrite5m +
		float64(t.CacheWrite1h)*r.CacheWrite1h
	if fast && r.Fast > 0 {
		tokens *= r.Fast
	}
	return tokens/1e6 + float64(t.WebSearch)*r.WebSearch/1e3
}

// maxPerMillion bounds every token price, the fast factor applied, and the
// web search price per thousand requests. The dearest list price is $50 per
// million tokens, twenty times that is a broken list, not a new model, and
// keeps every amount a call can book finite.
const maxPerMillion = 1000

// plausible says whether every price is a number from zero up to the bound.
func (r Rate) plausible() bool {
	fast := math.Max(r.Fast, 1)
	for _, v := range []float64{r.Input, r.Output, r.CacheRead, r.CacheWrite5m, r.CacheWrite1h} {
		if !(v >= 0 && v*fast < maxPerMillion) {
			return false
		}
	}
	return r.Fast >= 0 && r.WebSearch >= 0 && r.WebSearch < maxPerMillion
}

// valid says whether the rate can price a call: plausible and no token class
// left at zero.
func (r Rate) valid() bool {
	return r.plausible() && r.Input > 0 && r.Output > 0 && r.CacheRead > 0 && r.CacheWrite5m > 0 && r.CacheWrite1h > 0
}

// plausible says whether every rate of the table is.
func (t Table) plausible() bool {
	for _, models := range t {
		for _, r := range models {
			if !r.plausible() {
				return false
			}
		}
	}
	return true
}

// Table is the rates of every provider, provider first, then model.
type Table map[string]map[string]Rate

//go:embed snapshot.json
var snapshotJSON []byte

// Snapshot is the table embedded in the binary.
func Snapshot() Table {
	var t Table
	if err := json.Unmarshal(snapshotJSON, &t); err != nil {
		panic("price: embedded snapshot: " + err.Error())
	}
	return t
}

var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// Lookup finds a model's rate. A context window suffix like [1m] names the
// same model at the same price, a date suffix names a snapshot of it.
func (t Table) Lookup(provider, model string) (Rate, bool) {
	rates := t[provider]
	model = BaseModel(strings.TrimSpace(model))
	if r, ok := rates[model]; ok {
		return r, true
	}
	r, ok := rates[dateSuffix.ReplaceAllString(model, "")]
	return r, ok
}

// BaseModel is the name a model is booked under: without a context window
// suffix, the way the provider names it on a call.
func BaseModel(model string) string {
	if i := strings.IndexByte(model, '['); i > 0 {
		return model[:i]
	}
	return model
}

// covers reports whether t prices every model of base with a usable rate.
func (t Table) covers(base Table) bool {
	for provider, models := range base {
		for model := range models {
			r, ok := t[provider][model]
			if !ok || !r.valid() {
				return false
			}
		}
	}
	return true
}
