package forge

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"sync"
	"time"
)

// The pace of the poller. It looks at the branches it keeps every pollTick
// and reads one again every pollFast while its pipeline has not finished or
// for pollMoved after its pushed head moved or a write changed it, else
// every pollSlow. A read that failed waits twice as long as the one before,
// up to pollSlow, one the server limited until the limit lifts. At most
// pollParallel reads run at once, each within pollTimeout.
const (
	pollTick     = 5 * time.Second
	pollFast     = 15 * time.Second
	pollSlow     = 5 * time.Minute
	pollMoved    = 2 * time.Minute
	pollParallel = 4
	pollTimeout  = time.Minute
)

// Pace is the pace of the poller for the pages that tell it.
type Pace struct {
	Fast, Moved, Slow, Review time.Duration
}

// PollPace is the pace the poller keeps.
func PollPace() Pace {
	return Pace{Fast: pollFast, Moved: pollMoved, Slow: pollSlow, Review: pollReview}
}

// CacheFresh is how old a read of the poller may be to answer in place of
// the forge.
const CacheFresh = 30 * time.Second

// Watch is a branch whose status the poller keeps: the project's, where the
// project is bound, the branch's local name and its name on the forge, and
// the commit it was last pushed at, "" for a branch never pushed. A watch
// without a branch names a bound project whose branch could not be told this
// time: it is not read and keeps what it holds.
type Watch struct {
	Project string
	Binding Binding
	Branch  string
	Remote  string
	Head    string
}

func (w Watch) same(o Watch) bool {
	return w.branch(o) && w.Head == o.Head
}

// branch reports whether o is the same branch of the same project, bound the
// same way, wherever its head stands.
func (w Watch) branch(o Watch) bool {
	return w.Project == o.Project && w.Branch == o.Branch && w.Remote == o.Remote && w.Binding.same(o.Binding)
}

// moved reports whether o is w after a push or a fetch moved its head.
func (w Watch) moved(o Watch) bool {
	return w.branch(o) && w.Head != o.Head && o.Head != ""
}

func (b Binding) same(o Binding) bool {
	return b.Path == o.Path && b.Host.ID == o.Host.ID && b.Host.Kind.ID == o.Host.Kind.ID && b.Host.WebURL == o.Host.WebURL
}

// Polled is what the poller holds of a watch: what the forge answered when
// it was last read, Read zero before any answer, and why the last read
// failed, nil when it went through. A failure keeps the answer the same
// watch had before it.
type Polled struct {
	Watch  Watch
	Status BranchStatus
	Read   time.Time
	Err    error
}

// shows reports whether two reads show the same.
func (p Polled) shows(o Polled) bool {
	return p.Watch.same(o.Watch) && p.Read.IsZero() == o.Read.IsZero() && reflect.DeepEqual(p.Status, o.Status) && errorText(p.Err) == errorText(o.Err)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// poller is the cache the poll fills, by project. turns counts the changes
// of each host, a read answers for the count at its start and is dropped
// when its host changed meanwhile, accounts holds who each host's token
// belongs to in its turn and tested what the last test of each host's token
// found in it. kept is the projects the poller last told it keeps, nil
// before it told any.
type poller struct {
	now   func() time.Time
	wake  chan struct{}
	slots chan struct{}
	reads sync.WaitGroup

	mu       sync.Mutex
	watched  map[string]*watched
	turns    map[string]int
	accounts map[string]account
	tested   map[string]Connection
	kept     map[string]bool
	changed  func(project string)
}

// watched is one branch the poller keeps: where it stands now, what was read of
// it and of its review threads, when it is read next, since when its head
// moved or a write changed it, how many reads in a row failed and how
// to end the read in flight, nil while none is.
type watched struct {
	watch  Watch
	polled Polled
	review reviewed
	next   time.Time
	moved  time.Time
	fails  int
	stop   context.CancelFunc
}

// pollRead is one read the poller started: of w, for the branch k keeps, in
// its host's turn, after as many writes of its review threads, and whether it
// reads the review threads of a change request with that number too.
type pollRead struct {
	ctx     context.Context
	stop    context.CancelFunc
	k       *watched
	w       Watch
	turn    int
	writes  int
	threads func(change int) bool
}

func newPoller() *poller {
	return &poller{now: time.Now, wake: make(chan struct{}, 1), slots: make(chan struct{}, pollParallel), watched: map[string]*watched{}, turns: map[string]int{}, accounts: map[string]account{}, tested: map[string]Connection{}}
}

// nudge has the poller look at its branches now instead of at its next tick.
func (p *poller) nudge() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// wrote reads w's branch again at once and at the fast pace for pollMoved,
// where the poller keeps it: a write changed the branch on the forge, a retry
// runs its pipeline again, a change request opened for it, which a read at
// the slow pace would show minutes late.
func (p *poller) wrote(w Watch) {
	p.mu.Lock()
	if k := p.watched[w.Project]; k != nil && k.watch.branch(w) {
		k.next = time.Time{}
		k.moved = p.now()
	}
	p.mu.Unlock()
	p.nudge()
}

// wroteNotes is wrote for a write of review threads: the next read of w's
// branch reads its threads too, and a read of them in flight, which may have
// asked the forge before the write, publishes nothing, so it does not take
// back what the write changed.
func (p *poller) wroteNotes(w Watch) {
	p.mu.Lock()
	if k := p.watched[w.Project]; k != nil && k.watch.branch(w) {
		k.review.asked = time.Time{}
		k.review.writes++
	}
	p.mu.Unlock()
	p.wrote(w)
}

// hostChanged ends the reads in flight on the host and reads every branch on
// it again at once: its token or its settings changed, and what the new ones
// may read is not what the old ones could.
func (p *poller) hostChanged(id string) {
	p.mu.Lock()
	p.turns[id]++
	delete(p.accounts, id)
	delete(p.tested, id)
	for _, k := range p.watched {
		if k.watch.Binding.Host.ID == id {
			k.next = time.Time{}
			k.review.asked = time.Time{}
			if k.stop != nil {
				k.stop()
			}
		}
	}
	p.mu.Unlock()
	p.nudge()
}

func (p *poller) turn(id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.turns[id]
}

// Run keeps the status of the branches watches names until ctx ends, and
// calls changed with each project whose status moved, one that went
// included. watches is asked every pollTick and whenever a host, a token or
// a binding changed, with polled, which reports whether a binding is read at
// all: on a host with a token, of a provider that reads status. A failure of
// watches leaves everything as it stands.
func (s *Service) Run(ctx context.Context, watches func(ctx context.Context, polled func(Binding) bool) ([]Watch, error), changed func(project string)) {
	s.poll.mu.Lock()
	s.poll.changed = changed
	s.poll.mu.Unlock()
	tick := time.NewTicker(pollTick)
	defer tick.Stop()
	for {
		s.pollRound(ctx, watches)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.poll.wake:
		}
	}
}

// pollRound looks at the branches watches names once: it starts a read of
// each one due, new or moved, and forgets the ones no longer named. Without
// a token on any host watches is not asked, nothing would be read. A read
// takes the token its host has when it starts, and reads the review threads
// of the branch's change request too when somebody hears of them and they
// are due. Whoever hears of them hears which projects it keeps after the
// first round and whenever they change.
func (s *Service) pollRound(ctx context.Context, watches func(context.Context, func(Binding) bool) ([]Watch, error)) {
	tokens, err := s.tokens()
	if err != nil {
		return
	}
	polled := func(b Binding) bool {
		_, ok := s.statusReader(b)
		return ok && tokens[b.Host.ID] != ""
	}
	var list []Watch
	if len(tokens) > 0 {
		if list, err = watches(ctx, polled); err != nil {
			return
		}
	}
	p := s.poll
	now := p.now()
	listens := s.hearsReview()
	var due []pollRead
	var gone []string
	named := map[string]bool{}
	p.mu.Lock()
	for _, w := range list {
		if !polled(w.Binding) || named[w.Project] {
			continue
		}
		named[w.Project] = true
		if w.Branch == "" {
			continue
		}
		k := p.watched[w.Project]
		switch {
		case k == nil:
			k = &watched{watch: w}
			p.watched[w.Project] = k
		case !k.watch.same(w):
			k.moved = time.Time{}
			if k.watch.moved(w) {
				k.moved = now
			}
			k.watch = w
		}
		if k.stop != nil || (k.polled.Watch.same(w) && now.Before(k.next)) {
			continue
		}
		readCtx, stop := context.WithCancel(ctx)
		k.stop = stop
		last := k.review
		threads := func(change int) bool { return listens && last.due(w, change, now) }
		due = append(due, pollRead{ctx: readCtx, stop: stop, k: k, w: w, turn: p.turns[w.Binding.Host.ID], writes: last.writes, threads: threads})
	}
	for project, k := range p.watched {
		if named[project] {
			continue
		}
		delete(p.watched, project)
		if !k.polled.shows(Polled{}) {
			gone = append(gone, project)
		}
	}
	kept := listens && (p.kept == nil || !maps.Equal(p.kept, named))
	if kept {
		p.kept = named
	}
	changed := p.changed
	p.mu.Unlock()
	if changed != nil {
		for _, project := range gone {
			changed(project)
		}
	}
	if kept {
		p.reads.Add(1)
		go func() {
			defer p.reads.Done()
			s.noteKept(named)
		}()
	}
	for _, r := range due {
		p.reads.Add(1)
		go func() {
			defer p.reads.Done()
			defer r.stop()
			status, threads, err := s.readBranch(r.ctx, r.w, r.turn, r.threads)
			if !p.store(r.w, status, err, r.turn, r.k) {
				return
			}
			var off *BranchOff
			switch {
			case err == nil:
				s.noteEnd(r.w, status)
				s.noteReview(r, status, threads)
			case errors.As(err, &off):
				s.noteReview(r, BranchStatus{}, nil)
			}
		}()
	}
}

// readBranch reads w once a slot is free, and the review threads of the
// change request it found when threads says so, nil when it does not, with
// who the token of the host's turn belongs to while somebody hears of new
// notes.
func (s *Service) readBranch(ctx context.Context, w Watch, turn int, threads func(change int) bool) (BranchStatus, *threadsRead, error) {
	select {
	case s.poll.slots <- struct{}{}:
	case <-ctx.Done():
		return BranchStatus{}, nil, ctx.Err()
	}
	defer func() { <-s.poll.slots }()
	ctx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	status, err := s.BranchStatus(ctx, w)
	if err != nil || status.Change == nil || !threads(status.Change.Number) {
		return status, nil, err
	}
	review, err := s.Threads(ctx, w.Binding, status.Change.Number)
	read := &threadsRead{review: review, err: err}
	if err == nil && s.hearsNotes() {
		read.self, read.known = s.account(ctx, w.Binding.Host, turn)
	}
	return status, read, nil
}

// store keeps what a read of w in its host's turn answered, when w is still
// where the project's branch stands, schedules the next read and reports
// whether it kept the read. read is the branch of the poller's read, which
// then is over, nil for a read of another. A read of a turn gone by, or one
// that found the token gone, is dropped and the branch read again. A failed
// read keeps what the last one of the same commit found, unless it found the
// forge features off for the branch, which shows nothing of it.
func (p *poller) store(w Watch, status BranchStatus, err error, turn int, read *watched) bool {
	now := p.now()
	p.mu.Lock()
	k := p.watched[w.Project]
	if k == nil || (read != nil && read != k) {
		p.mu.Unlock()
		return false
	}
	if read != nil {
		k.stop = nil
	}
	if !k.watch.same(w) {
		p.mu.Unlock()
		return false
	}
	if p.turns[w.Binding.Host.ID] != turn || errors.Is(err, ErrNoToken) {
		p.mu.Unlock()
		p.nudge()
		return false
	}
	before := k.polled
	if err == nil {
		k.polled = Polled{Watch: w, Status: status, Read: now}
		k.fails = 0
		k.next = now.Add(k.pace(status, now))
	} else {
		var off *BranchOff
		if !before.Watch.same(w) || errors.As(err, &off) {
			k.polled = Polled{Watch: w}
		}
		k.polled.Err = err
		k.next = now.Add(k.backoff(err))
	}
	moved := !before.shows(k.polled)
	changed := p.changed
	p.mu.Unlock()
	if moved && changed != nil {
		changed(w.Project)
	}
	return true
}

// pace is how long a branch whose read went through waits for the next.
func (k *watched) pace(status BranchStatus, now time.Time) time.Duration {
	if pl := status.Pipeline; pl != nil && (pl.Status == Pending || pl.Status == Running) {
		return pollFast
	}
	if now.Sub(k.moved) < pollMoved {
		return pollFast
	}
	return pollSlow
}

// backoff is how long a branch whose read failed waits for the next.
func (k *watched) backoff(err error) time.Duration {
	var limited *RateLimitError
	if errors.As(err, &limited) {
		return max(limited.Wait, pollFast)
	}
	k.fails++
	wait := pollFast
	for i := 1; i < k.fails && wait < pollSlow; i++ {
		wait *= 2
	}
	return min(wait, pollSlow)
}

// fresh answers the poller's read of w when it went through no longer than
// CacheFresh ago and no change of its host since owes a read.
func (p *poller) fresh(w Watch) (Polled, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := p.watched[w.Project]
	if k == nil || !k.polled.Watch.same(w) || k.polled.Err != nil || k.polled.Read.IsZero() || k.next.IsZero() || p.now().Sub(k.polled.Read) > CacheFresh {
		return Polled{}, false
	}
	return k.polled, true
}

// Polled answers what the poller holds of the project's branch on b. It
// holds nothing of a project it does not poll, nor before its first read,
// and what it holds of another branch or binding, the moment after a
// checkout or a change of the binding, is not answered.
func (s *Service) Polled(project string, b Binding, branch string) (Polled, bool) {
	s.poll.mu.Lock()
	defer s.poll.mu.Unlock()
	k := s.poll.watched[project]
	if k == nil || k.polled.shows(Polled{}) || k.polled.Watch.Branch != branch || !k.polled.Watch.Binding.same(b) {
		return Polled{}, false
	}
	return k.polled, true
}

// Status answers what the forge says of w's branch: what the poller read of
// it no longer than CacheFresh ago, else what the forge answers now, which
// the poller keeps when w is where the branch it keeps stands. fresh always
// asks the forge.
func (s *Service) Status(ctx context.Context, w Watch, fresh bool) (BranchStatus, error) {
	if !fresh {
		if polled, ok := s.poll.fresh(w); ok {
			return polled.Status, nil
		}
	}
	turn := s.poll.turn(w.Binding.Host.ID)
	status, err := s.BranchStatus(ctx, w)
	if errors.Is(err, ErrNoToken) || errors.Is(err, ErrNoStatus) || ctx.Err() != nil {
		return status, err
	}
	if s.poll.store(w, status, err, turn, nil) && err == nil {
		s.noteEnd(w, status)
	}
	return status, err
}
