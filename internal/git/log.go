package git

import (
	"context"
	"strconv"
	"strings"
)

// commitFormat is how every call here asks for a commit, so the history, the
// revision picker's search and a single resolved hash all answer the same
// shape. NUL between the fields, because a subject may hold anything but a
// newline, and the subject stays last for the same reason. The ref names ride
// along (%D), which is where the tags come from: asking git for the tags of a
// page of commits separately would be a second call and a second moment.
const commitFormat = "--format=%H%x00%an%x00%at%x00%D%x00%s"

// parseCommits reads what commitFormat wrote, one commit per line.
func parseCommits(out []byte) []Commit {
	commits := []Commit{}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 5)
		if len(parts) < 5 {
			continue
		}
		commit := Commit{SHA: parts[0], Short: shortSHA(parts[0]), Author: parts[1], Summary: parts[4], Tags: parseTags(parts[3])}
		commit.Time, _ = strconv.ParseInt(parts[2], 10, 64)
		commits = append(commits, commit)
	}
	return commits
}

// parseTags reads the tag names out of the ref names git decorated a commit
// with. That list also carries the branches and HEAD, which say where the
// repository stands right now and not what this commit is; a tag is the one
// name that belongs to the commit itself, so it is the only one kept.
func parseTags(decoration string) []string {
	tags := []string{}
	for _, ref := range strings.Split(decoration, ", ") {
		if name := strings.TrimPrefix(ref, "tag: "); name != ref && name != "" {
			tags = append(tags, name)
		}
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
}

// LogPage is one page of history: the commits, and whether older ones exist
// beyond it. A repository without a first commit answers an empty page, like
// every history question here.
type LogPage struct {
	Repo    bool     `json:"repo"`
	Commits []Commit `json:"commits"`
	More    bool     `json:"more"`
}

// Log lists the commits that touched a path, newest first, or the whole
// repository's when the path is empty. skip and limit page through it; one
// commit more than the limit is asked for, so More is an answer and not a
// guess. A directory without a repository answers empty and no error.
func (r *Repo) Log(ctx context.Context, file string, skip, limit int) (LogPage, error) {
	page := LogPage{Commits: []Commit{}}
	if skip < 0 {
		skip = 0
	}
	if limit < 1 {
		limit = 1
	}
	var paths []string
	if file != "" {
		clean, err := repoPath(file)
		if err != nil {
			return page, err
		}
		paths = []string{clean}
	}
	if _, ok := r.resolve(ctx); !ok {
		return page, nil
	}
	page.Repo = true
	if !r.hasCommit(ctx) {
		return page, nil
	}
	args := []string{
		"log",
		commitFormat,
		"--skip=" + strconv.Itoa(skip),
		"-n", strconv.Itoa(limit + 1),
	}
	out, err := r.run(ctx, args, paths)
	if err != nil {
		return page, err
	}
	page.Commits = parseCommits(out)
	if len(page.Commits) > limit {
		page.Commits = page.Commits[:limit]
		page.More = true
	}
	return page, nil
}
