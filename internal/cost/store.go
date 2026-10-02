package cost

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// The cost folder holds one file of rows per month, the owners of the
// sessions and the sources' cursors:
//
//	rows-YYYY-MM.json  the bookings of one month in the cost zone
//	sessions.json      who each session belongs to, the assistants' names
//	cursors.json       how far each source read, its own state
//
// A tick rewrites the month it booked into. A closed month is written again
// only when a fold, a late entry or an owner that became known changes it, and
// a month past the retention is deleted. The rows are written before the
// cursors: a crash between the two books a tick twice rather than losing it.
const (
	rowsPrefix   = "rows-"
	monthLayout  = "2006-01"
	sessionsFile = "sessions.json"
	cursorsFile  = "cursors.json"
)

type monthFile struct {
	Rows []Row `json:"rows"`
}

// ledger is the cost folder in memory. months is each month file as it was
// read, what a save compares against.
type ledger struct {
	Rows    []Row
	Owners  directory
	Cursors map[string]json.RawMessage
	months  map[string][]Row
}

func (s *Service) monthPath(month string) string {
	return filepath.Join(s.dir, rowsPrefix+month+".json")
}

// monthsOnDisk lists the month files, oldest first.
func (s *Service) monthsOnDisk() []string {
	paths, _ := filepath.Glob(filepath.Join(s.dir, rowsPrefix+"*.json"))
	var out []string
	for _, p := range paths {
		month := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), rowsPrefix), ".json")
		if _, err := time.Parse(monthLayout, month); err == nil {
			out = append(out, month)
		}
	}
	slices.Sort(out)
	return out
}

// loadRows reads every month file. A row whose amount or tokens no booking
// can produce is dropped, it would break every sum the pages draw.
func (s *Service) loadRows() ([]Row, map[string][]Row) {
	var rows []Row
	months := map[string][]Row{}
	for _, month := range s.monthsOnDisk() {
		var f monthFile
		statefile.Load(s.monthPath(month), &f)
		kept := make([]Row, 0, len(f.Rows))
		for _, r := range f.Rows {
			if r.usable() {
				kept = append(kept, r)
			}
		}
		months[month] = kept
		rows = append(rows, kept...)
	}
	return rows, months
}

func (s *Service) load() ledger {
	l := ledger{}
	l.Rows, l.months = s.loadRows()
	l.Owners = s.loadOwners()
	statefile.Load(filepath.Join(s.dir, cursorsFile), &l.Cursors)
	if l.Cursors == nil {
		l.Cursors = map[string]json.RawMessage{}
	}
	return l
}

func (s *Service) loadOwners() directory {
	var d directory
	statefile.Load(filepath.Join(s.dir, sessionsFile), &d)
	d.ensure()
	return d
}

// read is the rows for the readers, each with who owns it now, parsed again
// only when a month file or the owners moved. The worker is their only
// writer, so the names, sizes and times of the files say whether it did.
func (s *Service) read() []Row {
	var sig strings.Builder
	paths := []string{filepath.Join(s.dir, sessionsFile)}
	for _, month := range s.monthsOnDisk() {
		paths = append(paths, s.monthPath(month))
	}
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil {
			fmt.Fprintf(&sig, "%s %d %d\n", p, info.Size(), info.ModTime().UnixNano())
		}
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cached != nil && sig.String() == s.cacheSig {
		return s.cached
	}
	rows, _ := s.loadRows()
	owners := s.loadOwners()
	for i := range rows {
		rows[i].Attribution = owners.attribution(rows[i].Coder, rows[i].Session)
	}
	if rows == nil {
		rows = []Row{}
	}
	s.cached, s.cacheSig = rows, sig.String()
	return rows
}

// save writes the months whose rows changed and removes the ones left empty,
// then the owners if they changed, then the cursors if they moved. It says
// whether a month or an owner changed, what the pages show.
func (s *Service) save(l ledger, ownersBefore directory, cursorsMoved bool, loc *time.Location) (bool, error) {
	after := map[string][]Row{}
	for _, r := range l.Rows {
		month := r.At.In(loc).Format(monthLayout)
		after[month] = append(after[month], r)
	}
	changed := false
	for _, month := range slices.Sorted(maps.Keys(after)) {
		rows := after[month]
		if same, err := sameRows(rows, l.months[month]); err != nil || same {
			if err != nil {
				return changed, err
			}
			continue
		}
		if err := statefile.Write(s.monthPath(month), 0o600, monthFile{Rows: rows}); err != nil {
			return changed, err
		}
		changed = true
	}
	for month := range l.months {
		if _, ok := after[month]; ok {
			continue
		}
		if err := os.Remove(s.monthPath(month)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return changed, err
		}
		changed = true
	}
	if !l.Owners.equal(ownersBefore) {
		if err := statefile.Write(filepath.Join(s.dir, sessionsFile), 0o600, l.Owners); err != nil {
			return changed, err
		}
		changed = true
	}
	if cursorsMoved {
		if err := statefile.Write(filepath.Join(s.dir, cursorsFile), 0o600, l.Cursors); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

func sameRows(a, b []Row) (bool, error) {
	ja, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	jb, err := json.Marshal(b)
	if err != nil {
		return false, err
	}
	return string(ja) == string(jb), nil
}

// retain drops the rows of the months before the n kept, now's month
// included, cut in now's zone.
func retain(rows []Row, now time.Time, n int) []Row {
	first := AddMonths(now, 1-n).Format(monthLayout)
	out := rows[:0]
	for _, r := range rows {
		if r.At.In(now.Location()).Format(monthLayout) >= first {
			out = append(out, r)
		}
	}
	return out
}
