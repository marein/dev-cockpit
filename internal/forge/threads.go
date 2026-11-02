package forge

import (
	"context"
	"errors"
	"time"
)

// Side is the side of a change request's diff a line stands on: the new one,
// the file as the source branch has it, or the old one, as the target has it.
type Side string

const (
	NewSide Side = "new"
	OldSide Side = "old"
)

// Note is one comment of a review thread: who wrote it, what, when, and its
// page on the forge. ID is the forge's own.
type Note struct {
	ID      string
	Author  string
	Body    string
	Created time.Time
	URL     string
}

// Thread is a review thread of a change request with its notes, oldest
// first, the first one's page standing for the thread. One on the diff names
// its file by Path and the Line it stands at on Side, 0 and no side for one
// on the whole file; Outdated says it was written on a version of the diff
// the change request has moved on from, its line one of that version. Commit
// is the commit whose version of the file holds that line, for a reader to
// take the line's text from; a forge that names none gives the text as
// Quote. A general thread names no path, and Single marks one the forge
// keeps as a note alone, no thread, which takes no reply. Resolvable says
// whether the forge resolves it, Resolved whether it is. ID is the forge's
// own.
type Thread struct {
	ID         string
	Path       string
	Line       int
	Side       Side
	Commit     string
	Quote      string
	Outdated   bool
	Single     bool
	Resolvable bool
	Resolved   bool
	Notes      []Note
}

// Takes says which writes the thread takes: a reply, which a note the forge
// keeps alone does not, and a resolve, which only a thread the forge
// resolves does. Every offer, hint and refusal of a write asks here.
func (t Thread) Takes() (reply, resolve bool) {
	return !t.Single, !t.Single && t.Resolvable
}

// Review is what was said on a change request, the threads on its diff and
// the general ones apart, each in the order the forge lists them. Partial
// says a list holds the first ones of more than one read takes.
type Review struct {
	Diff    []Thread
	General []Thread
	Partial bool
}

// ErrNoThreads answers a read of review threads on a forge whose provider
// cannot read them.
var ErrNoThreads = errors.New("This forge cannot read review threads.")

// Threads asks the forge b binds to for the review threads of the change
// request with that number.
func (s *Service) Threads(ctx context.Context, b Binding, number int) (Review, error) {
	p, ok := s.provider(b.Host.Kind.ID)
	if !ok {
		return Review{}, ErrNoThreads
	}
	reader, ok := p.(ThreadReader)
	if !ok {
		return Review{}, ErrNoThreads
	}
	token, err := s.token(b.Host.ID)
	if err != nil {
		return Review{}, err
	}
	if token == "" {
		return Review{}, ErrNoToken
	}
	return reader.Threads(ctx, s.client, b, token, number)
}
