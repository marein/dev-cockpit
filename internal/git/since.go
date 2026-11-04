package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Since is the pair the editor's compare opens on: the place DefaultCompare
// measures HEAD against on the left, the working copy on the right, compared
// since the two split. From is that place resolved, To the branch HEAD stands
// on by its name, or HEAD when detached, with HEAD's commit, Base the commit
// the comparison starts at (the merge base, which is From's own commit where
// HEAD descends from it). Files is Compare's answer for the pair, committed
// and uncommitted changes and untracked files alike, Changed, Added and
// Removed count it, and Commits counts the commits HEAD has on top of the
// split. Like Compare it is the project's own view: paths are relative to the
// directory asked, and a project below the repository root counts only the
// commits that touched it. Truncated says git's output cap cut the files, so
// the counts are those of its head. A directory without a repository, or one
// without a commit, answers Repo false and nothing else.
type Since struct {
	Repo      bool
	From      Revision
	To        Revision
	Base      string
	Commits   int
	Changed   int
	Added     int
	Removed   int
	Files     []RevisionChange
	Truncated bool
}

// Since reads it. It is asked for a page that lists several projects at once,
// where a stalled repository answers late rather than holding up the rest, so
// the comparison runs under the default timeout as a whole, and every other
// call under its own.
func (r *Repo) Since(ctx context.Context) (Since, error) {
	out := Since{}
	info, ok := r.resolve(ctx)
	if !ok || !r.hasCommit(ctx) {
		return out, nil
	}
	out.Repo = true
	preset := r.DefaultCompare(ctx)
	out.To.Name = preset.To
	bounded, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	pair, err := r.Compare(bounded, CompareRequest{From: preset.From, Since: true, Worktree: true})
	out.From, out.Base = pair.From, pair.Base
	if err != nil {
		return out, err
	}
	if out.To.SHA, err = r.Resolve(ctx, "HEAD"); err != nil {
		return out, &RevisionError{Side: "to", Rev: preset.To}
	}
	out.Files, out.Truncated = pair.Files, pair.Truncated
	for _, f := range out.Files {
		out.Added += f.Added
		out.Removed += f.Removed
	}
	out.Changed = len(out.Files)
	if out.Base == out.To.SHA {
		return out, nil
	}
	count, err := r.run(ctx, []string{"rev-list", "--count", out.Base + ".." + out.To.SHA}, projectPaths(info))
	if err != nil {
		return out, err
	}
	if out.Commits, err = strconv.Atoi(strings.TrimSpace(string(count))); err != nil {
		return out, fmt.Errorf("git counted %q.", count)
	}
	return out, nil
}

// Between lists the commits to holds and base does not, newest first, at most
// limit of them, and like Since only those that touched the project where it
// stands below the repository root. Both sides are commits by their full
// hash, the answer of Since.
func (r *Repo) Between(ctx context.Context, base, to string, limit int) ([]Commit, error) {
	if !fullHash(base) || !fullHash(to) {
		return nil, fmt.Errorf("%q..%q are no commit hashes.", base, to)
	}
	info, ok := r.resolve(ctx)
	if !ok {
		return []Commit{}, nil
	}
	out, err := r.run(ctx, []string{"log", "--no-show-signature", commitFormat, "-n", strconv.Itoa(max(limit, 1)), base + ".." + to}, projectPaths(info))
	if err != nil {
		return nil, err
	}
	return parseCommits(out), nil
}

// projectPaths limits a history question to the project: nothing at the
// repository root, where every commit is the project's, and the directory
// itself below it, which a pathspec of "." names since git runs there.
func projectPaths(info repoInfo) []string {
	if info.prefix == "" {
		return nil
	}
	return []string{"."}
}
