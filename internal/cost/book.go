package cost

import (
	"math"
	"time"
)

// bookAll books a source's entries and notes for every session that it
// spent, placing the ones without a known owner by their directory.
func (s *Service) bookAll(l *ledger, coder string, entries []Entry, now time.Time) bool {
	changed := false
	index := rowIndex(l.Rows)
	for _, e := range entries {
		if e.Session == "" {
			continue
		}
		s.booked(&l.Owners, coder, e.Session, e.CWD, now)
		if !Usable(e.USD) || e.USD == 0 && e.Tokens == (Tokens{}) {
			continue
		}
		at := e.At
		if at.IsZero() || at.After(now) {
			at = now
		}
		add(&l.Rows, index, Row{
			At:       at.UTC().Truncate(fineBucket),
			Coder:    coder,
			Session:  e.Session,
			Model:    e.Model,
			USD:      e.USD,
			Tokens:   e.Tokens,
			Unpriced: e.Unpriced,
			TopUp:    e.TopUp,
		})
		changed = true
	}
	return changed
}

// Usable says whether an amount can be booked: a number, not below zero.
func Usable(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func (r Row) usable() bool {
	t := r.Tokens
	return Usable(r.USD) && t.Input >= 0 && t.Output >= 0 && t.CacheRead >= 0 && t.CacheWrite5m >= 0 && t.CacheWrite1h >= 0 && t.WebSearch >= 0
}

type rowKey struct {
	At       int64
	Coder    string
	Session  string
	Model    string
	Unpriced bool
	TopUp    bool
}

func keyOf(r Row) rowKey {
	return rowKey{At: r.At.Unix(), Coder: r.Coder, Session: r.Session, Model: r.Model, Unpriced: r.Unpriced, TopUp: r.TopUp}
}

func rowIndex(rows []Row) map[rowKey]int {
	index := make(map[rowKey]int, len(rows))
	for i, r := range rows {
		index[keyOf(r)] = i
	}
	return index
}

func add(rows *[]Row, index map[rowKey]int, r Row) {
	k := keyOf(r)
	if i, ok := index[k]; ok {
		(*rows)[i].USD += r.USD
		(*rows)[i].Tokens.Add(r.Tokens)
		return
	}
	index[k] = len(*rows)
	*rows = append(*rows, r)
}

// compact folds the buckets older than two days into hours and the days
// before the last thirty into days of loc. The page reads minutes only for
// the last hour, and hours keep days right in every zone with a whole hour
// offset. A folded day stays in the zone it was folded in.
func compact(rows []Row, now time.Time, loc *time.Location) []Row {
	hours := now.Add(-fineKeep)
	days := AddDays(now.In(loc), -dayKeep)
	out := make([]Row, 0, len(rows))
	index := map[rowKey]int{}
	for _, r := range rows {
		switch {
		case r.At.Before(days):
			r.At = DayStart(r.At.In(loc)).UTC()
		case r.At.Before(hours):
			r.At = r.At.UTC().Truncate(coarseBucket)
		}
		add(&out, index, r)
	}
	return out
}
