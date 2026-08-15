package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// remoteTimeout caps a call that crosses the network: a big push on a slow
// line is minutes, not the seconds a status read gets, and the read timeout
// stays what it is. A call that would prompt for credentials has no terminal
// to prompt on and fails on its own long before this.
const remoteTimeout = 3 * time.Minute

// cloneTimeout caps the clone, which moves a whole repository before it
// answers and deserves more patience than a push.
const cloneTimeout = 10 * time.Minute

// quietFetchTimeout caps the fetch nobody asked for. An action somebody
// started may sit on a slow line for minutes, because somebody is waiting for
// it and watching a spinner; the fetch behind an opening sheet has neither, so
// a remote that is simply not reachable has to end in seconds. What it costs
// when it does is counts that stay as old as they were.
const quietFetchTimeout = 20 * time.Second

// Clone fills the directory from a repository: straight into it, never into
// a subdirectory, because the directory is the project. git refuses a
// directory that already holds anything, and that refusal comes back in
// git's words like every other one; authentication is whatever git on this
// host can do on its own, an SSH key or a credential helper, and a remote
// that wants more answers in git's words too, there is no prompt to give it.
func (r *Repo) Clone(ctx context.Context, url string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("A repository URL is required.")
	}
	w := *r
	w.timeout = cloneTimeout
	_, err := w.run(ctx, []string{"clone", "--", strings.TrimSpace(url), "."}, nil)
	return err
}

// Push sends the current branch to its upstream: where it goes, and whether
// it may, is the repository's own configuration, and what git refuses comes
// back in git's words. The one thing it answers itself is the branch that has
// no upstream yet, which is every branch the sheet's New branch just created:
// git refuses that one with the line about setting one, and asking somebody
// who has just tapped Push to go to a command line for it is a dead end, so
// the push sets it on the way. force is --force-with-lease and nothing
// stronger: it overwrites an upstream that moved away, and still refuses when
// the remote holds work this repository has never seen.
func (r *Repo) Push(ctx context.Context, force bool) error {
	w := *r
	w.timeout = remoteTimeout
	args := []string{"push"}
	if force {
		args = append(args, "--force-with-lease")
	}
	// The reading happens on the ordinary read budget and not on the push's,
	// it is a local call; the push itself keeps the minutes it needs.
	if remote, ok := r.upstreamRemote(ctx); ok {
		args = append(args, "-u", remote, "HEAD")
	}
	_, err := w.run(ctx, args, nil)
	return err
}

// upstreamRemote names the remote a push has to set the current branch's
// upstream on, and says whether there is one to name at all. The remote itself
// is pickRemote's answer, the same one a tag push uses.
//
// Whether an upstream stands is read out of the status, the same answer the
// statusbar shows, and not out of @{upstream}: a branch whose upstream is
// configured while the remote tracking ref is gone still pushes where it is
// configured to, and the status header says so where the ref lookup would
// not. Three states therefore go to git exactly as they always did, each for
// its own reason: a branch that has an upstream pushes there, a detached HEAD
// has no branch to configure anything on, and a status git never answered
// knows nothing, which is not the same as knowing there is no upstream. And
// where no remote can be named the push runs plain, so git's own refusal
// stands.
func (r *Repo) upstreamRemote(ctx context.Context) (string, bool) {
	status, err := r.run(ctx, statusArgs, nil)
	if err != nil {
		return "", false
	}
	if branch := parseBranch(status); branch.Detached || branch.Name == "" || branch.Upstream != "" {
		return "", false
	}
	return r.pickRemote(ctx)
}

// pickRemote answers the one remote a call may name on its own: the single
// configured one, or origin among several. Picking one of several strangers is
// a decision about where somebody's work goes, and no call here makes it.
func (r *Repo) pickRemote(ctx context.Context) (string, bool) {
	remotes := r.remotes(ctx)
	if len(remotes) == 1 {
		return remotes[0], true
	}
	if slices.Contains(remotes, "origin") {
		return "origin", true
	}
	return "", false
}

// Fetch brings what the remotes have up to date, which is what the ahead and
// behind counts are read against, and reports whether one ran at all. A
// repository without a remote has nothing to fetch and answers false and no
// error, that is a state and not a failure, and the caller that would tell
// every open editor about a move needs to know the difference.
func (r *Repo) Fetch(ctx context.Context) (bool, error) {
	if !r.hasRemote(ctx) {
		return false, nil
	}
	return r.fetch(ctx, remoteTimeout)
}

// fetch is the fetch itself, past the question whether there is a remote at
// all. It exists for the caller that has asked that already: a second `git
// remote` is a second process for an answer a moment old. The budget is the
// caller's, because the two fetches here are not the same kind of call: one
// somebody started and waits for, and one nobody asked for.
func (r *Repo) fetch(ctx context.Context, timeout time.Duration) (bool, error) {
	w := *r
	w.timeout = timeout
	if _, err := w.run(ctx, []string{"fetch"}, nil); err != nil {
		return false, err
	}
	return true, nil
}

// FetchIfStale fetches when the last fetch lies further back than maxAge, and
// reports whether one ran. The age is FETCH_HEAD's, which every fetch
// rewrites; a repository that never fetched has none and counts as stale. It
// is what the surfaces call that want fresh remotes without fetching on every
// glance: listing remote branches, opening the git sheet.
//
// This is the quiet one, so it runs on the short budget: nobody started it,
// nothing on the page says it is running, and a remote that does not answer
// must not keep it alive for minutes.
func (r *Repo) FetchIfStale(ctx context.Context, maxAge time.Duration) (bool, error) {
	info, ok := r.resolve(ctx)
	if !ok || !r.hasRemote(ctx) {
		return false, nil
	}
	if stat, err := os.Stat(filepath.Join(info.gitDir, "FETCH_HEAD")); err == nil && time.Since(stat.ModTime()) < maxAge {
		return false, nil
	}
	// The remotes were asked for above, so this goes straight to the fetch.
	return r.fetch(ctx, quietFetchTimeout)
}

// Pull brings the current branch up to its upstream, fast forward only: a
// branch that drifted apart needs a merge or a rebase, which stay with a
// coder or the command line, so git's refusal comes back in git's words and
// the working copy stands untouched.
func (r *Repo) Pull(ctx context.Context) error {
	w := *r
	w.timeout = remoteTimeout
	_, err := w.run(ctx, []string{"pull", "--ff-only"}, nil)
	return err
}

// remotes lists the configured remotes, in the order git names them. A call
// that failed answers none: nothing here is worth a second opinion, the
// callers either fetch nothing or leave the push to git.
func (r *Repo) remotes(ctx context.Context) []string {
	out, err := r.run(ctx, []string{"remote"}, nil)
	if err != nil {
		return nil
	}
	names := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// hasRemote answers whether any remote is configured at all.
func (r *Repo) hasRemote(ctx context.Context) bool {
	return len(r.remotes(ctx)) > 0
}
