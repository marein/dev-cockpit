package cost

import (
	"sort"

	"github.com/marein/dev-cockpit/internal/cost/price"
)

// Pricer is a source that can say what a model costs right now, the rate
// it books a call of that model with.
type Pricer interface {
	Rate(model string) (price.Rate, bool)
}

// ModelRate is a model the ledger booked and its list price now. Priced is
// false where the coder's source knows no list price for it.
type ModelRate struct {
	Coder  string
	Model  string
	Rate   price.Rate
	Priced bool
}

// ModelRates are the models the ledger has seen, per coder, with the rate
// their source prices them at now.
func (s *Service) ModelRates() []ModelRate {
	pricers := map[string]Pricer{}
	for _, src := range s.sources {
		if p, ok := src.(Pricer); ok {
			pricers[src.Coder()] = p
		}
	}
	seen := map[[2]string]bool{}
	var out []ModelRate
	for _, r := range s.read() {
		key := [2]string{r.Coder, r.Model}
		if r.Model == "" || seen[key] {
			continue
		}
		seen[key] = true
		m := ModelRate{Coder: r.Coder, Model: r.Model}
		if p := pricers[r.Coder]; p != nil {
			m.Rate, m.Priced = p.Rate(r.Model)
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Coder != out[j].Coder {
			return out[i].Coder < out[j].Coder
		}
		return out[i].Model < out[j].Model
	})
	return out
}
