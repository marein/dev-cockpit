package git

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

// worktreeTimeout caps adding a worktree. It fills a whole directory with the
// tree the branch stands on, which is the same order of work as a branch move
// and not a read, so it gets the same budget a checkout has.
const worktreeTimeout = 2 * time.Minute

// Worktree is one working copy of a repository: the main one and every linked
// worktree, in the order git lists them. Branch is the short name of what is
// checked out there and empty for a detached head or a bare repository, and a
// branch that stands in one working copy cannot be checked out in a second.
type Worktree struct {
	Path   string
	Branch string
}

// Worktrees answers every working copy this repository has, the main one
// first. It is the only place that knows which branch is currently taken
// where, which is what a form offering branches for a new worktree has to
// say before git refuses one. A directory that is no repository answers an
// empty list and no error, like every other reader here.
func (r *Repo) Worktrees(ctx context.Context) ([]Worktree, error) {
	if _, ok := r.resolve(ctx); !ok {
		return nil, nil
	}
	out, err := r.run(ctx, []string{"worktree", "list", "--porcelain"}, nil)
	if err != nil {
		return nil, err
	}
	return parseWorktrees(out), nil
}

// parseWorktrees reads the porcelain listing: one block per working copy,
// opened by its path, and a branch line only where a branch is checked out.
func parseWorktrees(out []byte) []Worktree {
	var list []Worktree
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			list = append(list, Worktree{Path: strings.TrimSpace(strings.TrimPrefix(line, "worktree "))})
		case strings.HasPrefix(line, "branch ") && len(list) > 0:
			name := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
			list[len(list)-1].Branch = strings.TrimPrefix(name, "refs/heads/")
		}
	}
	return list
}

// NewWorktree describes one worktree to add: the directory that becomes the
// working copy, the branch it stands on, and where that branch begins.
//
// Start empty means Branch exists already and is only checked out there;
// with a Start the branch is created at that point, which is also how a
// branch that so far only exists on a remote gets a local one that follows
// it. Dir must be an absolute path that is empty or not there yet, git
// refuses anything else and says so.
type NewWorktree struct {
	Dir    string
	Branch string
	Start  string
}

// AddWorktree adds a linked worktree to this repository. The registration is
// always written in the main repository, whether this reader was opened on it
// or on one of its worktrees, so a worktree of a worktree is a sibling and
// not a chain.
//
// Everything git decides stays git's: a branch that another working copy
// holds, a name it does not accept, a directory that is not empty. Those come
// back in its words, and nothing on disk is left changed by a refusal.
func (r *Repo) AddWorktree(ctx context.Context, w NewWorktree) error {
	if err := validBranchArg(w.Branch); err != nil {
		return err
	}
	if !filepath.IsAbs(w.Dir) {
		return errors.New("A worktree needs an absolute directory.")
	}
	if strings.HasPrefix(w.Start, "-") {
		return errors.New("A starting point must not start with a dash.")
	}
	args := []string{"worktree", "add"}
	if w.Start != "" {
		args = append(args, "-b", w.Branch)
	}
	// The separator guards the two operands behind it, the directory and the
	// revision, the same way it guards paths everywhere else here.
	args = append(args, "--", w.Dir)
	if w.Start != "" {
		args = append(args, w.Start)
	} else {
		args = append(args, w.Branch)
	}
	c := *r
	c.timeout = worktreeTimeout
	_, err := c.run(ctx, args, nil)
	return err
}
