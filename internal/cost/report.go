package cost

import (
	"sort"
	"time"
)

// Group is where spend counts in the per project views. The assistants run
// in no project and count together, everything else under its project, the
// empty one for spend without a project.
type Group struct {
	Assistants bool
	Project    string
}

// GroupOf is the group a booking counts in.
func GroupOf(a Attribution) Group {
	if a.Kind == KindAssistant {
		return Group{Assistants: true}
	}
	return Group{Project: a.Project}
}

// Day is one calendar day of spend.
type Day struct {
	Start time.Time
	Total float64
}

// Share is one entry of a breakdown. Session is set where the entry is one
// coder session, Coder is the CLI it ran in. Assistants marks the share of
// all assistants together in the project breakdown, Assistant is the id of
// one assistant's share.
type Share struct {
	Kind       Kind
	Project    string
	Assistants bool
	Assistant  string
	Name       string
	Model      string
	Coder      string
	Session    string
	USD        float64
	Tokens     Tokens
}

// Bucket is one step of a span's series, split by the span's dimension.
type Bucket struct {
	Start  time.Time
	End    time.Time
	USD    float64
	Tokens Tokens
	Parts  []Share
}

// Report is everything the cost surfaces show, computed from the rows alone.
// Days are the last thirty in the zone, oldest first, Hours the hours of today,
// Burn the spend of the last sixty minutes. The breakdowns cover Since to
// Until, or to now where Until is zero, and Buckets cut that span into steps.
type Report struct {
	Now      time.Time
	Today    float64
	Week     float64
	Month    float64
	Burn     float64
	Days     []Day
	Hours    []float64
	Since    time.Time
	Until    time.Time
	Buckets  []Bucket
	Total    float64
	Projects []Share
	// Assistants splits the assistants' share of Projects by assistant.
	Assistants []Share
	Sessions   []Share
	Models     []Share
	Kinds      []Share
	// Unpriced counts the sessions in the range that ran a model without a
	// list price: their tokens count, no money does.
	Unpriced int
	// TopUp is the part of Total that a CLI's own running total holds above
	// its logged calls, calls it made without a record.
	TopUp float64
	// Booked says whether any row is booked at all.
	Booked bool
}

// Ranges of the breakdowns: calendar periods, the one before them, and
// spans of days that end today.
const (
	RangeToday     = "today"
	RangeYesterday = "yesterday"
	RangeWeek      = "week"
	RangeLastWeek  = "lastweek"
	RangeMonth     = "month"
	RangeLastMonth = "lastmonth"
	Range7d        = "7d"
	Range30d       = "30d"
	Range90d       = "90d"
)

// DayStart is the first moment of t's day in its zone. That is midnight,
// except in a zone whose clocks jump forward at midnight: there the day
// begins with the jump, and Go would read the missing midnight as 23:00 of
// the day before.
func DayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	if _, _, sd := start.Date(); sd != d {
		if _, end := start.ZoneBounds(); !end.IsZero() {
			return end
		}
	}
	return start
}

// AddDays is the start of the day n calendar days from t's day. It counts
// from noon, which exists on every day, so no clock change moves it to a
// neighbouring date.
func AddDays(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	return DayStart(time.Date(y, m, d+n, 12, 0, 0, 0, t.Location()))
}

// WeekStart is the Monday of t's week.
func WeekStart(t time.Time) time.Time {
	return AddDays(t, -((int(t.Weekday()) + 6) % 7))
}

// MonthStart is the first of t's month.
func MonthStart(t time.Time) time.Time { return AddMonths(t, 0) }

// RangeSpan answers the whole days a named range covers, the end exclusive.
// A period that is still running ends with its last day, not with now.
func RangeSpan(name string, now time.Time) (time.Time, time.Time) {
	tomorrow := AddDays(now, 1)
	switch name {
	case RangeToday:
		return DayStart(now), tomorrow
	case RangeYesterday:
		return AddDays(now, -1), DayStart(now)
	case RangeWeek:
		return WeekStart(now), AddDays(WeekStart(now), 7)
	case RangeLastWeek:
		return AddDays(WeekStart(now), -7), WeekStart(now)
	case RangeMonth:
		return MonthStart(now), AddMonths(now, 1)
	case RangeLastMonth:
		return AddMonths(now, -1), MonthStart(now)
	case Range7d:
		return AddDays(now, -6), tomorrow
	case Range90d:
		return AddDays(now, -89), tomorrow
	}
	return AddDays(now, -29), tomorrow
}

// AddMonths is the first of the month n months from t's.
func AddMonths(t time.Time, n int) time.Time {
	y, m, _ := t.Date()
	return DayStart(time.Date(y, m+time.Month(n), 1, 12, 0, 0, 0, t.Location()))
}

// Step is the width of a span's buckets.
type Step int

const (
	StepNone Step = iota
	StepHour
	StepDay
	StepWeek
	StepMonth
)

// next is where the bucket that starts at t ends. A week ends on a Monday
// and a month on a first, so a span that starts inside one begins with the
// rest of it. Hours count from the span's start, a day with a clock change
// has one more or one less.
func (st Step) next(t time.Time) time.Time {
	switch st {
	case StepHour:
		return t.Add(time.Hour)
	case StepDay:
		return AddDays(t, 1)
	case StepWeek:
		return AddDays(WeekStart(t), 7)
	}
	return AddMonths(t, 1)
}

// Dimension is what a span's buckets are split by.
type Dimension string

const (
	ByProject Dimension = "project"
	ByKind    Dimension = "kind"
	ByModel   Dimension = "model"
)

// part is the share of a booking in the dimension, the fields that name it
// set and nothing else.
func (d Dimension) part(row Row) Share {
	switch d {
	case ByKind:
		return Share{Kind: row.Kind}
	case ByModel:
		return Share{Model: row.Model}
	}
	g := GroupOf(row.Attribution)
	return Share{Project: g.Project, Assistants: g.Assistants}
}

// Filter narrows a report to the bookings that match every field set. A
// nil pointer matches anything, an empty string behind one only the empty
// value: Project points at "" for spend outside every project.
type Filter struct {
	// Project is spend in this project, the assistants' excluded.
	Project *string
	// Assistants is the spend of every assistant, Assistant of the one with
	// this id.
	Assistants bool
	Assistant  string
	Kind       Kind
	Model      *string
	Coder      string
	Session    string
}

// Matches says whether a booking passes the filter.
func (f Filter) Matches(row Row) bool {
	g := GroupOf(row.Attribution)
	switch {
	case f.Project != nil && (g.Assistants || g.Project != *f.Project),
		(f.Assistants || f.Assistant != "") && !g.Assistants,
		f.Assistant != "" && row.Assistant != f.Assistant,
		f.Kind != "" && row.Kind != f.Kind,
		f.Model != nil && row.Model != *f.Model,
		f.Coder != "" && row.Coder != f.Coder,
		f.Session != "" && row.Session != f.Session:
		return false
	}
	return true
}

// Span is what a query covers: the breakdowns from From to To, To
// exclusive, a series of Step wide buckets split By a dimension, and only
// the bookings Filter lets through, the headline numbers included. A zero
// To runs to now, a zero Step leaves the series out.
type Span struct {
	From   time.Time
	To     time.Time
	Step   Step
	By     Dimension
	Filter Filter
}

// Query reads the month files and aggregates a span of them in loc. It
// takes no lock: a month file is replaced by a rename, and a render must not
// wait on a backfill.
func (s *Service) Query(loc *time.Location, span Span) Report {
	return build(s.read(), s.now().In(loc), span)
}

func build(rows []Row, now time.Time, span Span) Report {
	loc := now.Location()
	today := DayStart(now)
	first := AddDays(now, -29)
	r := Report{
		Now:     now,
		Days:    make([]Day, 30),
		Hours:   make([]float64, 24),
		Since:   span.From,
		Until:   span.To,
		Buckets: buckets(span),
		Booked:  len(rows) > 0,
	}
	parts := make([]map[Share]*Share, len(r.Buckets))
	for i := range parts {
		parts[i] = map[Share]*Share{}
	}
	for i := range r.Days {
		r.Days[i] = Day{Start: AddDays(first, i)}
	}
	week, month, hourAgo := WeekStart(now), MonthStart(now), now.Add(-time.Hour)
	projects := map[Group]*Share{}
	assistants := map[string]*Share{}
	sessions := map[Share]*Share{}
	models := map[string]*Share{}
	kinds := map[Kind]*Share{}
	unpriced := map[string]bool{}
	for _, row := range rows {
		at := row.At.In(loc)
		if at.After(now) || !span.Filter.Matches(row) {
			continue
		}
		if !at.Before(today) {
			r.Today += row.USD
			r.Hours[at.Hour()] += row.USD
		}
		if !at.Before(week) {
			r.Week += row.USD
		}
		if !at.Before(month) {
			r.Month += row.USD
		}
		if at.After(hourAgo) {
			r.Burn += row.USD
		}
		if i := dayIndex(first, at); !at.Before(first) && i >= 0 && i < len(r.Days) {
			r.Days[i].Total += row.USD
		}
		if at.Before(r.Since) || !r.Until.IsZero() && !at.Before(r.Until) {
			continue
		}
		if i := bucketIndex(r.Buckets, at); i >= 0 {
			b := &r.Buckets[i]
			b.USD += row.USD
			b.Tokens.Add(row.Tokens)
			part := span.By.part(row)
			bump(parts[i], part, part, row)
		}
		r.Total += row.USD
		if row.TopUp {
			r.TopUp += row.USD
		}
		if row.Unpriced {
			unpriced[row.Coder+"/"+row.Session] = true
		}
		group := GroupOf(row.Attribution)
		bump(projects, group, Share{Project: group.Project, Assistants: group.Assistants}, row)
		if group.Assistants {
			bump(assistants, row.Assistant, Share{Assistant: row.Assistant, Name: row.Name}, row)
		}
		key := Share{Kind: row.Kind, Project: row.Project, Assistant: row.Assistant, Name: row.Name}
		if row.Kind == KindCoder || row.Kind == KindOther {
			key.Coder, key.Session = row.Coder, row.Session
		}
		bump(sessions, key, key, row)
		bump(models, row.Model, Share{Model: row.Model}, row)
		bump(kinds, row.Kind, Share{Kind: row.Kind}, row)
	}
	r.Unpriced = len(unpriced)
	r.Projects = sorted(projects)
	r.Assistants = sorted(assistants)
	r.Sessions = sorted(sessions)
	r.Models = sorted(models)
	r.Kinds = sorted(kinds)
	for i := range r.Buckets {
		r.Buckets[i].Parts = sorted(parts[i])
	}
	return r
}

// buckets cuts a span into its steps, the last one ending with the span.
func buckets(span Span) []Bucket {
	if span.Step == StepNone || span.To.IsZero() {
		return nil
	}
	var out []Bucket
	for start := span.From; start.Before(span.To); {
		end := span.Step.next(start)
		if end.After(span.To) {
			end = span.To
		}
		out = append(out, Bucket{Start: start, End: end})
		start = end
	}
	return out
}

func bucketIndex(bs []Bucket, at time.Time) int {
	i := sort.Search(len(bs), func(i int) bool { return bs[i].End.After(at) })
	if i == len(bs) || at.Before(bs[i].Start) {
		return -1
	}
	return i
}

// dayIndex counts calendar days, not 24 hour spans, so a day with a clock
// change still lands on its own date.
func dayIndex(first, at time.Time) int {
	y, m, d := at.Date()
	day := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	fy, fm, fd := first.Date()
	start := time.Date(fy, fm, fd, 12, 0, 0, 0, time.UTC)
	return int(day.Sub(start).Hours() / 24)
}

func bump[K comparable](m map[K]*Share, key K, init Share, row Row) {
	s, ok := m[key]
	if !ok {
		s = &init
		m[key] = s
	}
	s.USD += row.USD
	s.Tokens.Add(row.Tokens)
}

func sorted[K comparable](m map[K]*Share) []Share {
	out := make([]Share, 0, len(m))
	for _, s := range m {
		out = append(out, *s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].USD != out[j].USD {
			return out[i].USD > out[j].USD
		}
		return out[i].Name+out[i].Project+out[i].Model < out[j].Name+out[j].Project+out[j].Model
	})
	return out
}
