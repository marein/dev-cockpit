package cost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"
)

// deleteWait bounds how long a delete waits for a collect that runs. One
// that reads what the records gained takes milliseconds, only the first read
// of every record takes seconds, and a delete is not held that long.
// exitWait bounds the wait for the session's CLI to end: claude writes its
// running total within about half a second of a hangup.
const (
	deleteWait = 2 * time.Second
	exitWait   = 5 * time.Second
)

// Run notes what the cockpit runs and reads the sources every interval, the
// first time right away. Blocks.
func (s *Service) Run(interval time.Duration, owners func() Owners) {
	var errs errorLog
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ; ; <-ticker.C {
		errs.report(errors.Join(s.NoteOwners(owners()), s.Collect()))
	}
}

// errorLog logs what the collector ran into once, not on every tick it runs
// into it again: a folder that cannot be read for an hour is one line.
type errorLog struct{ last string }

func (e *errorLog) report(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if msg != "" && msg != e.last {
		log.Printf("cost: %s", msg)
	}
	e.last = msg
}

// Collect books what every source's records gained.
func (s *Service) Collect() error {
	s.books <- struct{}{}
	defer func() { <-s.books }()
	return s.collect(s.sources, "")
}

// BookSession books what one session's records gained, right before they
// are deleted. It first waits, at most exitWait, until the session's CLI
// ended, which writes on its way out and would bring a deleted record back.
// A collect that runs longer than deleteWait, the first read of every
// record, is not waited for; it reads the session itself unless it passed
// it already.
func (s *Service) BookSession(coder, session string) error {
	i := slices.IndexFunc(s.sources, func(src Source) bool { return src.Coder() == coder })
	if i < 0 {
		return nil
	}
	src := s.sources[i]
	for deadline := time.Now().Add(exitWait); src.Running(session) && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
	}
	select {
	case s.books <- struct{}{}:
	case <-time.After(deleteWait):
		return fmt.Errorf("%s session %s was deleted unbooked, every record is being read", coder, session)
	}
	defer func() { <-s.books }()
	return s.collect([]Source{src}, session)
}

// collect reads the sources, all sessions or one, and books what they
// return. What booked something, and once an hour what did not, folds the
// old rows, drops the months past the retention and writes, then notes the
// sessions that spent and drops the owners nobody needs any more. An entry
// of a month past the retention is not booked, its cursor moves on all the
// same. A file that cannot be read or a write that fails books nothing, the
// next collect reads from the same cursor.
func (s *Service) collect(sources []Source, session string) error {
	now := s.now()
	loc := s.zone()
	local := now.In(loc)
	l, err := s.load(local)
	if err != nil {
		return err
	}
	spent := map[string]string{}
	booked, moved := false, false
	var firstErr error
	for _, src := range sources {
		b, m, err := s.readSource(&l, src, session, now, spent)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		booked, moved = booked || b, moved || m
	}
	if !booked && !moved && now.Sub(s.maintained) < maintainEvery {
		return firstErr
	}
	keep := s.keepMonths()
	l.Rows = retain(compact(l.Rows, now, loc), local, keep)
	changed, err := s.save(l, moved)
	if changed {
		s.wrote()
	}
	if err != nil {
		return err
	}
	s.maintained = now
	noted, err := s.noteSpent(spent, l.Rows, AddMonths(local, 1-keep), now)
	if changed || noted {
		s.announce()
	}
	if err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// readSource reads one source and books its entries, noting in spent the
// directory of every session that spent. A source without a cursor reads
// every session: it never read, or the current month file that held its
// cursor is gone or corrupt, and then what it reads again replaces the rows
// of every session it read instead of booking them twice.
func (s *Service) readSource(l *ledger, src Source, session string, now time.Time, spent map[string]string) (booked, moved bool, err error) {
	coder := src.Coder()
	cursor := l.Cursors[coder]
	if len(cursor) == 0 {
		session = ""
	}
	entries, next, err := src.Collect(cursor, session, now)
	if err != nil {
		return false, false, err
	}
	if len(cursor) == 0 {
		again := map[string]bool{}
		for _, e := range entries {
			again[e.Session] = true
		}
		l.Rows = slices.DeleteFunc(l.Rows, func(r Row) bool { return r.Coder == coder && again[r.Session] })
	}
	for _, e := range entries {
		if key := coder + "/" + e.Session; e.Session != "" && spent[key] == "" {
			spent[key] = e.CWD
		}
	}
	if !sameJSON(next, cursor) {
		l.Cursors[coder] = next
		moved = true
	}
	return bookAll(l, coder, entries, now), moved, nil
}

// sameJSON compares two cursors by content: the month file stores them
// indented, a source hands them back compact, and a byte compare would
// rewrite it on every poll.
func sameJSON(a, b json.RawMessage) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}
