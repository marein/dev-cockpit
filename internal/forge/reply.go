package forge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrNoReply answers a reply on a review thread on a forge whose provider
// cannot answer them.
var ErrNoReply = errors.New("This forge cannot answer review threads.")

// ErrNoResolve answers a resolve of a review thread on a forge whose
// provider cannot resolve them.
var ErrNoResolve = errors.New("This forge cannot resolve review threads.")

// MaxThreadID bounds the id of a review thread a write names, far beyond the
// ids forges hand out.
const MaxThreadID = 255

// ThreadIDRefusal says why id is no id of a review thread a write names, ""
// when it can be one.
func ThreadIDRefusal(id string) string {
	switch {
	case id == "":
		return "Name the review thread by its id."
	case !utf8.ValidString(id) || utf8.RuneCountInString(id) > MaxThreadID:
		return fmt.Sprintf("The id of a review thread is text of at most %d characters.", MaxThreadID)
	case strings.ContainsFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }):
		return "The id of a review thread holds no spaces, no control characters and no invisible format characters."
	}
	return ""
}

// ThreadWrite is a write on a review thread of a branch's change request, one
// on its diff or a general one, the thread by the forge's id: the note to
// answer it with, "" for none, and whether to resolve it.
type ThreadWrite struct {
	Thread  string
	Body    string
	Resolve bool
}

// ThreadTarget is the thread a write goes to as the forge has it now, with
// the change request of the branch it stands on.
type ThreadTarget struct {
	Change Change
	Thread Thread
}

// ThreadRefusal is a write on a review thread refused before the forge was
// asked to write anything.
type ThreadRefusal struct {
	Reason string
}

func (e *ThreadRefusal) Error() string {
	return e.Reason
}

// ThreadTarget reads w's branch from the forge afresh, its change request and
// the review threads of it, and answers the thread t names, on the diff or a
// general one. A branch without a change request, a thread it does not have,
// a note the forge keeps alone, and a resolve of a thread the forge does not
// resolve or that stands resolved with nothing to answer is refused. A
// refusal does not name the thread by the forge's id, which says nobody
// anything: the caller knows which one it named. One for a thread the change
// request lacks has the poller read the threads at once, the line comments
// still offer it.
func (s *Service) ThreadTarget(ctx context.Context, w Watch, t ThreadWrite) (ThreadTarget, error) {
	if _, _, _, err := s.threadWriters(w.Binding, t); err != nil {
		return ThreadTarget{}, err
	}
	return s.threadTarget(ctx, w, t)
}

// WriteThread answers the thread ThreadTarget finds for t with t's body where
// it has one, then resolves it where t asks and it is not resolved yet, and
// answers the note it posted, nil for none. The poller then reads the branch
// and its review threads at once, also after a refusal, which may come after
// the reply went.
func (s *Service) WriteThread(ctx context.Context, w Watch, t ThreadWrite) (ThreadTarget, *Note, error) {
	replier, resolver, token, err := s.threadWriters(w.Binding, t)
	if err != nil {
		return ThreadTarget{}, nil, err
	}
	target, err := s.threadTarget(ctx, w, t)
	if err != nil {
		return ThreadTarget{}, nil, err
	}
	defer s.poll.wroteNotes(w)
	var posted *Note
	if t.Body != "" {
		note, err := replier.Reply(ctx, s.client, w.Binding, token, target.Change, target.Thread, t.Body)
		if err != nil {
			return target, nil, err
		}
		posted = &note
	}
	if t.Resolve && !target.Thread.Resolved {
		if err := resolver.Resolve(ctx, s.client, w.Binding, token, target.Change, target.Thread); err != nil {
			if posted != nil {
				err = fmt.Errorf("The reply is posted, the thread is not resolved. %w", err)
			}
			return target, posted, err
		}
		target.Thread.Resolved = true
	}
	return target, posted, nil
}

func (s *Service) threadTarget(ctx context.Context, w Watch, t ThreadWrite) (ThreadTarget, error) {
	kind := w.Binding.Host.Kind
	refuse := func(format string, args ...any) (ThreadTarget, error) {
		return ThreadTarget{}, &ThreadRefusal{Reason: fmt.Sprintf(format, args...)}
	}
	status, err := s.Status(ctx, w, true)
	if err != nil {
		return ThreadTarget{}, err
	}
	if status.Change == nil {
		return refuse("%q has no %s.", w.Branch, kind.ChangeNoun)
	}
	change := *status.Change
	of := fmt.Sprintf("The %s %s", kind.ChangeNoun, kind.ChangeRef(change.Number))
	review, err := s.Threads(ctx, w.Binding, change.Number)
	if err != nil {
		return ThreadTarget{}, err
	}
	thread, found := findThread(slices.Concat(review.Diff, review.General), t.Thread)
	if !found {
		s.poll.wroteNotes(w)
		if review.Partial {
			return refuse("%s has no such review thread among the first ones %s lists of it, one read takes no more.", of, kind.Label)
		}
		return refuse("%s has no such review thread.", of)
	}
	reply, resolve := thread.Takes()
	if !reply {
		return refuse("%s holds this note of its conversation alone, %s keeps no thread of it to answer or resolve.", of, kind.Label)
	}
	if t.Resolve && !resolve {
		return refuse("%s does not resolve this thread.", kind.Label)
	}
	if t.Resolve && thread.Resolved && t.Body == "" {
		return refuse("The thread is resolved already.")
	}
	return ThreadTarget{Change: change, Thread: thread}, nil
}

func findThread(list []Thread, id string) (Thread, bool) {
	for _, t := range list {
		if t.ID == id {
			return t, true
		}
	}
	return Thread{}, false
}

// AnswersThreads reports whether the forge b binds to answers and resolves
// review threads, each apart.
func (s *Service) AnswersThreads(b Binding) (bool, bool) {
	p, ok := s.provider(b.Host.Kind.ID)
	if !ok {
		return false, false
	}
	_, replies := p.(Replier)
	_, resolves := p.(Resolver)
	return replies, resolves
}

// threadWriters answers what of the provider t's write needs, the token
// beside: a Replier where t answers the thread, a Resolver where it resolves
// it.
func (s *Service) threadWriters(b Binding, t ThreadWrite) (Replier, Resolver, string, error) {
	p, ok := s.provider(b.Host.Kind.ID)
	if !ok {
		return nil, nil, "", ErrNoReply
	}
	replier, replies := p.(Replier)
	resolver, resolves := p.(Resolver)
	switch {
	case t.Body != "" && !replies:
		return nil, nil, "", ErrNoReply
	case t.Resolve && !resolves:
		return nil, nil, "", ErrNoResolve
	}
	token, err := s.token(b.Host.ID)
	if err != nil {
		return nil, nil, "", err
	}
	if token == "" {
		return nil, nil, "", ErrNoToken
	}
	return replier, resolver, token, nil
}
