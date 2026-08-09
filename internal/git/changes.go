package git

import (
	"context"
	"strconv"
	"strings"
)

// WorktreeChange is a path the working copy changed, the status entry plus the
// two numbers a diff carries: how many lines came and how many went. It stays a
// status entry on purpose: the two porcelain codes carry what a status carries
// and nothing else does, a conflict for instance. Binary says git counted no
// lines because there are none to count.
type WorktreeChange struct {
	FileStatus
	Added   int  `json:"added"`
	Removed int  `json:"removed"`
	Binary  bool `json:"binary,omitempty"`
}

// Changes is what the working copy carries on top of HEAD, one entry per
// changed path, which is what feeds the marks in the editor's file tree. The
// branch rides along because it comes out of the same status call: one round,
// one answer, nothing to disagree about.
type Changes struct {
	Repo     bool             `json:"repo"`
	Branch   BranchInfo       `json:"branch"`
	Worktree []WorktreeChange `json:"worktree"`
}

// Changes lists what the working copy changed. A directory without a
// repository answers an empty list and no error, like Status does.
//
// This is the one read the whole git surface hangs on, so unlike the reads
// that only decorate a file it does not flatten "git could not be asked" into
// "no repository": that answer takes the branch out of the statusbar, the
// marks out of the tree and puts the clone where the repository's actions
// were, and a single stalled call must not do that to a repository that is
// there. A call that answered nothing travels as an error, and the client
// keeps what it had.
func (r *Repo) Changes(ctx context.Context) (Changes, error) {
	out := Changes{Worktree: []WorktreeChange{}}
	info, ok, err := r.resolveErr(ctx)
	if !ok {
		return out, err
	}
	out.Repo = true

	status, err := r.run(ctx, statusArgs, nil)
	if err != nil {
		return out, err
	}
	out.Branch = parseBranch(status)
	// The working copy against HEAD carries the numbers for everything git
	// tracks. An untracked file is in no diff at all, so it keeps zeroes and the
	// tooltip simply says nothing about its size.
	numbers := r.numstat(ctx, []string{"diff", "--numstat", "-M", "-z", "HEAD"})
	for _, file := range withinPrefix(parseStatus(status), info.prefix) {
		entry := WorktreeChange{FileStatus: file}
		// Both git calls report paths relative to the repository root, and the
		// status paths have just been cut back to the project, so the prefix
		// goes back on for the lookup. Keying it with the short path finds
		// nothing in a project below the root, and worse, finds the numbers of
		// a same named file at the root.
		if n, ok := numbers[info.prefix+file.Path]; ok {
			entry.Added, entry.Removed, entry.Binary = n.added, n.removed, n.binary
		}
		out.Worktree = append(out.Worktree, entry)
	}
	return out, nil
}

// counted is one line of numstat output.
type counted struct {
	added   int
	removed int
	binary  bool
}

// numstat runs a numstat diff and keys the counts by the path they belong to,
// which for a rename is the new one. The NUL separated format writes the two
// numbers and a tab, then either the path or, for a rename, nothing followed by
// the source and the target as records of their own. A binary file carries a
// dash instead of each number.
func (r *Repo) numstat(ctx context.Context, args []string) map[string]counted {
	out, err := r.run(ctx, args, nil)
	if err != nil {
		return map[string]counted{}
	}
	return parseNumstat(out)
}

// parseNumstat reads that output, see numstat.
func parseNumstat(out []byte) map[string]counted {
	counts := map[string]counted{}
	records := strings.Split(string(out), "\x00")
	for i := 0; i < len(records); i++ {
		record := records[i]
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		entry := counted{binary: fields[0] == "-" || fields[1] == "-"}
		entry.added, _ = strconv.Atoi(fields[0])
		entry.removed, _ = strconv.Atoi(fields[1])
		path := fields[2]
		if path == "" {
			// A rename: the source and the target follow as two records.
			if i+2 >= len(records) {
				return counts
			}
			i += 2
			path = records[i]
		}
		counts[path] = entry
	}
	return counts
}
