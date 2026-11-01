package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The bounds of a change request the cockpit opens. A title stays within
// the 255 characters GitLab takes, the mark of a draft included, a body
// within the 65536 GitHub takes.
const (
	MaxChangeTitle = 248
	MaxChangeBody  = 65536
)

// MaxChangeCommits is the most commits ChangeText needs: each subject but the
// last takes three characters of the list at least, so more of them outgrow
// MaxChangeBody, and the body's own cut stays the only one.
const MaxChangeCommits = MaxChangeBody/3 + 2

// MaxBranch bounds the name of a branch the cockpit asks a forge about.
const MaxBranch = 255

// BranchRefusal says why name is no branch a forge is asked about, "" when it
// can be one. It is the cockpit's own rule, stricter than git's, which takes
// any byte above 0x7F: every branch name the forge layer takes in, read from
// git, typed or named by a forge, passes it first, so none that could hide in
// a line or rewrite a terminal reaches an output. The refusal names the
// character, a name that reads like main shows nothing of it.
func BranchRefusal(name string) string {
	switch {
	case !utf8.ValidString(name):
		return "A branch name is valid UTF-8."
	case utf8.RuneCountInString(name) > MaxBranch:
		return fmt.Sprintf("A branch name has at most %d characters.", MaxBranch)
	case strings.HasPrefix(name, "-"):
		return "A branch name must not start with a dash."
	}
	if at, r, ok := hiddenRune(name); ok {
		return fmt.Sprintf("A branch name holds no spaces, no control characters and no invisible characters, its character %d is U+%04X.", at, r)
	}
	return ""
}

// hiddenRune answers the first rune of name that shows nothing of its own, a
// space, a control or format character, a filler or a variation selector, by
// its place counted from 1. The joiner, the variation selectors and the tags
// an emoji is made of show as the emoji, and are taken there: a joiner after
// a symbol, its skin tone or its selector and before a symbol, a selector
// after a symbol or a keycap's digit, and the tags of a flag up to their end.
func hiddenRune(name string) (int, rune, bool) {
	runes := []rune(name)
	at := func(i int) rune {
		if i < 0 || i >= len(runes) {
			return 0
		}
		return runes[i]
	}
	symbol := func(r rune) bool { return unicode.Is(unicode.So, r) }
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\u200D' && (symbol(at(i-1)) || at(i-1) == '\uFE0F' || at(i-1) >= '\U0001F3FB' && at(i-1) <= '\U0001F3FF') && symbol(at(i+1)):
			continue
		case (r == '\uFE0E' || r == '\uFE0F') && (symbol(at(i-1)) || strings.ContainsRune("0123456789#*", at(i-1)) && at(i+1) == '\u20E3'):
			continue
		case r == '\U0001F3F4':
			end := i + 1
			for at(end) >= '\U000E0020' && at(end) <= '\U000E007E' {
				end++
			}
			if end > i+1 && at(end) == '\U000E007F' {
				i = end
			}
			continue
		}
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point) || r == '\u2800' {
			return i + 1, r, true
		}
	}
	return 0, 0, false
}

// BranchOff answers a read or a write of a branch the forge features are off
// for: BranchRefusal refuses what names it, its name, its name on the forge
// or the branch its change request merges into.
type BranchOff struct {
	What   string
	Reason string
}

func (e *BranchOff) Error() string {
	return fmt.Sprintf("The forge features are off for this branch, since %s breaks the cockpit's rule: %s", e.What, lowerFirst(e.Reason))
}

// Refused answers why the forge features are off for w's branch, nil when
// they are on.
func (w Watch) Refused() error {
	if reason := BranchRefusal(w.Branch); reason != "" {
		return &BranchOff{What: "its name", Reason: reason}
	}
	if reason := BranchRefusal(w.Remote); reason != "" {
		return &BranchOff{What: "its name on origin", Reason: reason}
	}
	return nil
}

// targetRefused answers why the forge features are off for a branch whose
// change request c, as the forge answers it, merges into a branch
// BranchRefusal refuses, nil when c merges into one it takes or is none.
func targetRefused(k Kind, c *Change) error {
	if c == nil {
		return nil
	}
	if reason := BranchRefusal(c.Target); reason != "" {
		return &BranchOff{What: fmt.Sprintf("the branch its %s %s merges into", k.ChangeNoun, k.ChangeRef(c.Number)), Reason: reason}
	}
	return nil
}

func lowerFirst(s string) string {
	return strings.ToLower(s[:1]) + s[1:]
}

// ErrNoChange answers an open of a change request on a forge whose provider
// cannot open one.
var ErrNoChange = errors.New("This forge cannot open change requests.")

// NewChange is a change request to open: the branch it merges into, "" for
// the repository's default one, its title and body, and whether it opens as
// a draft.
type NewChange struct {
	Target string
	Title  string
	Body   string
	Draft  bool
}

// ChangeRefusal is an open of a change request refused before the forge was
// asked to open it.
type ChangeRefusal struct {
	Reason string
}

func (e *ChangeRefusal) Error() string {
	return e.Reason
}

// ChangeExists answers an open of a change request for a branch that has an
// open one already, which stands for it instead of a second one.
type ChangeExists struct {
	Kind   Kind
	Branch string
	Change Change
}

func (e *ChangeExists) Error() string {
	return fmt.Sprintf("%q has the open %s %s already: %s", e.Branch, e.Kind.ChangeNoun, e.Kind.ChangeRef(e.Change.Number), e.Change.URL)
}

// Commit is what one commit of a branch says: its subject line and the body
// below it.
type Commit struct {
	Subject string
	Body    string
}

// ChangeText is the title and the body a change request of commits, oldest
// first, is offered with: the subject and the body of the one commit, else
// the subject of the oldest over a list of every subject. Each is cut to what
// a change request takes.
func ChangeText(commits []Commit) (string, string) {
	switch len(commits) {
	case 0:
		return "", ""
	case 1:
		return cut(commits[0].Subject, MaxChangeTitle), cut(commits[0].Body, MaxChangeBody)
	}
	lines := make([]string, 0, len(commits))
	for _, c := range commits {
		lines = append(lines, "* "+c.Subject)
	}
	return cut(commits[0].Subject, MaxChangeTitle), cut(strings.Join(lines, "\n"), MaxChangeBody)
}

func cut(s string, limit int) string {
	return quotedCut(strings.TrimSpace(s), limit)
}

// ChangeBase reads w's branch and answers the branch a change request of it
// merges into unless it names another, the repository's default one. A
// branch not pushed is refused, one with an open change request answers
// ChangeExists. fresh always asks the forge, else what the poller read of the
// branch no longer than CacheFresh ago answers.
func (s *Service) ChangeBase(ctx context.Context, w Watch, fresh bool) (string, error) {
	opener, token, err := s.opener(w.Binding)
	if err != nil {
		return "", err
	}
	status, err := s.changeOpens(ctx, w, fresh)
	if err != nil {
		return "", err
	}
	if status.Default != "" {
		return status.Default, nil
	}
	return s.defaultBranch(ctx, opener, w.Binding, token)
}

// changeOpens reads w's branch and answers what the forge says of it where a
// change request of it can open: a branch not pushed is refused, one with an
// open change request answers ChangeExists.
func (s *Service) changeOpens(ctx context.Context, w Watch, fresh bool) (BranchStatus, error) {
	if w.Head == "" {
		return BranchStatus{}, &ChangeRefusal{Reason: fmt.Sprintf("%q is not pushed, push it first.", w.Branch)}
	}
	status, err := s.Status(ctx, w, fresh)
	if err != nil {
		return BranchStatus{}, err
	}
	if status.Change.Opened() {
		return BranchStatus{}, &ChangeExists{Kind: w.Binding.Host.Kind, Branch: w.Branch, Change: *status.Change}
	}
	return status, nil
}

// defaultBranch reads the default branch of b's repository. One the forge
// names that is no branch name is refused before anything is sent, a change
// request that names the branch it merges into opens all the same.
func (s *Service) defaultBranch(ctx context.Context, opener ChangeOpener, b Binding, token string) (string, error) {
	name, err := opener.DefaultBranch(ctx, s.client, b, token)
	if err != nil {
		return "", err
	}
	if reason := BranchRefusal(name); reason != "" {
		return "", &ChangeRefusal{Reason: fmt.Sprintf("The forge names a default branch of %s that breaks the cockpit's rule: %s Name the branch to merge into.", b.Address(), lowerFirst(reason))}
	}
	return name, nil
}

// ChangeTarget completes n for w's branch and answers it: into the
// repository's default branch unless n names another, which the default is
// not read for. The forge is read first, fresh as ChangeBase reads it: a
// branch not pushed is refused, one with an open change request answers
// ChangeExists, and so is a change request without a title or one that
// merges the branch into itself.
func (s *Service) ChangeTarget(ctx context.Context, w Watch, n NewChange, fresh bool) (NewChange, error) {
	noun := w.Binding.Host.Kind.ChangeNoun
	n.Title, n.Target = strings.TrimSpace(n.Title), strings.TrimSpace(n.Target)
	if n.Title == "" {
		return NewChange{}, &ChangeRefusal{Reason: fmt.Sprintf("A %s needs a title.", noun)}
	}
	if n.Target != "" {
		if _, err := s.changeOpens(ctx, w, fresh); err != nil {
			return NewChange{}, err
		}
	} else {
		base, err := s.ChangeBase(ctx, w, fresh)
		if err != nil {
			return NewChange{}, err
		}
		n.Target = base
	}
	if n.Target == w.Remote {
		return NewChange{}, &ChangeRefusal{Reason: fmt.Sprintf("A %s cannot merge %q into itself.", noun, n.Target)}
	}
	return n, nil
}

// OpenChange opens what ChangeTarget answers for n, the forge read afresh,
// and has the poller read the branch at once, also where the forge refused:
// the refusal may tell of a change request the poller has not read yet. The
// change request opened merges into the branch n named, whatever else the
// forge's answer names, which the poller's read of it judges.
func (s *Service) OpenChange(ctx context.Context, w Watch, n NewChange) (Change, error) {
	opener, token, err := s.opener(w.Binding)
	if err != nil {
		return Change{}, err
	}
	if n, err = s.ChangeTarget(ctx, w, n, true); err != nil {
		return Change{}, err
	}
	opened, err := opener.OpenChange(ctx, s.client, w.Binding, token, w.Remote, n)
	s.poll.wrote(w)
	if err != nil {
		return Change{}, err
	}
	opened.Target = n.Target
	return opened, nil
}

func (s *Service) opener(b Binding) (ChangeOpener, string, error) {
	opener, ok := s.changeOpener(b)
	if !ok {
		return nil, "", ErrNoChange
	}
	token, err := s.token(b.Host.ID)
	if err != nil {
		return nil, "", err
	}
	if token == "" {
		return nil, "", ErrNoToken
	}
	return opener, token, nil
}

func (s *Service) changeOpener(b Binding) (ChangeOpener, bool) {
	p, ok := s.provider(b.Host.Kind.ID)
	if !ok {
		return nil, false
	}
	opener, ok := p.(ChangeOpener)
	return opener, ok
}
