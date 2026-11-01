package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// commitFormat is how every call here asks for a commit, so the history, the
// revision picker's search and a single resolved hash all answer the same
// shape. NUL between the fields, because a subject may hold anything but a
// newline, and the subject stays last for the same reason. The ref names ride
// along (%D), which is where the tags come from: asking git for the tags of a
// page of commits separately would be a second call and a second moment. The
// parents (%P) are the commit's own, also in a history of one file, which
// rewrites no parent without --parents, so the first one is what the commit
// changed against.
const commitFormat = "--format=%H%x00%P%x00%an%x00%at%x00%D%x00%s"

// parseCommits reads what commitFormat wrote, one commit per line.
func parseCommits(out []byte) []Commit {
	commits := []Commit{}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 6)
		if len(parts) < 6 {
			continue
		}
		commit := Commit{SHA: parts[0], Short: shortSHA(parts[0]), Author: parts[2], Summary: parts[5], Tags: parseTags(parts[4])}
		if parents := strings.Fields(parts[1]); len(parents) > 0 {
			commit.Parent = parents[0]
		}
		commit.Time, _ = strconv.ParseInt(parts[3], 10, 64)
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
		"--no-show-signature",
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

// Message is what a commit says: its subject, the first line of its message,
// and the body below it.
type Message struct {
	Subject string
	Body    string
}

// Carried answers what the commits say that head holds and the branch target
// of remote does not, as this repository last saw that branch, oldest first,
// and how many they are: at most limit of them, the oldest ones, each by its
// subject and a lone one with its body too. It reports whether it knew
// target: without a remote tracking ref of it nothing tells where head left
// it, and head alone answers. head is a commit by its full hash.
func (r *Repo) Carried(ctx context.Context, remote, target, head string, limit int) ([]Message, int, bool, error) {
	if !fullHash(head) {
		return nil, 0, false, fmt.Errorf("%q is no commit hash.", head)
	}
	base, err := r.exactRef(ctx, "refs/remotes/"+remote+"/"+target, "%(objectname)")
	if err != nil {
		return nil, 0, false, err
	}
	revs, count := []string{head}, 1
	if base != "" {
		revs = append(revs, "^"+base)
		out, err := r.run(ctx, append([]string{"rev-list", "--count"}, revs...), nil)
		if err != nil {
			return nil, 0, false, err
		}
		if count, err = strconv.Atoi(strings.TrimSpace(string(out))); err != nil {
			return nil, 0, false, fmt.Errorf("git counted %q.", out)
		}
	}
	if count == 0 {
		return nil, 0, true, nil
	}
	format := "--format=%s"
	if count == 1 {
		format = "--format=%s%n%b"
	}
	read := min(count, max(limit, 1))
	args := append([]string{"log", "--no-show-signature", "-z", "--reverse", format, "--skip=" + strconv.Itoa(count-read), "-n", strconv.Itoa(read)}, revs...)
	out, err := r.run(ctx, args, nil)
	if err != nil {
		return nil, 0, false, err
	}
	var messages []Message
	for _, record := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		subject, body, _ := strings.Cut(record, "\n")
		messages = append(messages, Message{Subject: subject, Body: strings.TrimSpace(body)})
	}
	return messages, count, base != "", nil
}

// fullHash reports whether s is a commit's full hash, SHA-1 or SHA-256.
func fullHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}
