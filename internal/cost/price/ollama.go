package price

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Ollama is the provider of the Ollama Cloud models.
const Ollama = "ollama"

// ollamaURL is Ollama Cloud's pricing page. It publishes no machine readable
// list, the page's model table is the source.
const ollamaURL = "https://ollama.com/pricing"

// ollamaLimit caps the page, about 50 KB today.
const ollamaLimit = 4 << 20

// The acceptance rules of a scraped table: a page that lists fewer models,
// drops more of the last ones or moves a price further is a changed layout
// or a broken page, not a new price list.
const (
	ollamaMinModels = 5
	ollamaMinKept   = 0.8
	ollamaMaxFactor = 10
)

// ollamaRate bills claude's token classes the way Ollama Cloud does: a cache
// write is plain input, and a model without a cached input price bills
// cached tokens as input.
func ollamaRate(input, cached, output float64) Rate {
	if cached == 0 {
		cached = input
	}
	return Rate{Input: input, Output: output, CacheRead: cached, CacheWrite5m: input, CacheWrite1h: input}
}

// billOllama completes rates written the way the page lists them, input,
// cached input and output, into rates of every class.
func billOllama(rates map[string]Rate) {
	for model, r := range rates {
		full := ollamaRate(r.Input, r.CacheRead, r.Output)
		if r.OffPeak != nil {
			off := ollamaRate(r.OffPeak.Input, r.OffPeak.CacheRead, r.OffPeak.Output)
			full.OffPeak = &off
		}
		rates[model] = full
	}
}

// At is the rate of a call at a moment: the off-peak rate where the model
// has one and the moment lies outside 12:00 to 18:00 UTC on a weekday.
func (r Rate) At(at time.Time) Rate {
	if r.OffPeak == nil {
		return r
	}
	u := at.UTC()
	if u.Weekday() == time.Saturday || u.Weekday() == time.Sunday || u.Hour() < 12 || u.Hour() >= 18 {
		return *r.OffPeak
	}
	return r
}

// lookupOllama finds a model under the other names Ollama hands out: the
// app's name:cloud, and the API's name with a size tag where the page lists
// the bare name and no other size of it.
func lookupOllama(rates map[string]Rate, model string) (Rate, bool) {
	model = strings.TrimSuffix(model, ":cloud")
	if r, ok := rates[model]; ok {
		return r, true
	}
	base, _, tagged := strings.Cut(model, ":")
	r, ok := rates[base]
	if !tagged || !ok {
		return Rate{}, false
	}
	for name := range rates {
		if strings.HasPrefix(name, base+":") {
			return Rate{}, false
		}
	}
	return r, true
}

// parseOllama reads the model table of the pricing page: the first table
// whose header names an input, a cached input and an output column. A row
// "<model> (Off-Peak)" holds that model's off-peak rate. A price of "-" is
// none, which a cached input price may be and the others may not; a model
// without one is left out.
func parseOllama(page []byte) (map[string]Rate, error) {
	z := html.NewTokenizer(bytes.NewReader(page))
	var rows [][]string
	var row []string
	inCell, inTable := false, false
	endRow := func() {
		if row != nil {
			rows = append(rows, row)
		}
		row, inCell = nil, false
	}
	for {
		switch z.Next() {
		case html.ErrorToken:
			return nil, errors.New("the pricing page holds no complete price table")
		case html.StartTagToken, html.SelfClosingTagToken:
			switch name, _ := z.TagName(); string(name) {
			case "table":
				rows, row, inCell, inTable = nil, nil, false, true
			case "tr":
				endRow()
				row = []string{}
			case "td", "th":
				if row != nil {
					row = append(row, "")
					inCell = true
				}
			}
		case html.EndTagToken:
			switch name, _ := z.TagName(); string(name) {
			case "td", "th":
				inCell = false
			case "tr":
				endRow()
			case "table":
				endRow()
				if !inTable {
					continue
				}
				inTable = false
				if rates, ok, err := ollamaTable(rows); ok {
					return rates, err
				}
			}
		case html.TextToken:
			if inCell && row != nil {
				row[len(row)-1] += string(z.Text())
			}
		}
	}
}

// ollamaTable reads one table's rows. ok says the table is the price table.
func ollamaTable(rows [][]string) (rates map[string]Rate, ok bool, err error) {
	col := map[string]int{}
	header := -1
	for i, cells := range rows {
		for j, c := range cells {
			switch h := strings.ToLower(c); {
			case strings.Contains(h, "cached"):
				col["cached"] = j
			case strings.Contains(h, "input"):
				col["input"] = j
			case strings.Contains(h, "output"):
				col["output"] = j
			case strings.Contains(h, "model"):
				col["model"] = j
			}
		}
		if len(col) == 4 {
			header = i
			break
		}
		clear(col)
	}
	if header < 0 {
		return nil, false, nil
	}
	rates = map[string]Rate{}
	offPeak := map[string]Rate{}
	for _, cells := range rows[header+1:] {
		if len(cells) <= max(col["model"], col["input"], col["cached"], col["output"]) {
			continue
		}
		name := strings.Join(strings.Fields(cells[col["model"]]), " ")
		into := rates
		if base, ok := cutSuffixFold(name, "(off-peak)"); ok {
			name, into = strings.TrimSpace(base), offPeak
		}
		if !modelName(name) {
			continue
		}
		if _, dup := into[name]; dup {
			return nil, true, fmt.Errorf("the pricing page lists %s twice", name)
		}
		input, okIn, err := pagePrice(cells[col["input"]])
		if err != nil {
			return nil, true, fmt.Errorf("the pricing page prices %s as %w", name, err)
		}
		cached, _, err := pagePrice(cells[col["cached"]])
		if err != nil {
			return nil, true, fmt.Errorf("the pricing page prices %s as %w", name, err)
		}
		output, okOut, err := pagePrice(cells[col["output"]])
		if err != nil {
			return nil, true, fmt.Errorf("the pricing page prices %s as %w", name, err)
		}
		if okIn && okOut {
			into[name] = ollamaRate(input, cached, output)
		}
	}
	for name, off := range offPeak {
		if r, ok := rates[name]; ok {
			r.OffPeak = &off
			rates[name] = r
		}
	}
	return rates, true, nil
}

func cutSuffixFold(s, suffix string) (string, bool) {
	if len(s) < len(suffix) || !strings.EqualFold(s[len(s)-len(suffix):], suffix) {
		return s, false
	}
	return s[:len(s)-len(suffix)], true
}

// modelName says whether a cell reads as a model name, the way Ollama
// spells them: lower case letters, digits and . _ - : and nothing else.
func modelName(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._-:", r)) {
			return false
		}
	}
	return true
}

// pagePrice reads one price cell like "$0.30". ok is false for "-", no price.
func pagePrice(cell string) (v float64, ok bool, err error) {
	s := strings.Join(strings.Fields(cell), "")
	if s == "-" || s == "–" || s == "—" {
		return 0, false, nil
	}
	s = strings.TrimPrefix(s, "$")
	v, err = strconv.ParseFloat(s, 64)
	if err != nil || strings.ContainsAny(s, "xXpP_") {
		return 0, false, fmt.Errorf("%q, not a price", cell)
	}
	return v, true, nil
}

// acceptOllama checks a scraped table against the last good one and the
// models the last page listed. Every price must be plausible, the page must
// list enough models, keep most of the last ones and move no price by more
// than ollamaMaxFactor. It returns the table to store: the scraped rates over
// the stored ones, so a model the page drops keeps its last price for the
// calls still to be priced.
func acceptOllama(scraped, last, stored map[string]Rate, listed []string) (map[string]Rate, error) {
	for model, r := range scraped {
		if !r.plausible() {
			return nil, fmt.Errorf("the pricing page prices %s implausibly", model)
		}
	}
	if len(scraped) < ollamaMinModels {
		return nil, fmt.Errorf("the pricing page lists %d models, fewer than %d", len(scraped), ollamaMinModels)
	}
	kept := 0
	for _, model := range listed {
		if _, ok := scraped[model]; ok {
			kept++
		}
	}
	if float64(kept) < ollamaMinKept*float64(len(listed)) {
		return nil, fmt.Errorf("the pricing page lists %d of the last %d models", kept, len(listed))
	}
	for model, r := range scraped {
		if before, ok := last[model]; ok && jumped(before, r) {
			return nil, fmt.Errorf("the pricing page moves the price of %s by more than %dx", model, ollamaMaxFactor)
		}
	}
	out := map[string]Rate{}
	for model, r := range stored {
		out[model] = r
	}
	for model, r := range scraped {
		out[model] = r
	}
	return out, nil
}

// jumped says whether a price of b moved by more than ollamaMaxFactor
// against a, off-peak prices included where both have them.
func jumped(a, b Rate) bool {
	pairs := [][2]float64{{a.Input, b.Input}, {a.CacheRead, b.CacheRead}, {a.Output, b.Output}}
	if a.OffPeak != nil && b.OffPeak != nil {
		pairs = append(pairs, [2]float64{a.OffPeak.Input, b.OffPeak.Input}, [2]float64{a.OffPeak.CacheRead, b.OffPeak.CacheRead}, [2]float64{a.OffPeak.Output, b.OffPeak.Output})
	}
	return slices.ContainsFunc(pairs, func(p [2]float64) bool {
		if p[0] == p[1] {
			return false
		}
		return p[0] <= 0 || p[1] <= 0 || p[0]/p[1] > ollamaMaxFactor || p[1]/p[0] > ollamaMaxFactor
	})
}
