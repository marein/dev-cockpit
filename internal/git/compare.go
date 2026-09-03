package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// compareTimeout caps one comparison of two revisions. The tree diff itself
// answers in milliseconds, the line counts read every changed blob, and a
// changeset of tens of thousands of files is an ordinary thing to ask about
// here, a vendored tree or a generated folder between two releases.
const compareTimeout = 30 * time.Second

// RevisionError says one of the two names a comparison was asked about is
// nothing the repository can resolve. It names the side so the panel can put
// the refusal next to the field it belongs to.
type RevisionError struct {
	Side string
	Rev  string
}

func (e *RevisionError) Error() string {
	return fmt.Sprintf("%q is not known to this repository.", e.Rev)
}

func (e *RevisionError) Unwrap() error { return ErrRevision }

// ErrNoSplit says the two revisions share no history at all, so the question
// "what changed since they split" has no point to start from.
var ErrNoSplit = errors.New("The two revisions share no history, so there is no point they split at.")

// Revision is one side of a comparison: the name somebody picked or typed,
// and the commit it resolves to right now. A branch name moves, the hash
// does not, which is why the file reads below go by the hash.
type Revision struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

// RevisionChange is one path that differs between two revisions. Status is
// git's own letter (A, M, D, R, T, C), From the path a rename came from, and
// the two numbers say how many lines came and went; Binary says git counted
// none because there are none to count.
type RevisionChange struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	From    string `json:"from,omitempty"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Binary  bool   `json:"binary,omitempty"`
}

// CompareRequest is what a comparison is asked with. Empty names take the
// repository's own suggestion (see DefaultCompare). Since picks the question:
// true is what To changed since it split from From, git's three dots, false
// is everything that differs between the two, git's two dots.
type CompareRequest struct {
	From  string
	To    string
	Since bool
}

// Comparison is what one round answers: both sides resolved, the commit the
// left side really is (From's own for a direct comparison, the merge base
// when the question is what happened since the split), and the files. A
// repository without commits, or a directory without a repository, answers
// Repo false and nothing else. Truncated says git's output cap cut the list,
// so the files are the head of a longer one.
type Comparison struct {
	Repo      bool             `json:"repo"`
	From      Revision         `json:"from"`
	To        Revision         `json:"to"`
	Base      string           `json:"base"`
	Since     bool             `json:"since"`
	Files     []RevisionChange `json:"files"`
	Truncated bool             `json:"truncated,omitempty"`
}

// Resolve answers the commit a name stands for, or ErrRevision. The name is
// somebody's typing and travels into a git argument, so it is gated like the
// file read's revision: a leading dash cannot become an option.
func (r *Repo) Resolve(ctx context.Context, rev string) (string, error) {
	rev = strings.TrimSpace(rev)
	if !revPattern.MatchString(rev) {
		return "", ErrRevision
	}
	out, err := r.run(ctx, []string{"rev-parse", "--verify", "--quiet", rev + "^{commit}"}, nil)
	if err != nil {
		return "", ErrRevision
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", ErrRevision
	}
	return sha, nil
}

// Compare lists what differs between two revisions, one entry per path,
// renames folded into one entry with their source, line counts alongside. The
// paths are relative to the directory asked, so a project below the
// repository root sees its own files and nothing outside them (--relative),
// which is also why a rename whose source lies outside reads as an addition:
// the project has no path for where it came from.
//
// The names are resolved first, each one on its own, so a refusal names the
// side it belongs to. Everything else is git's answer: the merge base for the
// three dot question, and two diff calls over the same pair, the statuses and
// the numbers, keyed by the same relative paths.
func (r *Repo) Compare(ctx context.Context, req CompareRequest) (Comparison, error) {
	out := Comparison{Since: req.Since, Files: []RevisionChange{}}
	if _, ok := r.resolve(ctx); !ok {
		return out, nil
	}
	if !r.hasCommit(ctx) {
		return out, nil
	}
	out.Repo = true
	from, to := strings.TrimSpace(req.From), strings.TrimSpace(req.To)
	if from == "" || to == "" {
		suggested := r.DefaultCompare(ctx)
		if from == "" {
			from = suggested.From
		}
		if to == "" {
			to = suggested.To
		}
	}
	fromSHA, err := r.Resolve(ctx, from)
	if err != nil {
		return out, &RevisionError{Side: "from", Rev: from}
	}
	toSHA, err := r.Resolve(ctx, to)
	if err != nil {
		return out, &RevisionError{Side: "to", Rev: to}
	}
	out.From = Revision{Name: from, SHA: fromSHA}
	out.To = Revision{Name: to, SHA: toSHA}
	out.Base = fromSHA
	if req.Since && fromSHA != toSHA {
		base, err := r.run(ctx, []string{"merge-base", fromSHA, toSHA}, nil)
		if err != nil {
			if errors.Is(err, ErrNoAnswer) {
				return out, err
			}
			return out, ErrNoSplit
		}
		out.Base = strings.TrimSpace(string(base))
		if out.Base == "" {
			return out, ErrNoSplit
		}
	}
	if out.Base == toSHA {
		return out, nil
	}
	w := *r
	w.timeout = compareTimeout
	names, err := w.run(ctx, []string{"diff", "--name-status", "-z", "-M", "--relative", out.Base, toSHA}, nil)
	if err != nil {
		return out, err
	}
	files, cut := parseNameStatus(names)
	out.Truncated = cut || len(names) >= MaxOutput
	numbers, err := w.run(ctx, []string{"diff", "--numstat", "-z", "-M", "--relative", out.Base, toSHA}, nil)
	if err != nil {
		return out, err
	}
	if len(numbers) >= MaxOutput {
		out.Truncated = true
	}
	counts := parseNumstat(numbers)
	for i := range files {
		if n, ok := counts[files[i].Path]; ok {
			files[i].Added, files[i].Removed, files[i].Binary = n.added, n.removed, n.binary
		}
	}
	out.Files = files
	return out, nil
}

// parseNameStatus reads `diff --name-status -z`: a status record, then the
// path, and for a rename or a copy the source first and the target after it.
// The score git appends to R and C is dropped, the letter is what a row
// shows. The second value says the output ended inside a record, which is
// what a cut answer looks like; the half record is dropped rather than shown
// as a file nobody has.
func parseNameStatus(out []byte) ([]RevisionChange, bool) {
	files := []RevisionChange{}
	if len(out) == 0 {
		return files, false
	}
	cut := out[len(out)-1] != 0
	records := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if cut {
		records = records[:len(records)-1]
	}
	for i := 0; i < len(records); {
		code := records[i]
		if code == "" {
			i++
			continue
		}
		status := code[:1]
		if status == "R" || status == "C" {
			if i+2 >= len(records) {
				return files, true
			}
			files = append(files, RevisionChange{Path: records[i+2], Status: status, From: records[i+1]})
			i += 3
			continue
		}
		if i+1 >= len(records) {
			return files, true
		}
		files = append(files, RevisionChange{Path: records[i+1], Status: status})
		i += 2
	}
	return files, cut
}

// ComparePreset is the pair a comparison opens with when nobody picked one.
type ComparePreset struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// DefaultCompare suggests the pair somebody most likely wants in two moves:
// To is where the repository stands, the branch by name or HEAD when
// detached, and From is the place that branch is measured against. On a
// branch that is not the main one that is the main branch itself (the
// remote's HEAD names it, else a local main or master), which asks what the
// branch brings. On the main branch, detached, or wherever there is no other
// branch to measure against, it is the newest tag before HEAD, which asks
// what changed since the last release, and without a tag the commit before
// HEAD. A repository with one commit compares HEAD with itself, which is
// honest and empty.
func (r *Repo) DefaultCompare(ctx context.Context) ComparePreset {
	to := "HEAD"
	branch := ""
	if out, err := r.run(ctx, []string{"symbolic-ref", "--short", "-q", "HEAD"}, nil); err == nil {
		branch = strings.TrimSpace(string(out))
	}
	if branch != "" {
		to = branch
	}
	if branch != "" {
		if main := r.mainBranch(ctx); main != "" && main != branch && strings.TrimPrefix(main, "origin/") != branch {
			return ComparePreset{From: main, To: to}
		}
	}
	if out, err := r.run(ctx, []string{"describe", "--tags", "--abbrev=0", "HEAD^"}, nil); err == nil {
		if tag := strings.TrimSpace(string(out)); tag != "" {
			return ComparePreset{From: tag, To: to}
		}
	}
	if _, err := r.run(ctx, []string{"rev-parse", "--verify", "--quiet", "HEAD~1^{commit}"}, nil); err == nil {
		return ComparePreset{From: to + "~1", To: to}
	}
	return ComparePreset{From: to, To: to}
}

// mainBranch names the branch the repository measures itself against: the
// local branch the remote's HEAD points at when that branch exists here, the
// remote tracking branch itself when it does not, else a local main or
// master. Empty when nothing of that exists.
func (r *Repo) mainBranch(ctx context.Context) string {
	if out, err := r.run(ctx, []string{"symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"}, nil); err == nil {
		remote := strings.TrimSpace(string(out))
		if remote != "" {
			local := strings.TrimPrefix(remote, "origin/")
			if _, err := r.run(ctx, []string{"rev-parse", "--verify", "--quiet", "refs/heads/" + local}, nil); err == nil {
				return local
			}
			return remote
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, err := r.run(ctx, []string{"rev-parse", "--verify", "--quiet", "refs/heads/" + name}, nil); err == nil {
			return name
		}
	}
	return ""
}
