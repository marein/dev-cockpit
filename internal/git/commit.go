package git

import (
	"context"
	"errors"
	"strings"
	"time"
)

// commitTimeout caps the commit. It may run hooks and a signer, which are
// programs of their own, so it gets more room than a read before the caller
// hears a timeout instead of a result.
const commitTimeout = 30 * time.Second

// CommitInfo is what the commit panel shows before anything is committed:
// where the commit would go, and what the last one said, which is what an
// amend starts from. A directory without a repository answers Repo false and
// nothing else, like Changes does.
type CommitInfo struct {
	Repo        bool   `json:"repo"`
	Branch      string `json:"branch"`
	HasCommit   bool   `json:"hasCommit"`
	LastMessage string `json:"lastMessage,omitempty"`
}

// CommitResult names the commit that was just made, in the words the log
// prints: the abbreviated hash and the subject line.
type CommitResult struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}

// CommitInfo reads what the panel needs. The branch is the symbolic name HEAD
// carries, which exists on an unborn branch too; a detached HEAD has none and
// answers its abbreviated commit instead.
func (r *Repo) CommitInfo(ctx context.Context) (CommitInfo, error) {
	info := CommitInfo{}
	if _, ok := r.resolve(ctx); !ok {
		return info, nil
	}
	info.Repo = true
	if out, err := r.run(ctx, []string{"symbolic-ref", "--short", "--quiet", "HEAD"}, nil); err == nil {
		info.Branch = strings.TrimSpace(string(out))
	} else if out, err := r.run(ctx, []string{"rev-parse", "--short", "HEAD"}, nil); err == nil {
		info.Branch = strings.TrimSpace(string(out))
	}
	info.HasCommit = r.hasCommit(ctx)
	if info.HasCommit {
		if out, err := r.run(ctx, []string{"log", "-1", "--format=%B"}, nil); err == nil {
			info.LastMessage = strings.TrimRight(string(out), "\n")
		}
	}
	return info, nil
}

// Commit records the picked paths as a commit, and it is the one write this
// package makes. It is a pathspec commit: the commit takes the working copy
// content of exactly these paths, and what is staged for any other path stays
// staged and stays out, so a coder that is halfway through preparing its own
// commit keeps its index. Three things have to travel along for that mode to
// mean what the panel showed: an untracked path needs an intent-to-add entry
// before a pathspec commit may take it, the source of a rename has to be in
// the pathspec or the commit would record the copy and keep the deletion
// pending, and both are found by asking status here rather than trusting the
// caller's list. Amend rewrites the tip instead of adding to it, and an amend
// with no paths at all rewrites only the message, the everyday typo fix.
//
// Every pathspec is built as :(top,literal) from the repository relative path:
// top so a rename source outside the project keeps addressable, literal so a
// name that looks like a glob stays a name.
func (r *Repo) Commit(ctx context.Context, message string, paths []string, amend bool) (CommitResult, error) {
	if strings.TrimSpace(message) == "" {
		return CommitResult{}, errors.New("A commit message is required.")
	}
	if len(paths) == 0 && !amend {
		return CommitResult{}, errors.New("Pick at least one change to commit.")
	}
	w := *r
	w.timeout = commitTimeout
	info, ok := w.resolve(ctx)
	if !ok {
		return CommitResult{}, errors.New("The project is not inside a git repository.")
	}

	// An amend with nothing picked rewrites the message and nothing else.
	// --only is what keeps a coder's staged work out of it: a bare --amend
	// would take the index along.
	if len(paths) == 0 {
		if _, err := w.run(ctx, []string{"commit", "--only", "--amend", "-m", message}, nil); err != nil {
			return CommitResult{}, err
		}
		return w.tipResult(ctx), nil
	}

	selected := map[string]bool{}
	for _, p := range paths {
		clean, err := repoPath(p)
		if err != nil {
			return CommitResult{}, err
		}
		selected[info.prefix+strings.TrimPrefix(clean, "./")] = true
	}
	covered := func(path string) bool {
		path = strings.TrimSuffix(path, "/")
		if selected[path] {
			return true
		}
		for dir := path; ; {
			i := strings.LastIndex(dir, "/")
			if i < 0 {
				return false
			}
			dir = dir[:i]
			if selected[dir] {
				return true
			}
		}
	}

	status, err := w.run(ctx, statusArgs, nil)
	if err != nil {
		return CommitResult{}, err
	}
	specs := make([]string, 0, len(selected))
	for path := range selected {
		specs = append(specs, ":(top,literal)"+path)
	}
	intent := []string{}
	for _, f := range parseStatus(status) {
		if !covered(f.Path) {
			continue
		}
		if f.Worktree == "?" {
			intent = append(intent, ":(top,literal)"+strings.TrimSuffix(f.Path, "/"))
		}
		if f.From != "" && !covered(f.From) {
			specs = append(specs, ":(top,literal)"+f.From)
		}
	}
	if len(intent) > 0 {
		if _, err := w.run(ctx, []string{"add", "--intent-to-add"}, intent); err != nil {
			return CommitResult{}, err
		}
	}

	args := []string{"commit", "-m", message}
	if amend {
		args = append(args, "--amend")
	}
	if _, err := w.run(ctx, args, specs); err != nil {
		// The intent entries were this call's own preparation, so a refused
		// commit takes them back and the index reads as it did before. Best
		// effort: what cannot be reset stays a visible change, never a loss.
		if len(intent) > 0 {
			_, _ = w.run(ctx, []string{"reset", "--quiet"}, intent)
		}
		return CommitResult{}, err
	}

	return w.tipResult(ctx), nil
}

// tipResult names the commit that was just made, best effort: the commit
// stands either way, only its stamp would be missing.
func (r *Repo) tipResult(ctx context.Context) CommitResult {
	result := CommitResult{}
	if out, err := r.run(ctx, []string{"log", "-1", "--format=%h%x00%s"}, nil); err == nil {
		if hash, subject, found := strings.Cut(strings.TrimRight(string(out), "\n"), "\x00"); found {
			result.Hash, result.Subject = hash, subject
		}
	}
	return result
}
