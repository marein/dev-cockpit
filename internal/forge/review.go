package forge

import (
	"log"
	"maps"
	"reflect"
	"time"
)

// pollReview is how long what the poller read of the review threads of a
// branch's change request stands before a read of the branch reads them
// again, while neither the change request nor the pushed head moved.
const pollReview = time.Minute

// ReviewRead is what the poller read of the review threads of a branch it
// keeps: the branch, its change request, nil for a branch without one, which
// has none, and the threads.
type ReviewRead struct {
	Watch  Watch
	Change *Change
	Review Review
}

func (r ReviewRead) number() int {
	if r.Change == nil {
		return 0
	}
	return r.Change.Number
}

// OnReview sets what hears of the review threads of the branches the poller
// keeps. read hears of them on the first read of a branch, of each one that
// read them otherwise than the one before, and of one whose head or change
// request moved, also to none. kept hears which projects the poller keeps,
// and every other one has none: its binding, host or token went, or its HEAD
// stands on no branch. Only while somebody hears of them, or of new notes
// (OnReviewNote), does the poller read them.
func (s *Service) OnReview(read func(ReviewRead), kept func(projects map[string]bool)) {
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	s.onReview, s.onKept = read, kept
}

func (s *Service) hearsReview() bool {
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	return s.onReview != nil || s.onNote != nil
}

// reviewed is what the poller did about the review threads of a branch: what
// it published last, nil before anything, the last time it asked the forge,
// for which watch and change request number, why that failed, "" when it went
// through, and how many writes of the threads the cockpit made.
type reviewed struct {
	published *ReviewRead
	asked     time.Time
	at        Watch
	change    int
	failed    string
	writes    int
}

// due reports whether a read of w that found the change request with that
// number reads its threads too: when it found one, and the threads were last
// asked for of another head or change request, or longer than pollReview ago.
func (r reviewed) due(w Watch, change int, now time.Time) bool {
	return change != 0 && (!r.at.same(w) || r.change != change || now.Sub(r.asked) >= pollReview)
}

// threadsRead is what a read of review threads answered, and who the
// host's token belongs to when known says the read could tell.
type threadsRead struct {
	review Review
	err    error
	self   string
	known  bool
}

// noteReview keeps what the read r, which found status, answered of the
// review threads, nil when it did not ask, and publishes it when it differs
// from what was published last, while r is still where the branch stands in
// its host's turn and no write of the threads came after its start, after the
// notes it saw first. A failed read publishes nothing.
func (s *Service) noteReview(r pollRead, status BranchStatus, threads *threadsRead) {
	p := s.poll
	now := p.now()
	next := ReviewRead{Watch: r.w, Change: status.Change}
	p.mu.Lock()
	k := p.watched[r.w.Project]
	if k != r.k || !k.watch.same(r.w) || p.turns[r.w.Binding.Host.ID] != r.turn || k.review.writes != r.writes {
		p.mu.Unlock()
		return
	}
	last := k.review
	publish, notes := false, false
	switch {
	case status.Change == nil:
		k.review.at, k.review.change, k.review.asked, k.review.failed = r.w, 0, now, ""
		publish = last.published == nil || !last.published.Watch.branch(r.w) || last.published.Change != nil
	case threads == nil:
	case threads.err != nil:
		k.review.at, k.review.change, k.review.asked, k.review.failed = r.w, status.Change.Number, now, threads.err.Error()
		if last.failed != k.review.failed {
			log.Printf("forge: the review threads of %s could not be read: %v", r.w.Project, threads.err)
		}
	default:
		k.review.at, k.review.change, k.review.asked, k.review.failed = r.w, status.Change.Number, now, ""
		next.Review = threads.review
		notes = threads.known
		was := last.published
		publish = was == nil || !was.Watch.same(r.w) || was.number() != next.number() || !reflect.DeepEqual(was.Review, next.Review)
	}
	if publish {
		k.review.published = &next
	}
	p.mu.Unlock()
	if notes {
		s.noteNotes(r.w, *status.Change, threads.review, threads.self)
	}
	if !publish {
		return
	}
	s.reviewMu.Lock()
	hear := s.onReview
	s.reviewMu.Unlock()
	if hear == nil {
		return
	}
	s.tell(func() bool { return p.watched[r.w.Project] == k && k.review.published == &next }, func() { hear(next) })
}

// noteKept tells that the poller keeps the projects named, while it still
// does.
func (s *Service) noteKept(named map[string]bool) {
	s.reviewMu.Lock()
	hear := s.onKept
	s.reviewMu.Unlock()
	if hear == nil {
		return
	}
	s.tell(func() bool { return maps.Equal(s.poll.kept, named) }, func() { hear(named) })
}

// tell has hear called while current, asked under the poller's lock, says
// what it tells is still what the poller holds; a later word replaces it.
// One at a time, so a project is heard of in the order the poller decided,
// and apart from the lock a poll round asks, so a slow one holds up no
// round.
func (s *Service) tell(current func() bool, hear func()) {
	s.reviewCalls.Lock()
	defer s.reviewCalls.Unlock()
	s.poll.mu.Lock()
	ok := current()
	s.poll.mu.Unlock()
	if ok {
		hear()
	}
}
