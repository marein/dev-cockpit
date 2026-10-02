package cost

import (
	"testing"
	"time"
)

func TestRangeSpansCoverWholeDays(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) }
	cases := map[string][2]time.Time{
		RangeToday:     {day(10, 7), day(10, 8)},
		RangeYesterday: {day(10, 6), day(10, 7)},
		RangeWeek:      {day(10, 5), day(10, 12)},
		RangeLastWeek:  {day(9, 28), day(10, 5)},
		RangeMonth:     {day(10, 1), day(11, 1)},
		RangeLastMonth: {day(9, 1), day(10, 1)},
		Range7d:        {day(10, 1), day(10, 8)},
		Range30d:       {day(9, 8), day(10, 8)},
		Range90d:       {day(7, 10), day(10, 8)},
		"nonsense":     {day(9, 8), day(10, 8)},
	}
	for name, want := range cases {
		from, to := RangeSpan(name, now)
		if !from.Equal(want[0]) || !to.Equal(want[1]) {
			t.Errorf("%s = %v to %v, want %v to %v", name, from, to, want[0], want[1])
		}
	}
}

func TestBucketsCutASpanIntoItsSteps(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip(err)
	}
	clockBack := time.Date(2026, 10, 25, 12, 0, 0, 0, loc)
	if got := len(buckets(Span{From: DayStart(clockBack), To: AddDays(clockBack, 1), Step: StepHour})); got != 25 {
		t.Errorf("the day the clock goes back has %d hours, want 25", got)
	}
	from := time.Date(2026, 7, 10, 0, 0, 0, 0, loc)
	weeks := buckets(Span{From: from, To: time.Date(2026, 10, 8, 0, 0, 0, 0, loc), Step: StepWeek})
	if first := weeks[0]; first.Start != from || first.End.Weekday() != time.Monday || first.End.Day() != 13 {
		t.Errorf("the first week %v to %v, want the rest of the week the span starts in", first.Start, first.End)
	}
	if last := weeks[len(weeks)-1]; last.End.Day() != 8 || last.Start.Day() != 5 {
		t.Errorf("the last week %v to %v, want it to end with the span", last.Start, last.End)
	}
	months := buckets(Span{From: time.Date(2026, 1, 15, 0, 0, 0, 0, loc), To: time.Date(2026, 4, 1, 0, 0, 0, 0, loc), Step: StepMonth})
	if len(months) != 3 || months[1].Start.Month() != time.February || months[1].Start.Day() != 1 {
		t.Errorf("months %v", months)
	}
	if buckets(Span{From: from}) != nil {
		t.Error("a span without a step has buckets")
	}
}

func TestFilterMatchesEveryFieldItSets(t *testing.T) {
	str := func(s string) *string { return &s }
	shop := Row{Coder: "claude", Session: "s1", Model: "opus", Attribution: Attribution{Kind: KindCoder, Project: "shop", Name: "fix"}}
	loose := Row{Coder: "claude", Session: "s2", Attribution: Attribution{Kind: KindOther}}
	ops := Row{Coder: "claude", Session: "s3", Model: "sonnet", Attribution: Attribution{Kind: KindAssistant, Assistant: "ops", Name: "ops"}}
	cases := []struct {
		f    Filter
		want [3]bool
	}{
		{Filter{}, [3]bool{true, true, true}},
		{Filter{Project: str("shop")}, [3]bool{true, false, false}},
		{Filter{Project: str("")}, [3]bool{false, true, false}},
		{Filter{Assistants: true}, [3]bool{false, false, true}},
		{Filter{Assistant: "ops"}, [3]bool{false, false, true}},
		{Filter{Assistant: "fix"}, [3]bool{false, false, false}},
		{Filter{Kind: KindAssistant}, [3]bool{false, false, true}},
		{Filter{Model: str("")}, [3]bool{false, true, false}},
		{Filter{Coder: "claude", Session: "s2"}, [3]bool{false, true, false}},
		{Filter{Coder: "copilot"}, [3]bool{false, false, false}},
	}
	for _, c := range cases {
		for i, row := range []Row{shop, loose, ops} {
			if got := c.f.Matches(row); got != c.want[i] {
				t.Errorf("%+v on row %d = %v", c.f, i, got)
			}
		}
	}
}

func TestQueryFiltersEverythingAndSplitsTheBuckets(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	at := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	l := ledger{Rows: []Row{
		{At: at(7, 9), Coder: "claude", Session: "a", Model: "opus", Attribution: Attribution{Kind: KindCoder, Project: "shop"}, USD: 2, Tokens: Tokens{Output: 100}},
		{At: at(7, 10), Coder: "claude", Session: "b", Model: "sonnet", Attribution: Attribution{Kind: KindAssistant, Assistant: "ops", Name: "ops"}, USD: 1, Tokens: Tokens{Output: 50}},
		{At: at(5, 10), Coder: "claude", Session: "a", Model: "opus", Attribution: Attribution{Kind: KindCoder, Project: "shop"}, USD: 4},
		{At: at(1, 10), Coder: "claude", Session: "c", Model: "opus", Attribution: Attribution{Kind: KindCoder, Project: "blog"}, USD: 8},
	}}
	from, to := RangeSpan(RangeWeek, now)
	r := build(l.Rows, now, Span{From: from, To: to, Step: StepDay, By: ByModel})
	if len(r.Buckets) != 7 || !near(r.Total, 7) {
		t.Fatalf("buckets %d total %v", len(r.Buckets), r.Total)
	}
	today := r.Buckets[2]
	if !near(today.USD, 3) || today.Tokens.Total() != 150 || len(today.Parts) != 2 || today.Parts[0].Model != "opus" || !near(today.Parts[0].USD, 2) {
		t.Fatalf("today %+v", today)
	}
	shop := "shop"
	r = build(l.Rows, now, Span{From: from, To: to, Step: StepDay, By: ByProject, Filter: Filter{Project: &shop}})
	if !near(r.Total, 6) || !near(r.Today, 2) || !near(r.Month, 6) || len(r.Projects) != 1 || r.Buckets[2].Parts[0].Project != "shop" {
		t.Fatalf("filtered %+v", r)
	}
	from, to = RangeSpan(RangeLastWeek, now)
	r = build(l.Rows, now, Span{From: from, To: to, Step: StepDay})
	if !near(r.Total, 8) || !near(r.Buckets[3].USD, 8) {
		t.Fatalf("last week total %v", r.Total)
	}
}
