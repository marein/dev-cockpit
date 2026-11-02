package forge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ErrNoPublish answers a publish of review comments on a forge whose
// provider cannot publish them.
var ErrNoPublish = errors.New("This forge cannot publish review comments.")

// Diff is the diff of a change request as the forge shows it: the commits it
// is made of, Head the one the source stands at, Base where source and target
// part and Start the target's, which a forge that places a note by the head
// alone leaves "", and each file it shows by its path on the new side.
// Partial says the files are the first ones of more than one read takes.
type Diff struct {
	Head    string
	Base    string
	Start   string
	Files   map[string]DiffFile
	Partial bool
}

// DiffFile is one file of a diff: its path on the old side, and the lines of
// the new side its hunks show by their line there, each with its line on the
// old side, 0 for a line the change adds.
type DiffFile struct {
	OldPath string
	Lines   map[int]int
}

// HunkLines are the lines of the new side the hunks of a unified diff show,
// keyed by their line there, each with its line on the old side, 0 for one
// the diff adds. Each hunk takes as many lines as its head counts, so an
// empty line where a context line lost its blank still counts as one.
func HunkLines(patch string) map[int]int {
	lines := map[int]int{}
	old, new, oldLeft, newLeft := 0, 0, 0, 0
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@") {
			old, oldLeft, new, newLeft = hunkHead(line)
			continue
		}
		if oldLeft <= 0 && newLeft <= 0 {
			continue
		}
		mark := byte(' ')
		if line != "" {
			mark = line[0]
		}
		switch mark {
		case '+':
			lines[new] = 0
			new++
			newLeft--
		case '-':
			old++
			oldLeft--
		case ' ':
			lines[new] = old
			old++
			new++
			oldLeft--
			newLeft--
		}
	}
	return lines
}

// hunkHead reads where a hunk starts on each side and how many lines it
// spans there, none for a head it cannot read.
func hunkHead(line string) (int, int, int, int) {
	fields := strings.Fields(line)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, 0, 0
	}
	old, oldCount, okOld := hunkRange(fields[1][1:])
	new, newCount, okNew := hunkRange(fields[2][1:])
	if !okOld || !okNew {
		return 0, 0, 0, 0
	}
	return old, oldCount, new, newCount
}

func hunkRange(s string) (int, int, bool) {
	start, count, counted := strings.Cut(s, ",")
	from, err := strconv.Atoi(start)
	if err != nil || from < 0 {
		return 0, 0, false
	}
	if !counted {
		return from, 1, true
	}
	n, err := strconv.Atoi(count)
	if err != nil || n < 0 {
		return 0, 0, false
	}
	return from, n, true
}

// Draft is a note to publish as a review thread of its own on a change
// request: on its file, by the path in the repository, at the line it has in
// the commit the source of the change request stands at, and its body. Shown
// is the line it is shown at where that is another, a line comment's own in
// the file that moved on since the push, which a sentence names it by.
// General marks a note on the change request as a whole, a general comment
// without a file and a line.
type Draft struct {
	Path    string
	Line    int
	Shown   int
	Body    string
	General bool
}

// Placed is a draft where the diff shows its line: the file's path on the old
// side and the line's there, 0 for a line the change adds. A general draft
// stands on no line.
type Placed struct {
	Draft
	OldPath string
	OldLine int
}

// Publication is what a publish posts to: the open change request of a
// branch, the diff the forge shows of it at the branch's pushed head, none
// for general drafts alone, and each draft where that diff shows it, in
// order.
type Publication struct {
	Change Change
	Diff   Diff
	Placed []Placed
}

// PublishRefusal is a publish refused before the forge was asked to post
// anything.
type PublishRefusal struct {
	Reason string
}

func (e *PublishRefusal) Error() string {
	return e.Reason
}

// maxNamedOutside bounds the drafts a refusal names of those outside a diff.
const maxNamedOutside = 3

// PublishTarget reads w's branch from the forge afresh, its open change
// request and, for a draft on a line, the diff of it, and answers where each
// of drafts stands on that diff. A branch not pushed, one without an open
// change request, and one whose change request the forge shows at another
// commit than w's pushed head is refused, so is every draft where one of them
// stands on no line the diff shows: a publish posts all of its drafts or
// none.
func (s *Service) PublishTarget(ctx context.Context, w Watch, drafts []Draft) (Publication, error) {
	publisher, token, err := s.publisher(w.Binding)
	if err != nil {
		return Publication{}, err
	}
	return s.publishTarget(ctx, publisher, token, w, drafts)
}

// Publish posts each draft PublishTarget places as a review thread of its own,
// a general one as a general comment, in order, and answers the notes the
// forge has of the ones it posted, with the refusal of the forge that ended it
// where one did. The poller then reads the branch and its review threads at
// once, also after a refusal, which may come after some were posted.
func (s *Service) Publish(ctx context.Context, w Watch, drafts []Draft) (Publication, []Note, error) {
	publisher, token, err := s.publisher(w.Binding)
	if err != nil {
		return Publication{}, nil, err
	}
	target, err := s.publishTarget(ctx, publisher, token, w, drafts)
	if err != nil {
		return Publication{}, nil, err
	}
	defer s.poll.wroteNotes(w)
	notes := make([]Note, 0, len(target.Placed))
	for _, p := range target.Placed {
		var note Note
		if p.General {
			note, err = publisher.PublishGeneral(ctx, s.client, w.Binding, token, target.Change, p.Body)
		} else {
			note, err = publisher.PublishNote(ctx, s.client, w.Binding, token, target.Change, target.Diff, p)
		}
		if err != nil {
			return target, notes, err
		}
		notes = append(notes, note)
	}
	return target, notes, nil
}

func (s *Service) publishTarget(ctx context.Context, publisher Publisher, token string, w Watch, drafts []Draft) (Publication, error) {
	kind := w.Binding.Host.Kind
	refuse := func(format string, args ...any) (Publication, error) {
		return Publication{}, &PublishRefusal{Reason: fmt.Sprintf(format, args...)}
	}
	if err := Unpushed(w); err != nil {
		return Publication{}, err
	}
	status, err := s.Status(ctx, w, true)
	if err != nil {
		return Publication{}, err
	}
	if !status.Change.Opened() {
		return refuse("%q has no open %s to publish comments on.", w.Branch, kind.ChangeNoun)
	}
	change := *status.Change
	var diff Diff
	if slices.ContainsFunc(drafts, func(d Draft) bool { return !d.General }) {
		if diff, err = publisher.ChangeDiff(ctx, s.client, w.Binding, token, change); err != nil {
			return Publication{}, err
		}
		if diff.Head != w.Head {
			return refuse("The %s %s stands at %s on %s, not at the pushed head %s of %q: a fetch or a push catches up.", kind.ChangeNoun, kind.ChangeRef(change.Number), ShortHead(diff.Head), kind.Label, ShortHead(w.Head), w.Branch)
		}
	}
	placed, outside := diff.place(drafts)
	if len(outside) > 0 {
		return Publication{}, &PublishRefusal{Reason: outsideDiff(kind, change, diff, outside, len(drafts))}
	}
	return Publication{Change: change, Diff: diff, Placed: placed}, nil
}

// Unpushed refuses a publish of w's branch where it was never pushed, nil
// where it was: a comment publishes from its line in the pushed head.
func Unpushed(w Watch) error {
	if w.Head == "" {
		return &PublishRefusal{Reason: fmt.Sprintf("%q is not pushed, it has no %s to publish comments on.", w.Branch, w.Binding.Host.Kind.ChangeNoun)}
	}
	return nil
}

// place answers where the diff shows each draft, a general one on no line,
// and the drafts on a line it does not show.
func (d Diff) place(drafts []Draft) ([]Placed, []Draft) {
	placed := make([]Placed, 0, len(drafts))
	var outside []Draft
	for _, draft := range drafts {
		if draft.General {
			placed = append(placed, Placed{Draft: draft})
			continue
		}
		file, ok := d.Files[draft.Path]
		old, shown := file.Lines[draft.Line]
		if !ok || !shown {
			outside = append(outside, draft)
			continue
		}
		placed = append(placed, Placed{Draft: draft, OldPath: file.OldPath, OldLine: old})
	}
	return placed, outside
}

// outsideDiff says which of all drafts stand on no line of the diff of change.
func outsideDiff(k Kind, change Change, d Diff, outside []Draft, all int) string {
	of := fmt.Sprintf("the diff of %s %s", k.ChangeNoun, k.ChangeRef(change.Number))
	var said string
	if all == 1 {
		said = fmt.Sprintf("%s stands on no line %s shows, nothing was published.", draftPlace(outside[0]), of)
	} else {
		named := make([]string, 0, maxNamedOutside+1)
		for _, draft := range outside[:min(len(outside), maxNamedOutside)] {
			named = append(named, draftPlace(draft))
		}
		if rest := len(outside) - len(named); rest > 0 {
			named = append(named, fmt.Sprintf("%d more", rest))
		}
		list := named[0]
		if len(named) > 1 {
			list = strings.Join(named[:len(named)-1], ", ") + " and " + named[len(named)-1]
		}
		verb := "stand"
		if len(outside) == 1 {
			verb = "stands"
		}
		said = fmt.Sprintf("%d of the %d comments %s on no line %s shows, nothing was published: %s.", len(outside), all, verb, of, list)
	}
	if d.Partial {
		said += fmt.Sprintf(" %s lists more files of it than one read takes, a file past them counts as outside.", k.Label)
	}
	return said
}

func draftPlace(d Draft) string {
	line := d.Line
	if d.Shown > 0 {
		line = d.Shown
	}
	return fmt.Sprintf("%q:%d", d.Path, line)
}

// Publishes reports whether the forge b binds to publishes review comments.
func (s *Service) Publishes(b Binding) bool {
	_, ok := s.notePublisher(b)
	return ok
}

func (s *Service) publisher(b Binding) (Publisher, string, error) {
	publisher, ok := s.notePublisher(b)
	if !ok {
		return nil, "", ErrNoPublish
	}
	token, err := s.token(b.Host.ID)
	if err != nil {
		return nil, "", err
	}
	if token == "" {
		return nil, "", ErrNoToken
	}
	return publisher, token, nil
}

func (s *Service) notePublisher(b Binding) (Publisher, bool) {
	p, ok := s.provider(b.Host.Kind.ID)
	if !ok {
		return nil, false
	}
	publisher, ok := p.(Publisher)
	return publisher, ok
}
