package render

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
	"github.com/marein/dev-cockpit/internal/cost/price"
)

// CostStatus is the status line's cost item: today.
type CostStatus struct {
	Today string
}

func NewCostStatus(r cost.Report) CostStatus {
	return CostStatus{Today: Money(r.Today)}
}

// CostsData feeds the cost page.
type CostsData struct {
	Page
	Board CostBoard
}

// CostBoard is everything under the page head, the part a live update and
// a move to another state of the page swap.
type CostBoard struct {
	Booked     bool
	Toolbar    CostToolbar
	Tiles      []CostTile
	Charts     []CostChart
	RangeLabel string
	Unpriced   int
	// TopUp is the part of the range total that claude's own running totals
	// hold above its logged calls, empty when there is none.
	TopUp string
	// Prices says which list prices the spend was priced with.
	Prices string
}

// CostToolbar picks the period.
type CostToolbar struct {
	Label   string
	Dates   string
	Presets []CostLink
	Prev    string
	Next    string
	// Custom is the period as the date inputs hold it, Today their upper
	// bound.
	CustomFrom string
	CustomTo   string
	Today      string
}

// CostLink is one choice of a switch.
type CostLink struct {
	Label  string
	URL    string
	Active bool
}

// CostTile is one headline number.
type CostTile struct {
	Label string
	Value string
	Hint  string
}

// CostSplits are the period's steps split by coder session, by assistant
// and by model. The report's own buckets are split by project.
type CostSplits struct {
	Coders     []cost.Bucket
	Assistants []cost.Bucket
	Models     []cost.Bucket
}

// NewCostBoard lays a report out for the page in its state.
func NewCostBoard(r cost.Report, splits CostSplits, s CostState, prices string) CostBoard {
	now := r.Now
	from, to := s.Bounds(now)
	b := CostBoard{
		Booked:     r.Booked,
		Unpriced:   r.Unpriced,
		RangeLabel: s.rangeLabel(now),
		Prices:     prices,
	}
	if r.TopUp >= 0.005 {
		b.TopUp = Money(r.TopUp)
	}
	b.Toolbar = newCostToolbar(s, from, to, now)
	b.Tiles = []CostTile{
		{Label: "Today", Value: Money(r.Today), Hint: "since midnight"},
		{Label: "This month", Value: Money(r.Month), Hint: "since the 1st"},
	}
	b.Charts = []CostChart{
		newCostChart("project", "by project", r.Buckets, s, now, costProject),
		newCostChart("assistant", "by assistant", splits.Assistants, s, now, costSession("a:")),
		newCostChart("coder", "by coder", splits.Coders, s, now, costSession("c:")),
		newCostChart("model", "by model", splits.Models, s, now, costModel),
	}
	return b
}

func newCostToolbar(s CostState, from, to, now time.Time) CostToolbar {
	t := CostToolbar{
		Label:      s.rangeLabel(now),
		Dates:      costPeriod(from, to, now),
		CustomFrom: from.Format(costDate),
		CustomTo:   cost.AddDays(to, -1).Format(costDate),
		Today:      now.Format(costDate),
	}
	if t.CustomTo > t.Today {
		t.CustomTo = t.Today
	}
	for _, p := range costPresets {
		link := s.with(func(c *CostState) { c.Range, c.From, c.To = p.Key, "", "" })
		t.Presets = append(t.Presets, CostLink{Label: p.Label, URL: link.URL(), Active: p.Key == s.Range})
	}
	if prev, ok := s.shift(-1, now); ok {
		t.Prev = prev.URL()
	}
	if next, ok := s.shift(1, now); ok {
		t.Next = next.URL()
	}
	return t
}

func projectLabel(name string) string {
	if name == "" {
		return "No project"
	}
	return name
}

func modelLabel(name string) string {
	if name == "" {
		return "Unnamed model"
	}
	return name
}

func sessionLabel(s cost.Share) string {
	if s.Name != "" {
		return s.Name
	}
	if id := s.Session + s.Assistant; id != "" {
		return "Unnamed " + shortID(id)
	}
	return "Unnamed"
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// fraction is v's part of top from 0 to 1. It holds where a sum ran past
// the largest float: a part that reaches the top is all of it.
func fraction(v, top float64) float64 {
	switch {
	case !(top > 0) || !(v > 0):
		return 0
	case v >= top:
		return 1
	}
	return v / top
}

// CostPrices words where the list prices come from: the table built into
// this version, or the day it was last refreshed, and whether it still is.
func CostPrices(s price.Status, loc *time.Location) string {
	from := "List prices built into this version"
	if !s.FetchedAt.IsZero() {
		from = "List prices refreshed " + s.FetchedAt.In(loc).Format("2 Jan 15:04")
	}
	if !s.Enabled {
		return from + ", refresh is off."
	}
	return from + "."
}

// Money writes an amount in dollars: cents below a thousand, whole dollars
// with separators above, and a spend too small for a cent is not shown as
// nothing.
func Money(v float64) string {
	switch {
	case v <= 0:
		return "$0.00"
	case v < 0.005:
		return "<$0.01"
	case v < 1000:
		return "$" + strconv.FormatFloat(v, 'f', 2, 64)
	}
	digits := strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return "$" + b.String()
}
