package render

import (
	"encoding/binary"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
)

// costFuzzZones are zones whose clocks make days odd: a day that starts
// with a jump at midnight, a date line hop, a quarter hour offset.
var costFuzzZones = []string{"UTC", "Europe/Berlin", "America/Sao_Paulo", "America/Havana", "Pacific/Apia", "Asia/Kathmandu"}

// costFuzzBuckets cuts the span the way the books do and hands every step
// shares of the given amounts, whatever bits they hold.
func costFuzzBuckets(t *testing.T, books *cost.Service, loc *time.Location, span cost.Span, amounts []byte) []cost.Bucket {
	t.Helper()
	buckets := books.Query(loc, span).Buckets
	for i := 0; len(buckets) > 0 && i+8 <= len(amounts); i += 8 {
		v := math.Float64frombits(binary.LittleEndian.Uint64(amounts[i:]))
		b := &buckets[(i/8)%len(buckets)]
		n := i / 8
		b.USD += v
		b.Parts = append(b.Parts, cost.Share{
			Kind:       cost.KindCoder,
			Project:    []string{"", "shop", "blog"}[n%3],
			Assistants: n%4 == 3,
			Assistant:  []string{"", "a1"}[n%2],
			Model:      []string{"claude-opus-5-5", ""}[n%2],
			Coder:      "claude",
			Session:    strings.Repeat("s", n%12),
			USD:        v,
		})
	}
	return buckets
}

func costFuzzPercent(t *testing.T, what string, v float64) {
	t.Helper()
	if !(v >= 0 && v <= 100) {
		t.Fatalf("%s at %v percent", what, v)
	}
}

// FuzzCostPage reads any query as the page's state in a zone with odd days
// and lays out the board over any amounts. Nothing panics or loops, every
// position stays on the plot, the bars stay few and the state survives its
// own links.
func FuzzCostPage(f *testing.F) {
	for _, q := range []string{
		"", "range=today", "range=yesterday", "range=week", "range=lastweek", "range=month", "range=lastmonth", "range=7d", "range=90d",
		"range=custom&from=2026-09-01&to=2026-09-30", "range=custom&from=2026-10-02&to=2026-10-02", "range=custom&from=2030-01-01&to=2020-01-01",
		"range=custom&from=0000-01-01&to=9999-12-31", "range=custom&from=2026-02-30&to=x", "range=%zz&from=;&to=&&unknown=1&range=today",
	} {
		f.Add(q, uint8(1), int32(0), []byte{})
	}
	amounts := func(vs ...float64) []byte {
		var out []byte
		for _, v := range vs {
			out = binary.LittleEndian.AppendUint64(out, math.Float64bits(v))
		}
		return out
	}
	f.Add("range=today", uint8(2), int32(-60*24*200), amounts(1.25, 0.004, 3000, 1e7))
	f.Add("range=90d", uint8(3), int32(60*24*45), amounts(math.MaxFloat64, math.MaxFloat64, math.Inf(1), math.NaN(), -5, math.SmallestNonzeroFloat64))
	f.Add("range=custom&from=2026-03-29&to=2026-03-29", uint8(4), int32(0), amounts(1, 2, 3))

	books := cost.New(f.TempDir(), nil)
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil)
	base := time.Date(2026, 10, 2, 15, 30, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, raw string, zone uint8, minutes int32, amounts []byte) {
		loc, err := time.LoadLocation(costFuzzZones[int(zone)%len(costFuzzZones)])
		if err != nil {
			t.Fatal(err)
		}
		now := base.Add(time.Duration(minutes) * time.Minute).In(loc)
		q, _ := url.ParseQuery(raw)
		state := ParseCostState(q, now)
		back, err := url.Parse(state.URL())
		if err != nil {
			t.Fatalf("the state's own link %q does not parse: %v", state.URL(), err)
		}
		if again := ParseCostState(back.Query(), now); again != state {
			t.Fatalf("%q reads back as %+v, was %+v", state.URL(), again, state)
		}

		span := state.Span(now)
		r := cost.Report{Now: now, Today: 1, Month: 2, TopUp: 0.5, Unpriced: 1}
		r.Buckets = costFuzzBuckets(t, books, loc, span, amounts)
		split := func(by cost.Dimension) []cost.Bucket {
			span.By = by
			return costFuzzBuckets(t, books, loc, span, amounts)
		}
		board := NewCostBoard(r, CostSplits{Coders: split(cost.BySession), Assistants: split(cost.BySession), Models: split(cost.ByModel)}, state, "List prices.")

		for _, link := range []string{board.Toolbar.Prev, board.Toolbar.Next} {
			if link == "" {
				continue
			}
			u, err := url.Parse(link)
			if err != nil {
				t.Fatalf("link %q: %v", link, err)
			}
			ParseCostState(u.Query(), now).Span(now)
		}
		for _, c := range board.Charts {
			if len(c.Columns) > 200 || len(c.Labels) > 12 {
				t.Fatalf("%s draws %d bars and %d labels for %+v", c.Key, len(c.Columns), len(c.Labels), state)
			}
			if !(c.Top > 0) || math.IsInf(c.Top, 0) || len(c.Grid) > costGridLines {
				t.Fatalf("%s scales to %v with %d lines", c.Key, c.Top, len(c.Grid))
			}
			for _, g := range c.Grid {
				costFuzzPercent(t, "a grid line", g.Bottom)
			}
			for _, col := range c.Columns {
				costFuzzPercent(t, "a bar", col.Height)
			}
			for _, l := range c.Labels {
				costFuzzPercent(t, "a label", l.Left)
			}
		}
		var out strings.Builder
		if err := tmpl.ExecuteTemplate(&out, "costs_board.gohtml", board); err != nil {
			t.Fatal(err)
		}
		if out.Len() > 4<<20 {
			t.Fatalf("the board renders %d bytes", out.Len())
		}
	})
}
