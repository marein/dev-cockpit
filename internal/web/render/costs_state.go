package render

import (
	"math"
	"net/url"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
)

// CostState is the period the cost page shows, as its URL carries it.
type CostState struct {
	Range string
	// From and To are the first and the last day of a custom range.
	From string
	To   string
}

const (
	costCustom = "custom"
	costDate   = "2006-01-02"
)

type costPreset struct{ Key, Label string }

var costPresets = []costPreset{
	{cost.RangeToday, "Today"},
	{cost.RangeYesterday, "Yesterday"},
	{cost.RangeWeek, "This week"},
	{cost.RangeLastWeek, "Last week"},
	{cost.RangeMonth, "This month"},
	{cost.RangeLastMonth, "Last month"},
	{cost.Range7d, "Last 7 days"},
	{cost.Range30d, "Last 30 days"},
	{cost.Range90d, "Last 90 days"},
}

// normalizeCostRange keeps a range name the page knows, thirty days otherwise.
func normalizeCostRange(name string) string {
	for _, p := range costPresets {
		if p.Key == name {
			return name
		}
	}
	return cost.Range30d
}

// ParseCostState reads the page's state from its query. Anything it does
// not know falls back to the default, a custom range ends today at the
// latest and starts before it ends.
func ParseCostState(q url.Values, now time.Time) CostState {
	s := CostState{Range: normalizeCostRange(q.Get("range"))}
	if q.Get("range") == costCustom {
		from, okFrom := costDay(q.Get("from"), now)
		to, okTo := costDay(q.Get("to"), now)
		if okFrom && okTo {
			today := cost.DayStart(now)
			if to.After(today) {
				to = today
			}
			if from.After(today) {
				from = today
			}
			// The books keep nothing older than the longest retention, a
			// span reaching further only draws thousands of empty bars.
			first := cost.AddMonths(now, 1-cost.MaxRetention)
			if from.Before(first) {
				from = first
			}
			if to.Before(first) {
				to = first
			}
			if from.After(to) {
				from, to = to, from
			}
			s.Range, s.From, s.To = costCustom, from.Format(costDate), to.Format(costDate)
		}
	}
	return s
}

func costDay(value string, now time.Time) (time.Time, bool) {
	d, err := time.Parse(costDate, value)
	if err != nil {
		return time.Time{}, false
	}
	return cost.DayStart(time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, now.Location())), true
}

// Values is the state as a query, defaults left out.
func (s CostState) Values() url.Values {
	q := url.Values{}
	if s.Range != cost.Range30d {
		q.Set("range", s.Range)
	}
	if s.Range == costCustom {
		q.Set("from", s.From)
		q.Set("to", s.To)
	}
	return q
}

// URL is the page in this state.
func (s CostState) URL() string {
	if q := s.Values().Encode(); q != "" {
		return "/costs?" + q
	}
	return "/costs"
}

func (s CostState) with(change func(*CostState)) CostState {
	change(&s)
	return s
}

// Bounds are the days the state covers, the end exclusive.
func (s CostState) Bounds(now time.Time) (time.Time, time.Time) {
	if s.Range == costCustom {
		from, _ := costDay(s.From, now)
		to, _ := costDay(s.To, now)
		return from, cost.AddDays(to, 1)
	}
	return cost.RangeSpan(s.Range, now)
}

// Span is the query the state asks the books.
func (s CostState) Span(now time.Time) cost.Span {
	from, to := s.Bounds(now)
	return cost.Span{From: from, To: to, Step: costStep(from, to, now), By: cost.ByProject}
}

// costStep keeps a chart between a day of hours and a few dozen bars. A
// day the books folded keeps its single bar.
func costStep(from, to, now time.Time) cost.Step {
	switch days := costDays(from, to); {
	case days <= 1 && !from.Before(cost.HoursFrom(now)):
		return cost.StepHour
	case days <= 62:
		return cost.StepDay
	case days <= 186:
		return cost.StepWeek
	}
	return cost.StepMonth
}

func costDays(from, to time.Time) int {
	return int(math.Round(to.Sub(from).Hours() / 24))
}

func (s CostState) custom(from, to time.Time, now time.Time) CostState {
	return s.with(func(c *CostState) {
		c.Range, c.From, c.To = costCustom, from.Format(costDate), cost.AddDays(to, -1).Format(costDate)
		for _, p := range costPresets {
			if pf, pt := cost.RangeSpan(p.Key, now); pf.Equal(from) && pt.Equal(to) {
				c.Range, c.From, c.To = p.Key, "", ""
				return
			}
		}
	})
}

// shift is the period of the same length before (-1) or after (1) this one,
// whole months as months. A period that would start after today has none.
func (s CostState) shift(dir int, now time.Time) (CostState, bool) {
	from, to := s.Bounds(now)
	var nf, nt time.Time
	if months := costMonths(from, to); months > 0 {
		nf, nt = cost.AddMonths(from, dir*months), cost.AddMonths(to, dir*months)
	} else {
		days := costDays(from, to)
		nf, nt = cost.AddDays(from, dir*days), cost.AddDays(to, dir*days)
	}
	if nf.After(now) {
		return s, false
	}
	return s.custom(nf, nt, now), true
}

func costMonths(from, to time.Time) int {
	if !from.Equal(cost.MonthStart(from)) || !to.Equal(cost.MonthStart(to)) {
		return 0
	}
	return (to.Year()-from.Year())*12 + int(to.Month()-from.Month())
}

// costPeriod words a span of days: one day with its weekday, else the
// first and the last day, the year only where it is not this one.
func costPeriod(from, to, now time.Time) string {
	last := cost.AddDays(to, -1)
	day := func(t time.Time) string {
		if t.Year() != now.Year() {
			return t.Format("2 Jan 2006")
		}
		return t.Format("2 Jan")
	}
	if costDays(from, to) <= 1 {
		return from.Format("Mon ") + day(from)
	}
	return day(from) + " to " + day(last)
}

// rangeLabel names the state's period: the preset's name, or its days.
func (s CostState) rangeLabel(now time.Time) string {
	for _, p := range costPresets {
		if p.Key == s.Range {
			return p.Label
		}
	}
	from, to := s.Bounds(now)
	return costPeriod(from, to, now)
}
