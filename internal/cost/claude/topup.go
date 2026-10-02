package claude

import (
	"encoding/json"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
	"github.com/marein/dev-cockpit/internal/cost/price"
)

// topUpStep is the smallest top-up booked. The comparison is cumulative, so
// a smaller difference is not lost, it waits until it shows on the page; and
// two sums of the same calls priced the same way differ by far less.
const topUpStep = 0.01

type costStateLine struct {
	Type                string                     `json:"type"`
	TotalCostUSD        *float64                   `json:"totalCostUSD"`
	StartTime           int64                      `json:"startTime"`
	TotalDuration       int64                      `json:"totalDuration"`
	ModelUsage          map[string]modelUsageEntry `json:"modelUsage"`
	HasUnknownModelCost bool                       `json:"hasUnknownModelCost"`
}

type modelUsageEntry struct {
	CostUSD *float64 `json:"costUSD"`
}

// parseCostState reads the per model spend of one cost-state line, the
// models folded to the names their calls carry. unknown says claude priced
// a model it has no price for, at a price of its own choosing. end is when
// the process the total is of ended, zero where the line does not say.
func parseCostState(line []byte) (models map[string]float64, end time.Time, unknown bool, ok bool) {
	var l costStateLine
	if json.Unmarshal(line, &l) != nil || l.Type != "cost-state" || l.TotalCostUSD == nil {
		return nil, end, false, false
	}
	if l.StartTime > 0 {
		end = time.UnixMilli(l.StartTime + l.TotalDuration)
	}
	models = map[string]float64{}
	for name, u := range l.ModelUsage {
		if u.CostUSD == nil {
			continue
		}
		models[price.BaseModel(name)] += *u.CostUSD
	}
	return models, end, l.HasUnknownModelCost, true
}

// costState tops a session up to claude's own running total, per model. The
// total covers the whole session over every resume and every file of it, so
// what the session booked, what it copied from another session and what was
// topped up before are taken off; a total below that books nothing, a killed
// process leaves its calls in the transcript and its total behind. A
// session whose total holds an unknown model is never topped up, claude
// priced that model at a price of its own.
func (r *reader) costState(t transcript, c *fileCursor, line []byte) {
	if t.sub {
		return
	}
	models, end, unknown, ok := parseCostState(line)
	if !ok {
		return
	}
	ss := r.session(t.session)
	if unknown {
		ss.Unknown = true
	}
	if ss.Unknown {
		return
	}
	// A file of its exit lines alone is booked when the process ended, not
	// when the file was written: a backup import writes it again.
	at := c.At
	if at.IsZero() {
		at = end
	}
	if at.IsZero() {
		at = t.modTime
		if at.IsZero() || at.After(r.now) {
			at = r.now
		}
	}
	for model, total := range models {
		if _, priced := r.table.Lookup(price.Anthropic, model); !priced {
			continue
		}
		gap := total - ss.Booked[model] - ss.Copied[model] - ss.TopUp[model]
		if gap < topUpStep {
			continue
		}
		// An absurd gap goes on to the ledger, which refuses and logs it,
		// but stays out of the sums, which must stay finite to be stored.
		if cost.Usable(gap) {
			addUSD(&ss.TopUp, model, gap)
		}
		r.entries = append(r.entries, cost.Entry{
			Session: t.session,
			CWD:     r.cwdOf(t, c),
			At:      at,
			Model:   model,
			USD:     gap,
			TopUp:   true,
		})
	}
}
