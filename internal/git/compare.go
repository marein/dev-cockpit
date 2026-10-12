package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// git's own letter (A, M, D, R, T, C), or "?" for a file of the working copy
// that git does not track, From the path a rename came from, and the two
// numbers say how many lines came and went; Binary says git counted none
// because there are none to count.
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
// is everything that differs between the two, git's two dots. Worktree puts
// the working copy on the right instead of To, which is not read then: the
// files as they stand on the disk, untracked ones included, and the split is
// the one From has with HEAD, the commit the working copy stands on.
type CompareRequest struct {
	From     string
	To       string
	Since    bool
	Worktree bool
}

// Comparison is what one round answers: both sides resolved, the commit the
// left side really is (From's own for a direct comparison, the merge base
// when the question is what happened since the split), and the files. A
// repository without commits, or a directory without a repository, answers
// Repo false and nothing else. Truncated says git's output cap cut the list,
// so the files are the head of a longer one. Worktree says the right side is
// the working copy, which has no name and no commit, so To stays empty.
type Comparison struct {
	Repo      bool             `json:"repo"`
	From      Revision         `json:"from"`
	To        Revision         `json:"to"`
	Worktree  bool             `json:"worktree,omitempty"`
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
	if req.Worktree {
		to = "HEAD"
	}
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
	out.Base = fromSHA
	if req.Since && fromSHA != toSHA {
		if out.Base, err = r.mergeBase(ctx, fromSHA, toSHA); err != nil {
			return out, err
		}
	}
	w := *r
	w.timeout = compareTimeout
	if req.Worktree {
		out.Worktree = true
		return w.worktreeChanges(ctx, out)
	}
	out.To = Revision{Name: to, SHA: toSHA}
	if out.Base == toSHA {
		return out, nil
	}
	out.Files, out.Truncated, err = w.diffFiles(ctx, out.Base, toSHA)
	return out, err
}

// mergeBase answers the commit two commits split at, or ErrNoSplit when they
// share no history.
func (r *Repo) mergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := r.run(ctx, []string{"merge-base", a, b}, nil)
	if err != nil {
		if errors.Is(err, ErrNoAnswer) {
			return "", err
		}
		return "", ErrNoSplit
	}
	base := strings.TrimSpace(string(out))
	if base == "" {
		return "", ErrNoSplit
	}
	return base, nil
}

// SplitPoint answers where HEAD split off from rev, the merge base of the
// two, which is where a merge request measures a branch from, and the commit
// rev itself stands for. For an ancestor of HEAD the two are the same commit.
// The name is gated like every revision; a repository whose HEAD has no
// commit yet has nothing that split off, so rev is its own answer there.
func (r *Repo) SplitPoint(ctx context.Context, rev string) (base, at string, err error) {
	at, err = r.Resolve(ctx, rev)
	if err != nil {
		return "", "", err
	}
	head, err := r.Resolve(ctx, "HEAD")
	if err != nil || head == at {
		return at, at, nil
	}
	base, err = r.mergeBase(ctx, at, head)
	return base, at, err
}

// diffFiles lists what differs from base to the other side, the working copy
// when no second revision is named: two diff calls over the same pair, the
// statuses and the numbers, keyed by the same relative paths. The second value
// says git's output cap cut the list.
func (r *Repo) diffFiles(ctx context.Context, revs ...string) ([]RevisionChange, bool, error) {
	names, err := r.run(ctx, append([]string{"diff", "--name-status", "-z", "-M", "--relative"}, revs...), nil)
	if err != nil {
		return nil, false, err
	}
	files, cut := parseNameStatus(names)
	truncated := cut || len(names) >= MaxOutput
	numbers, err := r.run(ctx, append([]string{"diff", "--numstat", "-z", "-M", "--relative"}, revs...), nil)
	if err != nil {
		return nil, false, err
	}
	if len(numbers) >= MaxOutput {
		truncated = true
	}
	counts := parseNumstat(numbers)
	for i := range files {
		if n, ok := counts[files[i].Path]; ok {
			files[i].Added, files[i].Removed, files[i].Binary = n.added, n.removed, n.binary
		}
	}
	return files, truncated, nil
}

// worktreeChanges fills the comparison of the base with the working copy. A
// diff against one revision reads the working tree, but only for the paths
// the index knows, so the untracked files come from their own listing, which
// is relative to the project like the diff, and keeps git's ignore rules. No
// diff counts their lines, so that is done here.
func (r *Repo) worktreeChanges(ctx context.Context, out Comparison) (Comparison, error) {
	files, truncated, err := r.diffFiles(ctx, out.Base)
	if err != nil {
		return out, err
	}
	others, err := r.run(ctx, []string{"ls-files", "-z", "--others", "--exclude-standard"}, nil)
	if err != nil {
		return out, err
	}
	paths := strings.Split(string(others), "\x00")
	// The listing ends with a NUL, so the last element is empty, unless the
	// cap cut it inside a name, which is then dropped rather than shown.
	if last := len(paths) - 1; paths[last] != "" {
		truncated = true
	}
	paths = paths[:len(paths)-1]
	if len(others) >= MaxOutput {
		truncated = true
	}
	listed := make(map[string]bool, len(files))
	for _, f := range files {
		listed[f.Path] = true
	}
	counting, cancel := context.WithTimeout(ctx, compareTimeout)
	defer cancel()
	for _, path := range paths {
		if path == "" || listed[path] {
			continue
		}
		n, err := r.countLines(counting, path)
		if err != nil {
			return out, err
		}
		files = append(files, RevisionChange{Path: path, Status: "?", Added: n.added, Binary: n.binary})
	}
	out.Files, out.Truncated = files, truncated
	return out, nil
}

// binaryProbe is how far into a file git looks for a NUL before it calls the
// file binary, which is the same question asked here.
const binaryProbe = 8000

// countLines answers what numstat says about a file git has never seen: all
// of its lines are added, the last one counts without its newline too. Only a
// regular file is read, so a symlink is never followed out of the working
// copy and a fifo never blocks the answer; anything else counts nothing.
func (r *Repo) countLines(ctx context.Context, rel string) (counted, error) {
	full := filepath.Join(r.dir, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() {
		return counted{}, nil
	}
	f, err := os.Open(full)
	if err != nil {
		return counted{}, nil
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	n := counted{}
	read := 0
	var last byte
	for {
		if err := ctx.Err(); err != nil {
			return counted{}, err
		}
		got, err := f.Read(buf)
		if got > 0 {
			chunk := buf[:got]
			if read < binaryProbe && bytes.IndexByte(chunk[:min(got, binaryProbe-read)], 0) >= 0 {
				return counted{binary: true}, nil
			}
			n.added += bytes.Count(chunk, []byte{'\n'})
			last = chunk[got-1]
			read += got
		}
		if err != nil {
			break
		}
	}
	if read > 0 && last != '\n' {
		n.added++
	}
	return n, nil
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
