package render

import (
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
)

// CostChart is the period cut into steps, bars of spend stacked by one
// split. Heights and positions are percent of the plot.
type CostChart struct {
	Key     string
	Title   string
	Total   string
	Columns []CostColumn
	Grid    []CostGridLine
	Labels  []CostAxisLabel
	Legend  []CostLegend
	// Top is the spend at the plot's top. The page stacks a bar again from
	// it when the legend hides a series.
	Top   float64
	Empty bool
}

// CostColumn is one step: its bar and its tooltip. Drill is the page
// narrowed to the step, empty for an hour and for a step that has not
// begun.
type CostColumn struct {
	Key        string
	Label      string
	Value      string
	Height     float64
	Current    bool
	Drill      string
	DrillLabel string
	// DrillHint stands in the tooltip for the link where a click on the
	// bar drills itself.
	DrillHint string
	Segments  []CostSegment
	Tips      []CostTipRow
}

// CostSegment is one stacked part of a bar, V its spend and Grow its share
// of the bar. The shares of a bar add up to 1, a flex grow below that
// leaves the rest of the bar empty.
type CostSegment struct {
	Series string
	CostColor
	V    float64
	Grow float64
}

// CostTipRow is one series of a step in its tooltip.
type CostTipRow struct {
	Series string
	CostColor
	Label string
	Value string
}

// CostColor is a series color: a palette class, or the generated class
// with its hue.
type CostColor struct {
	Color string
	Hue   string
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

// CostLegend names one series.
type CostLegend struct {
	Key   string
	Label string
	CostColor
	Value string
}

// costPalette is the fixed order of the first series colors as classes of
// costs.css, checked for color blind separation in both themes. The status
// colors (green, red) are left out so a series never reads as a state.
// Further series step the hue by the golden angle, so no two in a chart
// share a color.
var costPalette = []string{
	"dc-cost-c1", "dc-cost-c2", "dc-cost-c3", "dc-cost-c4", "dc-cost-c5", "dc-cost-c6", "dc-cost-c7",
}

func costColor(i int) CostColor {
	if i < len(costPalette) {
		return CostColor{Color: costPalette[i]}
	}
	hue := math.Mod(float64(i-len(costPalette))*137.508, 360)
	return CostColor{Color: "dc-cost-gen", Hue: strconv.FormatFloat(hue, 'f', 1, 64)}
}

// costSeries is one series of a chart. A series with a color of its own
// keeps it.
type costSeries struct {
	key   string
	label string
	color CostColor
}

func costProject(p cost.Share) costSeries {
	if p.Assistants {
		return costSeries{key: "assistants", label: "Assistants", color: CostColor{Color: "dc-cost-assistants"}}
	}
	return costSeries{key: "p:" + p.Project, label: projectLabel(p.Project)}
}

func costModel(p cost.Share) costSeries {
	return costSeries{key: "m:" + p.Model, label: modelLabel(p.Model)}
}

func costSession(prefix string) func(cost.Share) costSeries {
	return func(p cost.Share) costSeries {
		return costSeries{key: prefix + p.Coder + "/" + p.Session + "/" + p.Assistant, label: sessionLabel(p)}
	}
}

// costSeriesSet names every series booked in the buckets, a model without a
// list price at $0 included, and colors them by name, not by spend: two
// that swap places keep their colors.
func costSeriesSet(all []cost.Bucket, of func(cost.Share) costSeries) map[string]costSeries {
	set := map[string]costSeries{}
	for _, b := range all {
		for _, p := range b.Parts {
			sr := of(p)
			set[sr.key] = sr
		}
	}
	i := 0
	for _, sr := range costByName(set) {
		if sr.color.Color == "" {
			sr.color = costColor(i)
			set[sr.key] = sr
			i++
		}
	}
	return set
}

func costByName(set map[string]costSeries) []costSeries {
	out := make([]costSeries, 0, len(set))
	for _, sr := range set {
		out = append(out, sr)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].label != out[j].label {
			return out[i].label < out[j].label
		}
		return out[i].key < out[j].key
	})
	return out
}

// costBySpend orders the series biggest first over the whole period, the
// order of the legend and of every bar from the bottom up, so the biggest
// stands on the baseline in each bar alike.
func costBySpend(set map[string]costSeries, totals map[string]float64) []costSeries {
	out := costByName(set)
	sort.SliceStable(out, func(i, j int) bool { return totals[out[i].key] > totals[out[j].key] })
	return out
}

func newCostChart(key, by string, buckets []cost.Bucket, s CostState, now time.Time, of func(cost.Share) costSeries) CostChart {
	from, to := s.Bounds(now)
	step := costStep(from, to, now)
	c := CostChart{Key: key, Title: costChartTitle(step) + " " + by}
	set := costSeriesSet(buckets, of)
	totals := map[string]float64{}
	for _, b := range buckets {
		for _, p := range b.Parts {
			if p.USD > 0 {
				totals[of(p).key] += p.USD
			}
		}
	}
	order := costBySpend(set, totals)
	peak, total := 0.0, 0.0
	n := len(buckets)
	for _, b := range buckets {
		col := CostColumn{
			Key:     key + ":" + strconv.FormatInt(b.Start.Unix(), 10),
			Label:   costStepLabel(step, b.Start, b.End, now),
			Current: !now.Before(b.Start) && now.Before(b.End),
		}
		parts := map[string]float64{}
		for _, p := range b.Parts {
			if p.USD > 0 {
				parts[of(p).key] += p.USD
			}
		}
		col.Value = Money(b.USD)
		var stacked []costSeries
		for _, sr := range order {
			if _, ok := parts[sr.key]; ok {
				stacked = append(stacked, sr)
				col.Segments = append(col.Segments, CostSegment{Series: sr.key, CostColor: sr.color, V: parts[sr.key]})
			}
		}
		for i := range col.Segments {
			col.Segments[i].Grow = fraction(col.Segments[i].V, col.sum())
		}
		for _, sr := range costByValue(stacked, parts) {
			col.Tips = append(col.Tips, CostTipRow{Series: sr.key, CostColor: sr.color, Label: sr.label, Value: Money(parts[sr.key])})
		}
		if inner := costStep(b.Start, b.End, now); inner < step && !b.Start.After(now) {
			col.Drill = s.custom(b.Start, b.End, now).URL() + "#cost-" + key
			col.DrillLabel, col.DrillHint = "Show the days of "+col.Label, "Click the bar to show the days"
			if inner == cost.StepHour {
				col.DrillLabel, col.DrillHint = "Show the hours of "+col.Label, "Click the bar to show the hours"
			}
		}
		peak = math.Max(peak, b.USD)
		total += b.USD
		c.Columns = append(c.Columns, col)
	}
	c.Empty = peak <= 0
	c.Total = Money(total)
	top, grid := costScale(peak)
	c.Top = top
	for _, g := range grid {
		c.Grid = append(c.Grid, CostGridLine{Bottom: g / top * 100, Label: Money(g)})
	}
	for i := range c.Columns {
		c.Columns[i].Height = math.Round(fraction(c.Columns[i].sum(), top)*10000) / 100
	}
	stride := 1
	if n > 8 {
		stride = (n + 5) / 6
	}
	for i := 0; i < n; i += stride {
		label := CostAxisLabel{Left: (float64(i) + 0.5) / float64(n) * 100, Label: costAxisLabel(step, buckets[i].Start, n, now), Minor: len(c.Labels)%2 == 1}
		switch {
		case i == 0:
			label.Anchor = "start"
		case i == n-1:
			label.Anchor = "end"
		}
		c.Labels = append(c.Labels, label)
	}
	for _, sr := range order {
		c.Legend = append(c.Legend, CostLegend{Key: sr.key, Label: sr.label, CostColor: sr.color, Value: Money(totals[sr.key])})
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

func costChartTitle(step cost.Step) string {
	switch step {
	case cost.StepHour:
		return "Spend per hour"
	case cost.StepWeek:
		return "Spend per week"
	case cost.StepMonth:
		return "Spend per month"
	}
	return "Spend per day"
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
	if !(step > 0) {
		// A peak so close to zero that its step underflows would scale
		// the axis to NaN, it draws like no spend.
		return costScale(0)
	}
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
