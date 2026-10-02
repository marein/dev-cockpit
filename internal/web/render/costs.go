package render

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
	"github.com/marein/dev-cockpit/internal/cost/price"
)

// CostNote is the one sentence every cost surface carries.
const CostNote = "API list price equivalent. A subscription does not pay it."

// CostStatus is the status line's cost item: today and the last hour.
type CostStatus struct {
	Show  bool
	Today string
	Burn  string
}

// NewCostStatus reads the item off a report. It shows once anything is
// booked at all, so a fresh install's status line stays quiet.
func NewCostStatus(r cost.Report) CostStatus {
	return CostStatus{Show: r.Booked, Today: Money(r.Today), Burn: Money(r.Burn)}
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
	Chips      []CostChip
	ClearURL   string
	Tiles      []CostTile
	Chart      CostChart
	RangeLabel string
	RangeTotal string
	Breakdowns []CostBreakdown
	Unpriced   int
	// TopUp is the part of the range total that claude's own running totals
	// hold above its logged calls, empty when there is none.
	TopUp string
	// Prices says which list prices the spend was priced with.
	Prices string
}

// CostToolbar picks the period and narrows the names.
type CostToolbar struct {
	Label   string
	Dates   string
	Presets []CostLink
	Prev    string
	Next    string
	// Custom is the period as the date inputs hold it, Today their upper
	// bound, Carry the rest of the state the custom form sends along.
	CustomFrom string
	CustomTo   string
	Today      string
	Carry      []CostField
	Query      string
}

// CostLink is one choice of a switch.
type CostLink struct {
	Label  string
	URL    string
	Active bool
}

// CostField is one hidden field of a form.
type CostField struct{ Name, Value string }

// CostChip is one active filter, Remove the page without it.
type CostChip struct {
	Label  string
	Value  string
	Remove string
}

// CostTile is one headline number, with the bars of its period.
type CostTile struct {
	Label string
	Value string
	Hint  string
	Spark []CostSparkBar
}

// CostSparkBar is one bar of a tile, Height in percent of the tile's peak.
type CostSparkBar struct {
	Height  float64
	Current bool
}

// CostBreakdown is one card of shares.
type CostBreakdown struct {
	Key   string
	Title string
	Rows  []CostShareRow
}

// CostShareRow is one row of a breakdown. Value is in the page's unit, Alt
// in the other one, Percent the share of the period in the page's unit.
// Drill is the page narrowed to the row, empty where it already is.
type CostShareRow struct {
	Label      string
	Sub        string
	Badge      string
	BadgeClass string
	Value      string
	Alt        string
	Percent    float64
	USD        float64
	Tokens     int64
	Drill      string
	Children   []CostShareRow
}

// NewCostBoard lays a report out for the page in its state. all is the
// same span without the filter: the series keep their colors when a filter
// takes some of them away.
func NewCostBoard(r, all cost.Report, s CostState, prices string) CostBoard {
	now := r.Now
	from, to := s.Bounds(now)
	tokens := tokenSum(r.Kinds)
	b := CostBoard{
		Booked:     r.Booked,
		Unpriced:   r.Unpriced,
		RangeLabel: s.rangeLabel(now),
		RangeTotal: Money(r.Total),
		Prices:     prices,
		ClearURL:   s.with(func(c *CostState) { c.Filter = cost.Filter{} }).URL(),
	}
	if s.Tokens {
		b.RangeTotal = tokenValue(tokens)
	}
	if r.TopUp >= 0.005 {
		b.TopUp = Money(r.TopUp)
	}
	b.Toolbar = newCostToolbar(s, from, to, now)
	b.Chips = costChips(s, r)
	b.Tiles = costTiles(r)
	b.Chart = newCostChart(r, all, s)
	rows := costRows{s: s, usd: r.Total, tokens: tokens}
	b.Breakdowns = []CostBreakdown{
		{Key: "projects", Title: "By project", Rows: rows.projects(r.Projects, r.Assistants)},
		{Key: "kinds", Title: "By kind", Rows: rows.kinds(r.Kinds)},
		{Key: "sessions", Title: "By coder and assistant", Rows: rows.sessions(r.Sessions)},
		{Key: "models", Title: "By model", Rows: rows.models(r.Models)},
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
		Query:      s.Query,
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
	for key, values := range s.Values() {
		if key != "range" && key != "from" && key != "to" {
			t.Carry = append(t.Carry, CostField{Name: key, Value: values[0]})
		}
	}
	sort.Slice(t.Carry, func(i, j int) bool { return t.Carry[i].Name < t.Carry[j].Name })
	return t
}

func costChips(s CostState, r cost.Report) []CostChip {
	var chips []CostChip
	add := func(label, value string, clear func(*cost.Filter)) {
		chips = append(chips, CostChip{Label: label, Value: value, Remove: s.with(func(c *CostState) { clear(&c.Filter) }).URL()})
	}
	f := s.Filter
	if f.Project != nil {
		add("Project", projectLabel(*f.Project), func(f *cost.Filter) { f.Project = nil })
	}
	if f.Assistants {
		add("Group", "Assistants", func(f *cost.Filter) { f.Assistants = false })
	}
	if f.Assistant != "" {
		name := ""
		for _, a := range r.Assistants {
			if a.Assistant == f.Assistant {
				name = a.Name
			}
		}
		add("Assistant", assistantLabel(name), func(f *cost.Filter) { f.Assistant = "" })
	}
	if f.Kind != "" {
		label, _ := CostKind(f.Kind)
		add("Kind", label, func(f *cost.Filter) { f.Kind = "" })
	}
	if f.Model != nil {
		add("Model", modelLabel(*f.Model), func(f *cost.Filter) { f.Model = nil })
	}
	if f.Coder != "" {
		add("Coder", f.Coder, func(f *cost.Filter) { f.Coder = "" })
	}
	if f.Session != "" {
		name := "Unnamed " + shortID(f.Session)
		for _, sh := range r.Sessions {
			if sh.Session == f.Session {
				name = sessionLabel(sh)
				break
			}
		}
		add("Session", name, func(f *cost.Filter) { f.Session = "" })
	}
	return chips
}

func costTiles(r cost.Report) []CostTile {
	week, month := cost.WeekStart(r.Now), cost.MonthStart(r.Now)
	days := func(since time.Time) []float64 {
		var out []float64
		for _, d := range r.Days {
			if !d.Start.Before(since) {
				out = append(out, d.Total)
			}
		}
		return out
	}
	return []CostTile{
		{Label: "Today", Value: Money(r.Today), Hint: "since midnight", Spark: spark(r.Hours, r.Now.Hour())},
		{Label: "This week", Value: Money(r.Week), Hint: "since Monday", Spark: spark(days(week), -1)},
		{Label: "This month", Value: Money(r.Month), Hint: "since the 1st", Spark: spark(days(month), -1)},
		{Label: "Burn rate", Value: Money(r.Burn) + "/h", Hint: "the last 60 minutes"},
	}
}

// spark scales a tile's bars to their peak, current marks the bar of now,
// -1 the last one.
func spark(values []float64, current int) []CostSparkBar {
	peak := 0.0
	for _, v := range values {
		peak = math.Max(peak, v)
	}
	if current < 0 {
		current = len(values) - 1
	}
	out := make([]CostSparkBar, len(values))
	for i, v := range values {
		out[i].Current = i == current
		out[i].Height = math.Round(fraction(v, peak)*1000) / 10
	}
	return out
}

// costRows builds the breakdowns, shares in the page's unit.
type costRows struct {
	s      CostState
	usd    float64
	tokens int64
}

func (c costRows) row(sh cost.Share, label string, narrow func(*cost.Filter)) CostShareRow {
	row := CostShareRow{Label: label, USD: sh.USD, Tokens: sh.Tokens.Total()}
	if c.s.Tokens {
		row.Value, row.Alt, row.Percent = tokenValue(row.Tokens), Money(sh.USD), percent(float64(row.Tokens), float64(c.tokens))
	} else {
		row.Value, row.Alt, row.Percent = Money(sh.USD), TokenCount(row.Tokens), percent(sh.USD, c.usd)
	}
	drill := c.s.with(func(s *CostState) { narrow(&s.Filter) })
	if drill.URL() != c.s.URL() {
		row.Drill = drill.URL()
	}
	return row
}

func (c costRows) projects(projects, assistants []cost.Share) []CostShareRow {
	out := make([]CostShareRow, 0, len(projects))
	for _, p := range projects {
		if !p.Assistants {
			name := p.Project
			out = append(out, c.row(p, projectLabel(name), func(f *cost.Filter) {
				f.Project, f.Assistants, f.Assistant = &name, false, ""
			}))
			continue
		}
		row := c.row(p, "Assistants", func(f *cost.Filter) { f.Project, f.Assistants = nil, true })
		for _, a := range assistants {
			id := a.Assistant
			row.Children = append(row.Children, c.row(a, assistantLabel(a.Name), func(f *cost.Filter) {
				f.Project, f.Assistant = nil, id
			}))
		}
		out = append(out, row)
	}
	return out
}

func (c costRows) kinds(kinds []cost.Share) []CostShareRow {
	out := make([]CostShareRow, 0, len(kinds))
	for _, k := range kinds {
		kind := k.Kind
		label, _ := CostKind(kind)
		out = append(out, c.row(k, label, func(f *cost.Filter) { f.Kind = kind }))
	}
	return out
}

func (c costRows) sessions(sessions []cost.Share) []CostShareRow {
	out := make([]CostShareRow, 0, len(sessions))
	for _, sh := range sessions {
		row := c.row(sh, sessionLabel(sh), func(f *cost.Filter) {
			if sh.Session != "" {
				f.Coder, f.Session = sh.Coder, sh.Session
				return
			}
			f.Kind, f.Project, f.Assistant = sh.Kind, nil, sh.Assistant
		})
		row.Badge, row.BadgeClass = CostKind(sh.Kind)
		row.Sub = projectLabel(sh.Project)
		if sh.Kind == cost.KindAssistant {
			row.Sub = ""
		}
		out = append(out, row)
	}
	return out
}

func (c costRows) models(models []cost.Share) []CostShareRow {
	out := make([]CostShareRow, 0, len(models))
	for _, m := range models {
		model := m.Model
		out = append(out, c.row(m, modelLabel(model), func(f *cost.Filter) { f.Model = &model }))
	}
	return out
}

func tokenSum(shares []cost.Share) int64 {
	var n int64
	for _, s := range shares {
		n += s.Tokens.Total()
	}
	return n
}

// CostKind is the label and the badge class of a kind. The badge wears the
// kind's color in the chart.
func CostKind(k cost.Kind) (string, string) {
	switch k {
	case cost.KindCoder:
		return "Coder", "bg-blue-lt"
	case cost.KindAssistant:
		return "Assistant", "bg-purple-lt"
	}
	return "Other", "bg-secondary-lt"
}

func projectLabel(name string) string {
	if name == "" {
		return "No project"
	}
	return name
}

func assistantLabel(name string) string {
	if name == "" {
		return "Unnamed assistant"
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
	if s.Session != "" {
		return "Unnamed " + shortID(s.Session)
	}
	return "Unnamed"
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func percent(v, total float64) float64 {
	return math.Round(fraction(v, total)*1000) / 10
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

// TokenCount writes a token count short, empty for none.
func TokenCount(n int64) string {
	if n <= 0 {
		return ""
	}
	return tokenShort(n) + " tokens"
}

// tokenValue is a token count where a value has to stand, none included.
func tokenValue(n int64) string {
	if n <= 0 {
		return "0 tokens"
	}
	return TokenCount(n)
}

func tokenShort(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return strconv.FormatFloat(float64(n)/1e3, 'f', 1, 64) + "K"
	case n < 1_000_000_000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	}
	return strconv.FormatFloat(float64(n)/1e9, 'f', 1, 64) + "B"
}

// CostPrices words where the list prices come from: the table built into
// this version, or the day it was last refreshed, and whether it still is.
func CostPrices(s price.Status, loc *time.Location) string {
	from := "List prices built into this version"
	if !s.FetchedAt.IsZero() {
		from = "List prices refreshed " + s.FetchedAt.In(loc).Format("2 Jan 15:04")
	}
	if !s.Enabled {
		return from + ", the daily refresh is off."
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
