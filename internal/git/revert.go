package git

import (
	"context"
	"errors"
	"strings"
	"time"
)

// revertTimeout caps a revert, which may rewrite a directory's worth of the
// working copy before it answers, like a branch move does; the short read
// timeout stays what it is.
const revertTimeout = 2 * time.Minute

// Revert takes the working copy under one path back to HEAD, recursively for a
// directory, and it is the one deliberate discard this package offers. What
// HEAD has is restored, staged edits included, so one revert leaves the path
// clean: a modification goes back, a deleted file comes back, and a path
// without a state in HEAD, an untracked file or a staged addition, is deleted,
// which the caller's confirmation has to say before anything runs. Ignored
// files are not changes and stay untouched.
//
// The two sides are found by asking status here, never by trusting the caller:
// what HEAD knows goes through restore, what it does not goes through clean,
// and a side with nothing under it runs nothing, because restore refuses a
// pathspec that matches nothing it knows. The source of a rename joins the
// restore like it joins the commit, or reverting the target would delete the
// file and leave its old name pending as a deletion. Every pathspec is built
// :(top,literal) for the same two reasons as the commit's.
func (r *Repo) Revert(ctx context.Context, path string) error {
	clean, err := repoPath(path)
	if err != nil {
		return err
	}
	w := *r
	w.timeout = revertTimeout
	info, ok := w.resolve(ctx)
	if !ok {
		return errors.New("The project is not inside a git repository.")
	}
	target := info.prefix + strings.TrimPrefix(clean, "./")

	status, err := w.run(ctx, statusArgs, nil)
	if err != nil {
		return err
	}
	spec := ":(top,literal)" + target
	restore := []string{}
	remove := false
	for _, f := range parseStatus(status) {
		p := strings.TrimSuffix(f.Path, "/")
		if p != target && !strings.HasPrefix(f.Path, target+"/") {
			continue
		}
		if f.Worktree == "?" {
			remove = true
			continue
		}
		if len(restore) == 0 {
			restore = append(restore, spec)
		}
		// The rename's source may lie outside the reverted path; without it the
		// revert would delete the target and leave the old name a pending
		// deletion, half a rename taken back.
		if f.From != "" && f.From != target && !strings.HasPrefix(f.From, target+"/") {
			restore = append(restore, ":(top,literal)"+f.From)
		}
	}

	if len(restore) > 0 {
		// A repository without a commit has no state to restore to, and its
		// staged files are somebody's only copy; refusing is the one honest
		// answer, deleting them as "back to HEAD" is not.
		if !w.hasCommit(ctx) {
			return errors.New("The repository has no commit yet, there is no state to revert to.")
		}
		// restore also removes what is tracked but not in HEAD, a staged
		// addition or a rename target, so clean below only ever sees what git
		// never tracked.
		if _, err := w.run(ctx, []string{"restore", "--source=HEAD", "--staged", "--worktree"}, restore); err != nil {
			return err
		}
	}
	if remove {
		if _, err := w.run(ctx, []string{"clean", "-q", "-f", "-d"}, []string{spec}); err != nil {
			return err
		}
	}
	return nil
}
