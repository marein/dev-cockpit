package render

import (
	"strconv"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
	"github.com/marein/dev-cockpit/internal/cost/price"
)

// SettingsCostsData feeds the costs settings page: the daily refresh switch
// and where the prices stand. FetchedAt and NextAt are RFC 3339, empty when
// there is no such moment.
type SettingsCostsData struct {
	Page
	SettingsNav  SettingsNav
	PriceRefresh bool
	FetchedAt    string
	NextAt       string
	Models       []CostModelRate
	// Retention is how many months of bookings stay, KeptMonths names them.
	Retention    int
	MinRetention int
	MaxRetention int
	KeptMonths   string
}

// CostModelRate is one model's list price as the page shows it, token
// prices per million tokens, web search per thousand requests.
type CostModelRate struct {
	Coder        string
	Model        string
	Priced       bool
	Input        string
	Output       string
	CacheRead    string
	CacheWrite5m string
	CacheWrite1h string
	WebSearch    string
}

// NewSettingsCosts words the price status. A refresh that is due already
// happens within the minute, so the page names no moment in the past.
func NewSettingsCosts(s price.Status, rates []cost.ModelRate, retention int, now time.Time) SettingsCostsData {
	d := SettingsCostsData{PriceRefresh: s.Enabled, Retention: retention, MinRetention: cost.MinRetention, MaxRetention: cost.MaxRetention}
	d.KeptMonths = keptMonths(retention, now)
	if !s.FetchedAt.IsZero() {
		d.FetchedAt = s.FetchedAt.Format(time.RFC3339)
	}
	if s.Enabled && s.NextAt.After(now) {
		d.NextAt = s.NextAt.Format(time.RFC3339)
	}
	for _, r := range rates {
		row := CostModelRate{Coder: r.Coder, Model: r.Model, Priced: r.Priced}
		if r.Priced {
			row.Input = ratePrice(r.Rate.Input)
			row.Output = ratePrice(r.Rate.Output)
			row.CacheRead = ratePrice(r.Rate.CacheRead)
			row.CacheWrite5m = ratePrice(r.Rate.CacheWrite5m)
			row.CacheWrite1h = ratePrice(r.Rate.CacheWrite1h)
			row.WebSearch = ratePrice(r.Rate.WebSearch)
		}
		d.Models = append(d.Models, row)
	}
	return d
}

// ratePrice writes a list price at least to the cent: the pricing pages
// quote fractions of a cent per million tokens. Six places drop the float
// noise of a feed price per token scaled to a million.
func ratePrice(v float64) string {
	if v <= 0 {
		return ""
	}
	s := strings.TrimRight(strconv.FormatFloat(v, 'f', 6, 64), "0")
	if i := strings.IndexByte(s, '.'); len(s)-i < 3 {
		s += strings.Repeat("0", 3-(len(s)-i))
	}
	return "$" + s
}

// keptMonths names the months n keeps at now, oldest first, the way the help
// text reads them: "August, September and October". More than a year reads
// as its first and its last month.
func keptMonths(n int, now time.Time) string {
	month := func(i int) time.Time { return cost.AddMonths(now, i+1-n) }
	if n > 12 {
		return month(0).Format("January 2006") + " to " + month(n-1).Format("January 2006")
	}
	names := make([]string, n)
	for i := range n {
		names[i] = month(i).Month().String()
	}
	return strings.Join(names[:n-1], ", ") + " and " + names[n-1]
}
