package git

import (
	"context"
	"errors"
	"strings"
	"time"
)

// checkoutTimeout caps a branch move, which may rewrite a good part of a
// large working copy before it answers; the short read timeout is not
// touched by this, a status stays quick or fails quick.
const checkoutTimeout = 2 * time.Minute

// Checkout switches the working copy to a branch. The name is a local branch,
// or through git's own guessing a remote one, and then the local tracking
// branch is created on the way; an ambiguous name, one git does not know, or
// local changes the switch would overwrite all come back in git's words, and
// the working copy stands as it was. There is deliberately no stash and no
// merge behind this.
func (r *Repo) Checkout(ctx context.Context, name string) error {
	if err := validBranchArg(name); err != nil {
		return err
	}
	w := *r
	w.timeout = checkoutTimeout
	_, err := w.run(ctx, []string{"switch", name}, nil)
	return err
}

// CreateBranch creates a branch at the current HEAD and switches to it.
// Whether the name is one git accepts is git's own question, and its answer
// comes back in its words.
func (r *Repo) CreateBranch(ctx context.Context, name string) error {
	if err := validBranchArg(name); err != nil {
		return err
	}
	w := *r
	w.timeout = checkoutTimeout
	_, err := w.run(ctx, []string{"switch", "-c", name}, nil)
	return err
}

// validBranchArg refuses what could read as an option; everything else about
// the name is git's call.
func validBranchArg(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("A branch name is required.")
	}
	if strings.HasPrefix(name, "-") {
		return errors.New("A branch name must not start with a dash.")
	}
	return nil
}
