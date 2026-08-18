package git

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Summary is the short answer a status line wants: where HEAD stands and how
// many paths the working copy changed, every untracked file counted for
// itself. It is one status call and no diff, because the line is drawn after
// every request a coder makes.
type Summary struct {
	Branch  BranchInfo
	Changed int
}

// Summary reads it for the whole working copy, whatever directory inside it
// was asked. ok is false outside a repository and when git did not answer.
func (r *Repo) Summary(ctx context.Context) (Summary, bool) {
	out, err := r.run(ctx, statusArgs, nil)
	if err != nil {
		return Summary{}, false
	}
	return Summary{Branch: parseBranch(out), Changed: len(parseStatus(out))}, true
}

// Stashes counts the repository's stash entries.
func (r *Repo) Stashes(ctx context.Context) (int, bool) {
	out, err := r.run(ctx, []string{"stash", "list"}, nil)
	if err != nil {
		return 0, false
	}
	text := strings.TrimRight(string(out), "\n")
	if text == "" {
		return 0, true
	}
	return strings.Count(text, "\n") + 1, true
}

// LastCommit answers when the commit HEAD points at was made, by its committer
// time, which is when it landed on this branch, an amend or a rebase included.
// A repository without a commit has none.
func (r *Repo) LastCommit(ctx context.Context) (time.Time, bool) {
	out, err := r.run(ctx, []string{"log", "-1", "--format=%ct"}, nil)
	if err != nil {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}
