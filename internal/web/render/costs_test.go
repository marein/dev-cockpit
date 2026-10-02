package render

import (
	"fmt"
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
	r := cost.Report{Now: now, Booked: true, Since: from, Until: cost.AddDays(now, 1), Hours: make([]float64, 24)}
	for h := 0; h < 24; h++ {
		start := from.Add(time.Duration(h) * time.Hour)
		r.Buckets = append(r.Buckets, cost.Bucket{Start: start, End: start.Add(time.Hour)})
	}
	b := &r.Buckets[now.Hour()]
	for _, p := range parts {
		b.USD += p.USD
		b.Tokens.Add(p.Tokens)
		r.Total += p.USD
		b.Parts = append(b.Parts, p)
	}
	return r
}

func costTestState(raw string, now time.Time) CostState {
	q, _ := url.ParseQuery(raw)
	return ParseCostState(q, now)
}

func TestCostChartStacksTheTopProjectsAndFoldsTheRest(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 30, 0, 0, time.UTC)
	var parts []cost.Share
	for i := 0; i < 9; i++ {
		parts = append(parts, cost.Share{Project: string(rune('a' + i)), USD: float64(10 - i)})
	}
	r := costDayReport(now, parts)
	c := NewCostBoard(r, r, costTestState("range=today", now), "").Chart
	if c.Title != "Spend per hour" || len(c.Columns) != 24 || !c.Columns[15].Current || c.Columns[14].Current || !c.Columns[16].Future {
		t.Fatalf("columns %d, title %s", len(c.Columns), c.Title)
	}
	if len(c.Legend) != len(costPalette)+1 || c.Legend[len(c.Legend)-1].Label != "Other projects" || c.Legend[len(c.Legend)-1].Value != "$5.00" {
		t.Fatalf("legend = %+v", c.Legend)
	}
	col := c.Columns[15]
	if len(col.Segments) != len(costPalette)+1 || col.Value != "$54.00" || col.Segments[len(col.Segments)-1].Color != costOtherColor {
		t.Fatalf("the bar of now = %+v", col)
	}
	if col.Segments[0].Series != "p:a" || col.Segments[0].Color != "dc-cost-c1" || col.Tips[0].Label != "a" || col.Tips[0].Value != "$10.00" {
		t.Fatalf("the stack starts with the first name and the tooltip with the biggest part: %+v %+v", col.Segments[0], col.Tips[0])
	}
	if c.Top != 60 || col.Height != 90 || col.Drill != "" || col.Cum != 90 || c.CumTop != "$60.00" {
		t.Fatalf("top %v height %v drill %q cum %v %s", c.Top, col.Height, col.Drill, col.Cum, c.CumTop)
	}
	if !strings.HasPrefix(c.CumLine, "0,100 2.08,100.00") || !strings.HasSuffix(c.CumLine, "64.58,10.00") || c.Empty {
		t.Fatalf("the running total stops at now: %s", c.CumLine)
	}
}

func TestCostChartKeepsTheColorsOfTheUnfilteredSpan(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	all := costDayReport(now, []cost.Share{{Project: "a", USD: 1}, {Project: "b", USD: 2}, {Project: "c", USD: 3}})
	only := costDayReport(now, []cost.Share{{Project: "c", USD: 3}})
	c := NewCostBoard(only, all, costTestState("range=today&project=c", now), "").Chart
	if len(c.Legend) != 1 || c.Legend[0].Color != "dc-cost-c3" {
		t.Fatalf("c lost its color under the filter: %+v", c.Legend)
	}
}

func TestCostChartSplitsByKindInAFixedOrder(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	r := costDayReport(now, []cost.Share{
		{Kind: cost.KindAssistant, USD: 1, Tokens: cost.Tokens{Output: 3000}},
		{Kind: cost.KindCoder, USD: 2, Tokens: cost.Tokens{Output: 1000}},
	})
	c := NewCostBoard(r, r, costTestState("range=today&by=kind&unit=tokens", now), "").Chart
	col := c.Columns[15]
	if c.Title != "Tokens per hour" || col.Segments[0].Series != "k:coder" || col.Segments[1].Color != costAssistantsColor || col.Value != "4.0K tokens" {
		t.Fatalf("kind stack %+v", col)
	}
	if c.Legend[0].Label != "Assistant" || c.Legend[0].Value != "3.0K tokens" || c.Grid[len(c.Grid)-1].Label != "4.0K" {
		t.Fatalf("legend %+v grid %+v", c.Legend, c.Grid)
	}
}

func TestCostBoardShowsTheAssistantsAsOneGroupSplitByName(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	r := costDayReport(now, []cost.Share{{Assistants: true, USD: 6}, {Project: "shop", USD: 3}, {USD: 1}})
	r.Projects = []cost.Share{{Assistants: true, USD: 6}, {Project: "shop", USD: 3}, {USD: 1}}
	r.Assistants = []cost.Share{{Assistant: "a1", Name: "Ops", USD: 4}, {Assistant: "a2", USD: 2}}
	b := NewCostBoard(r, r, costTestState("", now), "")
	var rows []string
	for _, row := range b.Breakdowns[0].Rows {
		rows = append(rows, fmt.Sprintf("%s/%s/%v", row.Label, row.Value, row.Drill))
		for _, child := range row.Children {
			rows = append(rows, fmt.Sprintf("  %s/%s/%v", child.Label, child.Value, child.Drill))
		}
	}
	want := "Assistants/$6.00//costs?group=assistants,  Ops/$4.00//costs?assistant=a1,  Unnamed assistant/$2.00//costs?assistant=a2," +
		"shop/$3.00//costs?project=shop,No project/$1.00//costs?project="
	if got := strings.Join(rows, ","); got != want {
		t.Fatalf("project rows\n got %s\nwant %s", got, want)
	}
	legend := map[string]string{}
	for _, l := range b.Chart.Legend {
		legend[l.Label] = l.Color
	}
	if legend["Assistants"] != costAssistantsColor || !strings.HasPrefix(legend["No project"], "dc-cost-c") {
		t.Fatalf("legend %v", legend)
	}
}

func TestCostRowsDrillIntoWhatTheyNameAndChipsTakeItBack(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	r := costDayReport(now, nil)
	r.Sessions = []cost.Share{
		{Kind: cost.KindCoder, Project: "shop", Name: "fix", Coder: "claude", Session: "s1", USD: 2},
		{Kind: cost.KindAssistant, Assistant: "a1", Name: "ops", USD: 1},
	}
	r.Kinds = []cost.Share{{Kind: cost.KindCoder, USD: 2, Tokens: cost.Tokens{Output: 10}}}
	r.Models = []cost.Share{{Model: "opus", USD: 2}, {USD: 0, Tokens: cost.Tokens{Output: 5}}}
	r.Total = 3
	s := costTestState("range=week&kind=coder&unit=tokens", now)
	b := NewCostBoard(r, r, s, "")
	sessions := b.Breakdowns[2].Rows
	if sessions[0].Drill != "/costs?coder=claude&kind=coder&range=week&session=s1&unit=tokens" || sessions[0].Sub != "shop" || sessions[0].Badge != "Coder" {
		t.Fatalf("a coder session %+v", sessions[0])
	}
	if sessions[1].Drill != "/costs?assistant=a1&kind=assistant&range=week&unit=tokens" || sessions[1].Sub != "" {
		t.Fatalf("an assistant %+v", sessions[1])
	}
	if kinds := b.Breakdowns[1].Rows; kinds[0].Drill != "" || kinds[0].Value != "10 tokens" || kinds[0].Alt != "$2.00" || kinds[0].Percent != 100 {
		t.Fatalf("the kind the page already shows %+v", kinds[0])
	}
	if models := b.Breakdowns[3].Rows; models[1].Label != "Unnamed model" || models[1].Drill != "/costs?kind=coder&model=&range=week&unit=tokens" {
		t.Fatalf("the unnamed model %+v", models[1])
	}
	if len(b.Chips) != 1 || b.Chips[0].Value != "Coder" || b.Chips[0].Remove != "/costs?range=week&unit=tokens" || b.ClearURL != "/costs?range=week&unit=tokens" {
		t.Fatalf("chips %+v clear %s", b.Chips, b.ClearURL)
	}
	s = costTestState("session=s1&coder=claude", now)
	if chips := NewCostBoard(r, r, s, "").Chips; len(chips) != 2 || chips[1].Label != "Session" || chips[1].Value != "fix" || chips[1].Remove != "/costs?coder=claude" {
		t.Fatalf("session chips %+v", chips)
	}
}

func TestCostStateReadsAndWritesTheURL(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	s := costTestState("range=custom&from=2026-10-20&to=2026-09-30&by=model&unit=tokens&model=&q=+shop+&sort=name-asc", now)
	if s.From != "2026-09-30" || s.To != "2026-10-07" || s.By != cost.ByModel || !s.Tokens || s.Filter.Model == nil || s.Query != "shop" {
		t.Fatalf("state %+v", s)
	}
	if got := s.URL(); got != "/costs?by=model&from=2026-09-30&model=&range=custom&to=2026-10-07&unit=tokens" {
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
		{"range=month&project=shop", "/costs?project=shop&range=lastmonth", ""},
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
	s := costTestState("range=month&kind=coder&by=kind", now)
	from, to := s.Bounds(now)
	tb := newCostToolbar(s, from, to, now)
	if tb.Label != "This month" || tb.Dates != "1 Oct to 31 Oct" || tb.CustomTo != "2026-10-07" || len(tb.Carry) != 2 || tb.Carry[0].Name != "by" {
		t.Fatalf("toolbar %+v", tb)
	}
}

func TestCostChartDrillsIntoADayAndAWeek(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	s := costTestState("range=90d&project=shop", now)
	span := s.Span(now)
	r := cost.Report{Now: now, Booked: true, Hours: make([]float64, 24)}
	for start := span.From; start.Before(span.To); {
		end := cost.AddDays(cost.WeekStart(start), 7)
		if end.After(span.To) {
			end = span.To
		}
		r.Buckets = append(r.Buckets, cost.Bucket{Start: start, End: end})
		start = end
	}
	c := NewCostBoard(r, r, s, "").Chart
	if c.Title != "Spend per week" || c.Columns[0].Drill != "/costs?from=2026-07-10&project=shop&range=custom&to=2026-07-12" || !c.Empty {
		t.Fatalf("the first week %+v", c.Columns[0])
	}
	if last := c.Columns[len(c.Columns)-1]; last.Drill != "/costs?from=2026-10-05&project=shop&range=custom&to=2026-10-07" || !strings.HasPrefix(last.DrillLabel, "Show the days of") {
		t.Fatalf("the week of today %+v", last)
	}
	s = costTestState("range=week", now)
	r.Buckets = nil
	for d := 0; d < 7; d++ {
		start := cost.AddDays(cost.WeekStart(now), d)
		r.Buckets = append(r.Buckets, cost.Bucket{Start: start, End: cost.AddDays(start, 1)})
	}
	c = NewCostBoard(r, r, s, "").Chart
	if c.Columns[1].Drill != "/costs?range=yesterday" || c.Columns[3].Drill != "" || c.Labels[0].Label != "Mon 5" || c.Labels[0].Anchor != "start" || c.Labels[6].Anchor != "end" || !c.Labels[1].Minor || c.Labels[6].Minor {
		t.Fatalf("week columns %+v labels %+v", c.Columns[1], c.Labels)
	}
}

func TestCostBoardTemplateRenders(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	r := costDayReport(now, []cost.Share{{Project: "shop", USD: 3}, {Assistants: true, USD: 1}})
	r.Projects = []cost.Share{{Project: "shop", USD: 3}, {Assistants: true, USD: 1}}
	r.Assistants = []cost.Share{{Name: "Ops", USD: 1}}
	r.Sessions = []cost.Share{{Kind: cost.KindCoder, Project: "a-very-long-project-name-that-goes-on", Name: "fix", Coder: "claude", Session: "s1", USD: 3}}
	r.Unpriced = 1
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "costs_board.gohtml", NewCostBoard(r, r, costTestState("range=today&q=fix", now), "List prices.")); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{`data-cost-col="`, `data-cost-tipbody="`, `data-series="p:shop"`, `class="dc-cost-children"`, `title="a-very-long-project-name-that-goes-on"`, `value="fix"`, `data-cost-unpriced`, `href="/costs?project=shop&amp;range=today"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the board misses %s", want)
		}
	}
	if strings.Contains(html, `fw-medium text-break dc-trunc`) || strings.Count(html, `class="fw-medium text-break" data-cost-label`) != 4 {
		t.Errorf("a main label is cut or missing")
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
	r.Hours[15] = math.Inf(1)
	b := NewCostBoard(r, r, costTestState("range=today", now), "")
	c := b.Chart
	if math.IsInf(c.Top, 0) || len(c.Grid) > costGridLines {
		t.Fatalf("top %v grid %+v", c.Top, c.Grid)
	}
	for i, col := range c.Columns {
		if !(col.Height >= 0 && col.Height <= 100) || !(col.Cum >= 0 && col.Cum <= 100) {
			t.Fatalf("column %d height %v cum %v", i, col.Height, col.Cum)
		}
	}
	if c.Columns[15].Height != 100 {
		t.Fatalf("the absurd hour is not full: %v", c.Columns[15].Height)
	}
	for _, bar := range b.Tiles[0].Spark {
		if !(bar.Height >= 0 && bar.Height <= 100) {
			t.Fatalf("spark %+v", b.Tiles[0].Spark)
		}
	}
}

func TestTheAssistantChipNamesTheAssistantOfTheId(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	r := costDayReport(now, nil)
	r.Assistants = []cost.Share{{Assistant: "a1", Name: "Ops", USD: 1}}
	chips := NewCostBoard(r, r, costTestState("assistant=a1", now), "").Chips
	if len(chips) != 1 || chips[0].Label != "Assistant" || chips[0].Value != "Ops" || chips[0].Remove != "/costs" {
		t.Fatalf("chips %+v", chips)
	}
	if chips := NewCostBoard(r, r, costTestState("assistant=gone", now), "").Chips; chips[0].Value != "Unnamed assistant" {
		t.Fatalf("an id without bookings in the range %+v", chips)
	}
}
