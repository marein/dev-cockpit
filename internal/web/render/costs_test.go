package render

import (
	"math"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
)

func TestMoney(t *testing.T) {
	cases := map[float64]string{0: "$0.00", 0.001: "<$0.01", 1.234: "$1.23", 999.994: "$999.99", 1234.6: "$1,235", 1234567: "$1,234,567"}
	for v, want := range cases {
		if got := Money(v); got != want {
			t.Errorf("Money(%v) = %q, want %q", v, got, want)
		}
	}
}

// costDay builds a report of one day of hours in UTC whose buckets carry
// the given parts in its last hour.
func costDayReport(now time.Time, parts []cost.Share) cost.Report {
	from := cost.DayStart(now)
	r := cost.Report{Now: now}
	for h := 0; h < 24; h++ {
		start := from.Add(time.Duration(h) * time.Hour)
		r.Buckets = append(r.Buckets, cost.Bucket{Start: start, End: start.Add(time.Hour)})
	}
	b := &r.Buckets[now.Hour()]
	for _, p := range parts {
		b.USD += p.USD
		b.Parts = append(b.Parts, p)
	}
	return r
}

func costTestState(raw string, now time.Time) CostState {
	q, _ := url.ParseQuery(raw)
	return ParseCostState(q, now)
}

func TestCostChartGivesEveryProjectItsOwnColor(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 30, 0, 0, time.UTC)
	var parts []cost.Share
	for i := 0; i < 9; i++ {
		parts = append(parts, cost.Share{Project: string(rune('a' + i)), USD: float64(10 - i)})
	}
	r := costDayReport(now, parts)
	c := NewCostBoard(r, CostSplits{}, costTestState("range=today", now), "").Charts[0]
	if c.Title != "Spend per hour by project" || c.Total != "$54.00" || len(c.Columns) != 24 || !c.Columns[15].Current || c.Columns[14].Current {
		t.Fatalf("columns %d, title %s", len(c.Columns), c.Title)
	}
	colors := map[CostColor]bool{}
	for _, l := range c.Legend {
		colors[l.CostColor] = true
	}
	if len(c.Legend) != 9 || len(colors) != 9 || c.Legend[8].Label != "i" || c.Legend[8].Color != "dc-cost-gen" || c.Legend[8].Hue != "137.5" {
		t.Fatalf("legend = %+v", c.Legend)
	}
	col := c.Columns[15]
	if len(col.Segments) != 9 || col.Value != "$54.00" {
		t.Fatalf("the bar of now = %+v", col)
	}
	if col.Segments[0].Series != "p:a" || col.Segments[0].Color != "dc-cost-c1" || col.Tips[0].Label != "a" || col.Tips[0].Value != "$10.00" {
		t.Fatalf("the stack starts with the first name and the tooltip with the biggest part: %+v %+v", col.Segments[0], col.Tips[0])
	}
	if c.Top != 60 || col.Height != 90 || col.Drill != "" || c.Empty {
		t.Fatalf("top %v height %v drill %q", c.Top, col.Height, col.Drill)
	}
}

func TestCostChartShowsTheAssistantsAsOneSeries(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	r := costDayReport(now, []cost.Share{{Assistants: true, USD: 6}, {Project: "shop", USD: 3}, {USD: 1}})
	legend := map[string]string{}
	for _, l := range NewCostBoard(r, CostSplits{}, costTestState("", now), "").Charts[0].Legend {
		legend[l.Label] = l.Color + " " + l.Value
	}
	if len(legend) != 3 || legend["Assistants"] != "dc-cost-assistants $6.00" || !strings.HasPrefix(legend["No project"], "dc-cost-c") {
		t.Fatalf("legend %v", legend)
	}
}

func TestCostChartsSplitByCoderAssistantAndModel(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	r := costDayReport(now, nil)
	splits := CostSplits{
		Coders:     costDayReport(now, []cost.Share{{Kind: cost.KindCoder, Name: "fix", Coder: "claude", Session: "s1", USD: 2}, {Kind: cost.KindCoder, Coder: "claude", Session: "s2abcdefgh", USD: 1}}).Buckets,
		Assistants: costDayReport(now, []cost.Share{{Kind: cost.KindAssistant, Assistant: "a1", Name: "ops", USD: 1}}).Buckets,
		Models:     costDayReport(now, []cost.Share{{Model: "opus", USD: 3}, {USD: 1}}).Buckets,
	}
	var got []string
	for _, c := range NewCostBoard(r, splits, costTestState("range=today", now), "").Charts {
		var keys []string
		for _, l := range c.Legend {
			keys = append(keys, l.Label+" "+l.Value)
		}
		got = append(got, c.Title+": "+c.Total+" "+strings.Join(keys, ", "))
	}
	want := "Spend per hour by project: $0.00 |Spend per hour by assistant: $1.00 ops $1.00|Spend per hour by coder: $3.00 fix $2.00, Unnamed s2abcdef $1.00|Spend per hour by model: $4.00 opus $3.00, Unnamed model $1.00"
	if strings.Join(got, "|") != want {
		t.Fatalf("charts\n got %s\nwant %s", strings.Join(got, "|"), want)
	}
}

func TestCostStateReadsAndWritesTheURL(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	s := costTestState("range=custom&from=2026-10-20&to=2026-09-30&by=model&unit=tokens&model=&project=shop&q=+shop+&sort=name-asc", now)
	if s.From != "2026-09-30" || s.To != "2026-10-07" {
		t.Fatalf("state %+v", s)
	}
	if got := s.URL(); got != "/costs?from=2026-09-30&range=custom&to=2026-10-07" {
		t.Fatalf("url %s", got)
	}
	if got := costTestState("range=custom&from=nope&to=2026-10-01", now).Range; got != cost.Range30d {
		t.Fatalf("a broken custom range is %s", got)
	}
	if got := costTestState("by=nonsense&range=eternity", now).URL(); got != "/costs" {
		t.Fatalf("defaults %s", got)
	}
	steps := map[string]cost.Step{"today": cost.StepHour, "yesterday": cost.StepHour, "week": cost.StepDay, "lastmonth": cost.StepDay, "90d": cost.StepWeek}
	for name, want := range steps {
		if got := costTestState("range="+name, now).Span(now).Step; got != want {
			t.Errorf("%s steps %v, want %v", name, got, want)
		}
	}
	if got := costTestState("range=custom&from=2026-01-01&to=2026-10-01", now).Span(now).Step; got != cost.StepMonth {
		t.Errorf("nine months step %v", got)
	}
}

func TestCostToolbarStepsBackAndForthByThePeriod(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	cases := []struct{ state, prev, next string }{
		{"range=today", "/costs?range=yesterday", ""},
		{"range=yesterday", "/costs?from=2026-10-05&range=custom&to=2026-10-05", "/costs?range=today"},
		{"range=month", "/costs?range=lastmonth", ""},
		{"range=lastmonth", "/costs?from=2026-08-01&range=custom&to=2026-08-31", "/costs?range=month"},
		{"range=week", "/costs?range=lastweek", ""},
		{"range=7d", "/costs?from=2026-09-24&range=custom&to=2026-09-30", ""},
	}
	for _, c := range cases {
		s := costTestState(c.state, now)
		from, to := s.Bounds(now)
		tb := newCostToolbar(s, from, to, now)
		if tb.Prev != c.prev || tb.Next != c.next {
			t.Errorf("%s: prev %q next %q, want %q %q", c.state, tb.Prev, tb.Next, c.prev, c.next)
		}
	}
	s := costTestState("range=month", now)
	from, to := s.Bounds(now)
	tb := newCostToolbar(s, from, to, now)
	if tb.Label != "This month" || tb.Dates != "1 Oct to 31 Oct" || tb.CustomTo != "2026-10-07" {
		t.Fatalf("toolbar %+v", tb)
	}
}

func TestCostChartDrillsIntoADayAndAWeek(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	s := costTestState("range=90d", now)
	span := s.Span(now)
	r := cost.Report{Now: now}
	for start := span.From; start.Before(span.To); {
		end := cost.AddDays(cost.WeekStart(start), 7)
		if end.After(span.To) {
			end = span.To
		}
		r.Buckets = append(r.Buckets, cost.Bucket{Start: start, End: end})
		start = end
	}
	c := NewCostBoard(r, CostSplits{}, s, "").Charts[0]
	if c.Title != "Spend per week by project" || c.Columns[0].Drill != "/costs?from=2026-07-10&range=custom&to=2026-07-12#cost-project" || !c.Empty {
		t.Fatalf("the first week %+v", c.Columns[0])
	}
	if last := c.Columns[len(c.Columns)-1]; last.Drill != "/costs?from=2026-10-05&range=custom&to=2026-10-07#cost-project" || !strings.HasPrefix(last.DrillLabel, "Show the days of") {
		t.Fatalf("the week of today %+v", last)
	}
	s = costTestState("range=week", now)
	r.Buckets = nil
	for d := 0; d < 7; d++ {
		start := cost.AddDays(cost.WeekStart(now), d)
		r.Buckets = append(r.Buckets, cost.Bucket{Start: start, End: cost.AddDays(start, 1)})
	}
	c = NewCostBoard(r, CostSplits{}, s, "").Charts[0]
	if c.Columns[1].Drill != "/costs?range=yesterday#cost-project" || c.Columns[3].Drill != "" || c.Labels[0].Label != "Mon 5" || c.Labels[0].Anchor != "start" || c.Labels[6].Anchor != "end" || !c.Labels[1].Minor || c.Labels[6].Minor {
		t.Fatalf("week columns %+v labels %+v", c.Columns[1], c.Labels)
	}
}

func TestCostBoardTemplateRenders(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	r := costDayReport(now, []cost.Share{{Project: "shop", USD: 3}, {Assistants: true, USD: 1}})
	r.Unpriced = 1
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "costs_board.gohtml", NewCostBoard(r, CostSplits{}, costTestState("range=today", now), "List prices.")); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{`data-cost-col="`, `data-cost-tipbody="`, `data-series="p:shop"`, `data-cost-chart="model"`, `data-cost-nothing>`, `data-cost-unpriced>1 session ran`} {
		if !strings.Contains(html, want) {
			t.Errorf("the board misses %s", want)
		}
	}
}

func TestAnEmptyBoardRendersTheWholePage(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "costs_board.gohtml", NewCostBoard(cost.Report{Now: now}, CostSplits{}, costTestState("", now), "List prices.")); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	counts := map[string]int{`data-cost-tile="Today">$0.00<`: 1, `data-cost-tile="This month">$0.00<`: 1, `data-cost-toolbar`: 1, `data-cost-frame`: 4, `dc-cost-axis`: 4, `data-cost-nothing>`: 4, `data-cost-legend`: 0}
	for want, n := range counts {
		if got := strings.Count(html, want); got != n {
			t.Errorf("%s %d times, want %d", want, got, n)
		}
	}
}

func TestCostScaleStaysFiniteAndDrawsAtMostThreeLines(t *testing.T) {
	for _, peak := range []float64{0, -1, math.NaN(), 0.004, 1, 7.3, 1234, 1.6e308, math.MaxFloat64, math.Inf(1)} {
		top, grid := costScale(peak)
		if math.IsInf(top, 0) || math.IsNaN(top) || top <= 0 {
			t.Fatalf("peak %v: top %v", peak, top)
		}
		if len(grid) == 0 || len(grid) > costGridLines {
			t.Fatalf("peak %v: grid %v", peak, grid)
		}
		for _, g := range grid {
			if math.IsInf(g, 0) || g <= 0 || g > top {
				t.Fatalf("peak %v: grid line %v over top %v", peak, g, top)
			}
		}
		if peak > 0 && peak <= math.MaxFloat64/2 && top < peak {
			t.Fatalf("peak %v above top %v", peak, top)
		}
	}
	if top, grid := costScale(7.3); top != 7.5 || len(grid) != 3 || grid[2] != 7.5 {
		t.Fatalf("7.3: top %v grid %v", top, grid)
	}
}

func TestCostChartOfAnAbsurdDayDrawsBarsInsideThePlot(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	r := costDayReport(now, []cost.Share{{Project: "a", USD: 1.6e308}, {Project: "b", USD: 1.6e308}})
	r.Buckets[3].USD, r.Buckets[3].Parts = 1, []cost.Share{{Project: "a", USD: 1}}
	c := NewCostBoard(r, CostSplits{}, costTestState("range=today", now), "").Charts[0]
	if math.IsInf(c.Top, 0) || len(c.Grid) > costGridLines {
		t.Fatalf("top %v grid %+v", c.Top, c.Grid)
	}
	for i, col := range c.Columns {
		if !(col.Height >= 0 && col.Height <= 100) {
			t.Fatalf("column %d height %v", i, col.Height)
		}
	}
	if c.Columns[15].Height != 100 {
		t.Fatalf("the absurd hour is not full: %v", c.Columns[15].Height)
	}
}

func TestCostDaysPastTheFoldHaveNoHours(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	steps := map[string]cost.Step{"2026-09-07": cost.StepHour, "2026-09-06": cost.StepDay, "2026-09-03": cost.StepDay}
	for day, want := range steps {
		if got := costTestState("range=custom&from="+day+"&to="+day, now).Span(now).Step; got != want {
			t.Errorf("%s steps %v, want %v", day, got, want)
		}
	}
	s := costTestState("range=lastmonth", now)
	r := cost.Report{Now: now}
	from, to := s.Bounds(now)
	for d := from; d.Before(to); d = cost.AddDays(d, 1) {
		r.Buckets = append(r.Buckets, cost.Bucket{Start: d, End: cost.AddDays(d, 1)})
	}
	c := NewCostBoard(r, CostSplits{}, s, "").Charts[0]
	if c.Columns[5].Drill != "" || c.Columns[6].Drill != "/costs?from=2026-09-07&range=custom&to=2026-09-07#cost-project" || !strings.HasPrefix(c.Columns[6].DrillLabel, "Show the hours of") {
		t.Fatalf("6 Sep %+v, 7 Sep %+v", c.Columns[5], c.Columns[6])
	}
}
