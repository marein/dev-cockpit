package cost

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// The cost folder holds one file of rows per month and the owners of the
// sessions:
//
//	rows-YYYY-MM.json  the rows booked in that month of the cost zone, the
//	                   newest file also the cursors of every source
//	sessions.json      who each session belongs to, the assistants' names
//
// The newest month file is the commit point: a booking writes its rows and
// the cursors that moved past them in one rename, so a write that fails
// commits neither and the next collect reads the same records again. A row
// lives in the file of the month it was booked in, a late call of a closed
// month too, so a closed month only changes by what moves no money: a fold,
// the retention, the rows a lost cursor reads again replace. Those are
// written first, the commit point last. The owners move no money and are
// written on their own.
const (
	rowsPrefix   = "rows-"
	monthLayout  = "2006-01"
	sessionsFile = "sessions.json"
)

type monthFile struct {
	Rows    []Row                      `json:"rows"`
	Cursors map[string]json.RawMessage `json:"cursors,omitempty"`
}

// ledger is the month files in memory. Each row knows its file. months is
// each month file as it was read, what a save compares against; into is the
// commit point, the month new rows are booked in.
type ledger struct {
	Rows    []Row
	Cursors map[string]json.RawMessage
	months  map[string]monthFile
	into    string
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

// loadMonths reads every month file. A row whose amount or tokens no
// booking can produce is dropped, it would break every sum the pages draw.
// A file that exists but cannot be read is an error: its rows are unknown,
// not none.
func (s *Service) loadMonths() (map[string]monthFile, error) {
	months := map[string]monthFile{}
	for _, month := range s.monthsOnDisk() {
		var f monthFile
		if err := readFile(s.monthPath(month), &f); err != nil {
			return nil, err
		}
		kept := make([]Row, 0, len(f.Rows))
		for _, r := range f.Rows {
			if r.usable() {
				r.file = month
				kept = append(kept, r)
			}
		}
		f.Rows = kept
		months[month] = f
	}
	return months, nil
}

// readFile reads a state file. A file that is there but cannot be read is
// an error: what it holds is unknown, not nothing, and nothing may be
// written over it. A corrupt one is quarantined by statefile.Load.
func readFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) > 0 && json.Unmarshal(data, v) != nil {
		statefile.Load(path, v)
	}
	return nil
}

func flatten(months map[string]monthFile) []Row {
	var rows []Row
	for _, month := range slices.Sorted(maps.Keys(months)) {
		rows = append(rows, months[month].Rows...)
	}
	return rows
}

// load reads the month files for a booking made at now. The cursors are the
// newest month's, and new rows go into the later of that month and now's.
func (s *Service) load(now time.Time) (ledger, error) {
	months, err := s.loadMonths()
	if err != nil {
		return ledger{}, err
	}
	l := ledger{Rows: flatten(months), Cursors: map[string]json.RawMessage{}, months: months, into: now.Format(monthLayout)}
	if names := slices.Sorted(maps.Keys(months)); len(names) > 0 {
		newest := names[len(names)-1]
		maps.Copy(l.Cursors, months[newest].Cursors)
		l.into = max(l.into, newest)
	}
	return l, nil
}

// rows is the rows for the readers, each with who owns it now, read again
// after every write. A file that cannot be read leaves its rows out and
// nothing is cached.
func (s *Service) rows() []Row {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cached != nil && s.cachedGen == s.gen {
		return s.cached
	}
	months, err := s.loadMonths()
	owners, ownersErr := s.loadOwners()
	rows := flatten(months)
	for i := range rows {
		rows[i].Attribution = owners.attribution(rows[i].Coder, rows[i].Session)
	}
	if rows == nil {
		rows = []Row{}
	}
	if err == nil && ownersErr == nil {
		s.cached, s.cachedGen = rows, s.gen
	}
	return rows
}

// wrote tells the readers that what they cached is old.
func (s *Service) wrote() {
	s.cacheMu.Lock()
	s.gen++
	s.cacheMu.Unlock()
}

func (s *Service) announce() {
	if s.onChange != nil {
		s.onChange()
	}
}

// save writes the closed months whose rows changed and removes the ones
// left empty, and last the commit point if its rows changed or a cursor
// moved. It says whether it wrote anything.
func (s *Service) save(l ledger, cursorsMoved bool) (bool, error) {
	after := map[string][]Row{}
	for _, r := range l.Rows {
		after[r.file] = append(after[r.file], r)
	}
	changed := false
	for _, month := range slices.Sorted(maps.Keys(after)) {
		if month == l.into {
			continue
		}
		same, err := sameRows(after[month], l.months[month].Rows)
		if err != nil {
			return changed, err
		}
		if same {
			continue
		}
		if err := statefile.Write(s.monthPath(month), 0o600, monthFile{Rows: after[month], Cursors: l.months[month].Cursors}); err != nil {
			return changed, err
		}
		changed = true
	}
	for month := range l.months {
		if _, ok := after[month]; ok || month == l.into {
			continue
		}
		if err := os.Remove(s.monthPath(month)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return changed, err
		}
		changed = true
	}
	same, err := sameRows(after[l.into], l.months[l.into].Rows)
	if err != nil {
		return changed, err
	}
	if same && !cursorsMoved {
		return changed, nil
	}
	rows := after[l.into]
	if rows == nil {
		rows = []Row{}
	}
	if err := statefile.Write(s.monthPath(l.into), 0o600, monthFile{Rows: rows, Cursors: l.Cursors}); err != nil {
		return changed, err
	}
	return true, nil
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
