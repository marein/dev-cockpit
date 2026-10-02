package cost

import (
	"log"
	"math"
	"time"
)

// job is one piece of queued work, applied by the worker to the cost folder
// as it stands in memory. It says whether it booked rows and whether it
// moved a source's cursor.
type job func(l *ledger, now time.Time) (booked, moved bool)

// enqueue hands work to the worker and returns at once. A caller holding a
// lock of its own may call it: nothing here waits.
func (s *Service) enqueue(j job) {
	s.queueMu.Lock()
	s.queue = append(s.queue, j)
	s.queueMu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) take() []job {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	jobs := s.queue
	s.queue = nil
	return jobs
}

// Run is the worker: it applies what is queued as it comes, and reads the
// sources every interval, the first time right away. The burn rate of the
// last hour falls while nothing is spent, and only a write announces
// itself, so a burn rate that moved by a cent is announced here. Blocks.
func (s *Service) Run(interval time.Duration) {
	lastBurn := int64(-1)
	collect := func() {
		if err := s.Collect(); err != nil {
			log.Printf("cost: %v", err)
		}
		burn := int64(math.Round(s.lastHour() * 100))
		if lastBurn >= 0 && burn != lastBurn && s.onChange != nil {
			s.onChange()
		}
		lastBurn = burn
	}
	collect()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.wake:
			if err := s.Settle(); err != nil {
				log.Printf("cost: %v", err)
			}
		case <-ticker.C:
			collect()
		}
	}
}

func (s *Service) lastHour() float64 {
	since := s.now().Add(-time.Hour)
	sum := 0.0
	for _, r := range s.read() {
		if r.At.After(since) {
			sum += r.USD
		}
	}
	return sum
}

// Settle applies what is queued, in the order it came, and writes once.
func (s *Service) Settle() error { return s.work(false) }

// Collect applies what is queued and then reads every source.
func (s *Service) Collect() error { return s.work(true) }

// work applies the queue and, with read, the sources. What booked something,
// and once an hour what did not, folds the old rows, drops the months past
// the retention and the owners nobody needs any more. An entry of a month
// past the retention is not booked, its cursor moves on all the same.
func (s *Service) work(read bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := s.take()
	if len(jobs) == 0 && !read {
		return nil
	}
	l := s.load()
	before := l.Owners.clone()
	now := s.now()
	booked, moved := false, false
	for _, j := range jobs {
		b, m := j(&l, now)
		booked, moved = booked || b, moved || m
	}
	var firstErr error
	if read {
		for _, src := range s.sources {
			b, m, err := s.read1(&l, src, nil, now)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			booked, moved = booked || b, moved || m
		}
	}
	if !booked && !moved && l.Owners.equal(before) && now.Sub(s.maintained) < maintainEvery {
		return firstErr
	}
	loc := s.zone()
	local := now.In(loc)
	l.Rows = retain(compact(l.Rows, now, loc), local, s.keepMonths())
	l.Owners.prune(l.Rows, AddMonths(local, 1-s.keepMonths()))
	changed, err := s.save(l, before, moved, loc)
	if err != nil {
		return err
	}
	s.maintained = now
	if changed && s.onChange != nil {
		s.onChange()
	}
	return firstErr
}

// read1 reads one source, everything on disk or what a capture holds.
func (s *Service) read1(l *ledger, src Source, captured any, now time.Time) (booked, moved bool, err error) {
	entries, cursor, err := src.Collect(l.Cursors[src.Coder()], captured, now)
	if err != nil {
		return false, false, err
	}
	if !sameJSON(cursor, l.Cursors[src.Coder()]) {
		l.Cursors[src.Coder()] = cursor
		moved = true
	}
	return s.bookAll(l, src.Coder(), entries, now), moved, nil
}

func (s *Service) source(coder string) Source {
	for _, src := range s.sources {
		if src.Coder() == coder {
			return src
		}
	}
	return nil
}

// SessionDeleting is the one step of a delete that waits: the session's
// records are read into memory while they still exist, the CLI already
// ended. Booking them is queued, the delete goes on.
func (s *Service) SessionDeleting(coder, session string) {
	src := s.source(coder)
	if src == nil {
		return
	}
	captured := src.Capture(session)
	s.enqueue(func(l *ledger, now time.Time) (bool, bool) {
		booked, moved, err := s.read1(l, src, captured, now)
		if err != nil {
			log.Printf("cost: book %s/%s before its delete: %v", coder, session, err)
		}
		return booked, moved
	})
}

// CoderSession names a coder the cockpit runs: on a start, a resume, a
// rename, and for every coder there is when the server starts.
func (s *Service) CoderSession(coder, session, name, cwd string) {
	s.enqueue(func(l *ledger, now time.Time) (bool, bool) {
		s.coderSession(&l.Owners, coder, session, name, cwd, now)
		return false, false
	})
}

// AssistantNamed names an assistant: on a create, on every rename and for
// every assistant there is when the server starts. The name stays for its
// bookings once it is gone.
func (s *Service) AssistantNamed(id, name string) {
	s.enqueue(func(l *ledger, now time.Time) (bool, bool) {
		l.Owners.Assistants[id] = assistantName{Name: name, Seen: day(now)}
		return false, false
	})
}
