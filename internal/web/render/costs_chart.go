package render

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
)

// CostChart is the period cut into steps: bars stacked by the chosen
// dimension in the page's unit, and the running total under them on a
// scale of its own. Heights and positions are percent of the plot.
type CostChart struct {
	Title   string
	Dims    []CostLink
	Units   []CostLink
	Columns []CostColumn
	Grid    []CostGridLine
	Labels  []CostAxisLabel
	Legend  []CostLegend
	// Top is the value of the plot's top in the page's unit. The page
	// stacks a bar again from it when the legend hides a series.
	Top     float64
	CumTop  string
	CumLine string
	CumArea string
	Empty   bool
}

// CostColumn is one step: its bar, its point on the running total and its
// tooltip. Drill is the page narrowed to the step, empty for an hour and
// for a step that has not begun.
type CostColumn struct {
	Key        string
	Label      string
	Value      string
	Height     float64
	Current    bool
	Future     bool
	Drill      string
	DrillLabel string
	Cum        float64
	CumValue   string
	Segments   []CostSegment
	Tips       []CostTipRow
}

// CostSegment is one stacked part of a bar, V its value in the page's unit.
type CostSegment struct {
	Series string
	Color  string
	V      float64
}

// CostTipRow is one series of a step in its tooltip.
type CostTipRow struct {
	Series string
	Color  string
	Label  string
	Value  string
}

// CostGridLine is a value line, Bottom in percent of the plot.
type CostGridLine struct {
	Bottom float64
	Label  string
}

// CostAxisLabel names a step under the plot, Left in percent of the width.
// Anchor holds a label at the plot's edge inside it: "start", "end" or
// centered under its step. Minor labels give way on a narrow plot.
type CostAxisLabel struct {
	Left   float64
	Label  string
	Anchor string
	Minor  bool
}

// CostLegend names one series. Color is always a class.
type CostLegend struct {
	Key   string
	Label string
	Color string
	Value string
}

// costPalette is the fixed order of the series colors as classes of
// style.css, checked for color blind separation in both themes. The status
// colors (green, red) are left out so a series never reads as a state.
var costPalette = []string{
	"dc-cost-c1", "dc-cost-c2", "dc-cost-c3", "dc-cost-c4", "dc-cost-c5", "dc-cost-c6", "dc-cost-c7",
}

const (
	costOtherColor      = "dc-cost-other"
	costAssistantsColor = "dc-cost-assistants"
	costOtherKey        = "other"
)

// costKinds is the stack order of the kinds and their colors, the ones
// next to each other apart for color blind eyes too.
var costKinds = []struct {
	Kind  cost.Kind
	Color string
}{
	{cost.KindCoder, "dc-cost-c1"},
	{cost.KindAssistant, costAssistantsColor},
	{cost.KindOther, costOtherColor},
}

type costSeries struct {
	key   string
	label string
	color string
	rank  int
	name  string
}

// costSeriesSet names the series of a dimension. The named ones get the
// palette by name, not by rank: two that swap places keep their colors.
type costSeriesSet struct {
	by    cost.Dimension
	named map[string]costSeries
	other costSeries
}

func newCostSeriesSet(by cost.Dimension, all []cost.Bucket) costSeriesSet {
	set := costSeriesSet{by: by, named: map[string]costSeries{}}
	set.other = costSeries{key: costOtherKey, label: "Other projects", color: costOtherColor, rank: 2}
	if by == cost.ByModel {
		set.other.label = "Other models"
	}
	if by == cost.ByKind {
		for i, k := range costKinds {
			label, _ := CostKind(k.Kind)
			set.named[costKey(by, cost.Share{Kind: k.Kind})] = costSeries{key: costKey(by, cost.Share{Kind: k.Kind}), label: label, color: k.Color, rank: i}
		}
		return set
	}
	sums := map[cost.Share]float64{}
	for _, b := range all {
		for _, p := range b.Parts {
			sums[costIdentity(p)] += p.USD
		}
	}
	var names []cost.Share
	for p := range sums {
		if p.Assistants {
			key := costKey(by, p)
			set.named[key] = costSeries{key: key, label: "Assistants", color: costAssistantsColor, rank: 1}
			continue
		}
		names = append(names, p)
	}
	sort.Slice(names, func(i, j int) bool {
		if sums[names[i]] != sums[names[j]] {
			return sums[names[i]] > sums[names[j]]
		}
		return costName(names[i]) < costName(names[j])
	})
	if len(names) > len(costPalette) {
		names = names[:len(costPalette)]
	}
	sort.Slice(names, func(i, j int) bool { return costName(names[i]) < costName(names[j]) })
	for i, p := range names {
		key := costKey(by, p)
		label := projectLabel(p.Project)
		if by == cost.ByModel {
			label = modelLabel(p.Model)
		}
		set.named[key] = costSeries{key: key, label: label, color: costPalette[i], name: costName(p)}
	}
	return set
}

func (set costSeriesSet) of(p cost.Share) costSeries {
	if s, ok := set.named[costKey(set.by, p)]; ok {
		return s
	}
	return set.other
}

// byKey is the series a key names, the folded one for the other key.
func (set costSeriesSet) byKey(key string) costSeries {
	if key == costOtherKey {
		return set.other
	}
	return set.named[key]
}

// stacked orders series bottom up: the named ones by name or by their
// fixed place, the assistants above them and the folded ones on top.
func (set costSeriesSet) stacked(keys map[string]float64) []costSeries {
	out := make([]costSeries, 0, len(keys))
	for key := range keys {
		out = append(out, set.byKey(key))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		return out[i].name < out[j].name
	})
	return out
}

func costIdentity(p cost.Share) cost.Share {
	return cost.Share{Kind: p.Kind, Project: p.Project, Assistants: p.Assistants, Model: p.Model}
}

func costName(p cost.Share) string {
	return p.Project + p.Model
}

func costKey(by cost.Dimension, p cost.Share) string {
	switch {
	case by == cost.ByKind:
		return "k:" + string(p.Kind)
	case by == cost.ByModel:
		return "m:" + p.Model
	case p.Assistants:
		return "assistants"
	}
	return "p:" + p.Project
}

func newCostChart(r, all cost.Report, s CostState) CostChart {
	now := r.Now
	from, to := s.Bounds(now)
	step := costStep(from, to)
	unit := func(usd float64, t cost.Tokens) float64 {
		if s.Tokens {
			return float64(t.Total())
		}
		return usd
	}
	format := func(v float64) string {
		if s.Tokens {
			return tokenValue(int64(math.Round(v)))
		}
		return Money(v)
	}
	c := CostChart{Title: costChartTitle(step, s.Tokens)}
	for _, d := range []struct {
		by    cost.Dimension
		label string
	}{{cost.ByProject, "Project"}, {cost.ByKind, "Kind"}, {cost.ByModel, "Model"}} {
		c.Dims = append(c.Dims, CostLink{Label: d.label, Active: s.By == d.by, URL: s.with(func(c *CostState) { c.By = d.by }).URL()})
	}
	c.Units = []CostLink{
		{Label: "USD", Active: !s.Tokens, URL: s.with(func(c *CostState) { c.Tokens = false }).URL()},
		{Label: "Tokens", Active: s.Tokens, URL: s.with(func(c *CostState) { c.Tokens = true }).URL()},
	}
	set := newCostSeriesSet(s.By, all.Buckets)
	totals := map[string]float64{}
	peak, running := 0.0, 0.0
	n := len(r.Buckets)
	cums := make([]float64, n)
	for i, b := range r.Buckets {
		col := CostColumn{
			Key:     strconv.FormatInt(b.Start.Unix(), 10),
			Label:   costStepLabel(step, b.Start, b.End, now),
			Current: !now.Before(b.Start) && now.Before(b.End),
			Future:  b.Start.After(now),
		}
		parts := map[string]float64{}
		for _, p := range b.Parts {
			v := unit(p.USD, p.Tokens)
			if v > 0 {
				parts[set.of(p).key] += v
			}
		}
		total := unit(b.USD, b.Tokens)
		col.Value = format(total)
		for _, sr := range set.stacked(parts) {
			col.Segments = append(col.Segments, CostSegment{Series: sr.key, Color: sr.color, V: parts[sr.key]})
			totals[sr.key] += parts[sr.key]
		}
		for _, sr := range costByValue(set.stacked(parts), parts) {
			col.Tips = append(col.Tips, CostTipRow{Series: sr.key, Color: sr.color, Label: sr.label, Value: format(parts[sr.key])})
		}
		running += total
		cums[i] = running
		col.CumValue = format(running)
		if step != cost.StepHour && !col.Future {
			col.Drill = s.custom(b.Start, b.End, now).URL()
			col.DrillLabel = "Show the days of " + col.Label
			if step == cost.StepDay {
				col.DrillLabel = "Show the hours of " + col.Label
			}
		}
		peak = math.Max(peak, total)
		c.Columns = append(c.Columns, col)
	}
	c.Empty = peak <= 0
	top, grid := costScale(peak)
	c.Top = top
	for _, g := range grid {
		c.Grid = append(c.Grid, CostGridLine{Bottom: g / top * 100, Label: costAxisValue(g, s.Tokens)})
	}
	for i := range c.Columns {
		c.Columns[i].Height = math.Round(fraction(c.Columns[i].sum(), top)*10000) / 100
	}
	cumTop, _ := costScale(running)
	c.CumTop = costAxisValue(cumTop, s.Tokens)
	points := []string{"0,100"}
	for i, col := range c.Columns {
		if col.Future {
			break
		}
		c.Columns[i].Cum = math.Round(fraction(cums[i], cumTop)*10000) / 100
		points = append(points, costPoint((float64(i)+0.5)/float64(n)*100, 100-c.Columns[i].Cum))
	}
	if len(points) > 1 && running > 0 {
		c.CumLine = strings.Join(points, " ")
		last := strings.Split(points[len(points)-1], ",")[0]
		c.CumArea = c.CumLine + " " + last + ",100"
	}
	stride := 1
	if n > 8 {
		stride = (n + 5) / 6
	}
	for i := 0; i < n; i += stride {
		label := CostAxisLabel{Left: (float64(i) + 0.5) / float64(n) * 100, Label: costAxisLabel(step, r.Buckets[i].Start, n, now), Minor: len(c.Labels)%2 == 1}
		switch {
		case i == 0:
			label.Anchor = "start"
		case i == n-1:
			label.Anchor = "end"
		}
		c.Labels = append(c.Labels, label)
	}
	// The legend leads with the biggest series, the folded ones last.
	legend := make([]costSeries, 0, len(totals))
	for key := range totals {
		legend = append(legend, set.byKey(key))
	}
	sort.SliceStable(legend, func(i, j int) bool {
		if (legend[i].key == costOtherKey) != (legend[j].key == costOtherKey) {
			return legend[j].key == costOtherKey
		}
		return totals[legend[i].key] > totals[legend[j].key]
	})
	for _, sr := range legend {
		c.Legend = append(c.Legend, CostLegend{Key: sr.key, Label: sr.label, Color: sr.color, Value: format(totals[sr.key])})
	}
	return c
}

func (c CostColumn) sum() float64 {
	sum := 0.0
	for _, s := range c.Segments {
		sum += s.V
	}
	return sum
}

func costByValue(series []costSeries, values map[string]float64) []costSeries {
	out := append([]costSeries(nil), series...)
	sort.SliceStable(out, func(i, j int) bool { return values[out[i].key] > values[out[j].key] })
	return out
}

func costPoint(x, y float64) string {
	return strconv.FormatFloat(x, 'f', 2, 64) + "," + strconv.FormatFloat(y, 'f', 2, 64)
}

func costChartTitle(step cost.Step, tokens bool) string {
	what := "Spend"
	if tokens {
		what = "Tokens"
	}
	switch step {
	case cost.StepHour:
		return what + " per hour"
	case cost.StepWeek:
		return what + " per week"
	case cost.StepMonth:
		return what + " per month"
	}
	return what + " per day"
}

func costStepLabel(step cost.Step, start, end, now time.Time) string {
	switch step {
	case cost.StepHour:
		return start.Format("Mon 2 Jan, 15:04") + " to " + end.Format("15:04")
	case cost.StepDay:
		return start.Format("Mon 2 Jan")
	}
	return costPeriod(start, end, now)
}

func costAxisLabel(step cost.Step, start time.Time, n int, now time.Time) string {
	switch {
	case step == cost.StepHour:
		return start.Format("15:04")
	case step == cost.StepDay && n <= 8:
		return start.Format("Mon 2")
	case step == cost.StepMonth && start.Year() != now.Year():
		return start.Format("Jan 2006")
	case step == cost.StepMonth:
		return start.Format("Jan")
	}
	return start.Format("2 Jan")
}

func costAxisValue(v float64, tokens bool) string {
	if tokens {
		return tokenShort(int64(math.Round(v)))
	}
	return Money(v)
}

// costGridLines is how many value lines an axis draws at most.
const costGridLines = 3

// costScale picks a round top for an axis and up to three grid lines under
// it. The top stays below 5/3 of the peak, so a peak held to half the
// largest float keeps it finite; a bar above it is drawn full.
func costScale(peak float64) (float64, []float64) {
	if !(peak > 0) {
		peak = 1
	}
	peak = math.Min(peak, math.MaxFloat64/2)
	step := niceStep(peak / costGridLines)
	top := math.Ceil(peak/step) * step
	grid := make([]float64, 0, costGridLines)
	for i := 1; i <= costGridLines; i++ {
		v := float64(i) * step
		if v > top+step/2 {
			break
		}
		grid = append(grid, v)
	}
	return top, grid
}

func niceStep(raw float64) float64 {
	exp := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if raw <= m*exp {
			return m * exp
		}
	}
	return 10 * exp
}
