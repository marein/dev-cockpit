package git

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Commit is one commit as the editor shows it: enough to say who wrote a line
// and what for, and nothing more. Pending marks the commit that does not exist
// yet, which is what blame answers for a line that is only in the working copy.
type Commit struct {
	SHA     string `json:"sha"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	Time    int64  `json:"time"`
	Summary string `json:"summary"`
	// Tags are the tag names pointing at this commit, in git's own order, and
	// nothing else the commit is decorated with: a branch says where the
	// repository stands, a tag says what this commit is.
	Tags    []string `json:"tags,omitempty"`
	Pending bool     `json:"pending,omitempty"`
	// Parent is the first parent's sha, empty for a root commit and for the
	// pending one: it is what the commit is shown against, and a commit
	// without one has nothing earlier to compare a file with.
	Parent string `json:"parent,omitempty"`
	// Path is where the file stood in this commit, relative to the project,
	// and PreviousPath where it stood in the version before it. blame follows
	// a rename, so both may differ from the path that was asked about, and a
	// revision read under the path of today finds nothing. Empty where git
	// named a path outside the project, which the project has no name for.
	Path         string `json:"path,omitempty"`
	PreviousPath string `json:"previousPath,omitempty"`
	// Newest marks the one commit of the answer that landed last, by commit
	// time, and only where the answer holds more than one: a file of a single
	// commit has no latest change to point out.
	Newest bool `json:"newest,omitempty"`
}

// Blame is who last touched each line of a file. The commits are listed once
// and Lines carries an index into that list per line, in order: a file of a few
// thousand lines usually comes from a handful of commits, and repeating the
// whole entry per line would be the same answer over and over.
type Blame struct {
	Repo    bool     `json:"repo"`
	Path    string   `json:"path"`
	Commits []Commit `json:"commits"`
	Lines   []int    `json:"lines"`
	// Large marks a file whose blame outgrew what one call keeps in memory
	// (MaxOutput). The lines are empty then, because half a blame is worse
	// than none: the head of the file would carry its commits and everything
	// past the cut would read like a part nobody ever touched.
	Large bool `json:"large,omitempty"`
}

// blameHeader is the line that opens every blame entry: the commit, the line it
// came from, the line it is now, and for the first entry of a group how many
// lines follow.
var blameHeader = regexp.MustCompile(`^([0-9a-f]{7,64}) \d+ \d+(?: \d+)?$`)

// Blame reads who last changed each line of the file on disk, so lines that
// are only in the working copy answer as pending, which is the honest answer
// while somebody is typing. A directory without a repository answers empty and
// no error, like every other call here.
func (r *Repo) Blame(ctx context.Context, file string) (Blame, error) {
	blame := Blame{Path: file, Commits: []Commit{}, Lines: []int{}}
	clean, err := repoPath(file)
	if err != nil {
		return blame, err
	}
	info, ok := r.resolve(ctx)
	if !ok {
		return blame, nil
	}
	blame.Repo = true
	out, err := r.run(ctx, []string{"blame", "--porcelain"}, []string{clean})
	if err != nil {
		// Three ordinary paths end here, and none is a failure: a repository
		// without a first commit, where blame has no ref to walk and every file
		// in it is unattributable; a file git has never heard of; and one it
		// knows that is not on the disk any more, a delete waiting to be
		// committed. All three have nothing to attribute, which is an answer;
		// reporting them puts a line in the log and a bad gateway on the page
		// for the most everyday thing in a working copy. The unborn case has to
		// be asked first: ls-files lists a staged file there, so the two checks
		// below would both pass and let the error through.
		if !r.hasCommit(ctx) || !r.tracks(ctx, clean) || !r.onDisk(clean) {
			return blame, nil
		}
		return blame, err
	}
	// The output cap truncates silently, and the porcelain format costs a
	// multiple of the file it describes, so a file well inside the edit limit
	// can still fill it. An answer that reached the cap is the head of a
	// larger one and says so instead of attributing what it has; a blame that
	// happens to end exactly there is called too large with it, which is the
	// safe way to be wrong.
	if len(out) >= MaxOutput {
		blame.Large = true
		return blame, nil
	}
	blame.Commits, blame.Lines = parseBlame(out)
	for i := range blame.Commits {
		blame.Commits[i].Path = projectPath(blame.Commits[i].Path, info.prefix)
		blame.Commits[i].PreviousPath = projectPath(blame.Commits[i].PreviousPath, info.prefix)
	}
	r.fillParents(ctx, blame.Commits)
	return blame, nil
}

// projectPath turns a path git names relative to the repository root into one
// relative to the project, empty for a path outside it. A path git had to
// quote comes back unquoted, and one that cannot be read reads as outside.
func projectPath(path, prefix string) string {
	if strings.HasPrefix(path, `"`) {
		unquoted, err := strconv.Unquote(path)
		if err != nil {
			return ""
		}
		path = unquoted
	}
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	return strings.TrimPrefix(path, prefix)
}

// fillParents writes each commit's first parent onto it, out of one rev-list
// over every commit of the answer: the porcelain format names no parent, and
// asking per commit would be a process per commit. The same list answers
// which commit is the newest, it comes newest first by commit time, which is
// when a change landed, where the author time is kept through a rebase. A
// rev-list that fails leaves the parents empty and marks nothing, which reads
// as nothing to compare against and never as an error, the blame itself is
// whole.
func (r *Repo) fillParents(ctx context.Context, commits []Commit) {
	args := []string{"rev-list", "--no-walk", "--parents"}
	at := map[string]int{}
	for i, commit := range commits {
		if commit.Pending {
			continue
		}
		at[commit.SHA] = i
		args = append(args, commit.SHA)
	}
	if len(at) == 0 {
		return
	}
	out, err := r.run(ctx, args, nil)
	if err != nil {
		return
	}
	first := true
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		i, ok := at[fields[0]]
		if !ok {
			continue
		}
		if first && len(at) > 1 {
			commits[i].Newest = true
		}
		first = false
		if len(fields) > 1 {
			commits[i].Parent = fields[1]
		}
	}
}

// hasCommit answers whether HEAD resolves to anything. A repository that was
// just initialised has no commit yet, and every history question about it has
// the same empty answer rather than an error.
func (r *Repo) hasCommit(ctx context.Context) bool {
	_, err := r.run(ctx, []string{"rev-parse", "--verify", "--quiet", "HEAD"}, nil)
	return err == nil
}

// tracks answers whether the repository knows a path at all.
func (r *Repo) tracks(ctx context.Context, clean string) bool {
	out, err := r.run(ctx, []string{"ls-files", "-z"}, []string{clean})
	return err == nil && len(out) > 0
}

// onDisk answers whether the path exists in the working copy. clean carries the
// "./" prefix the git calls use, which is relative to the directory they run
// in, so it is joined onto that same directory here.
func (r *Repo) onDisk(clean string) bool {
	_, err := os.Stat(filepath.Join(r.dir, filepath.FromSlash(strings.TrimPrefix(clean, "./"))))
	return err == nil
}

// parseBlame reads the porcelain format: an entry opens with its commit, is
// followed by the commit's details the first time that commit appears, and ends
// with the content of the line itself, which is the only line that starts with
// a tab.
func parseBlame(out []byte) ([]Commit, []int) {
	commits := []Commit{}
	lines := []int{}
	index := map[string]int{}
	current := -1
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		if strings.HasPrefix(line, "\t") {
			if current >= 0 {
				lines = append(lines, current)
			}
			continue
		}
		if m := blameHeader.FindStringSubmatch(line); m != nil {
			sha := m[1]
			at, ok := index[sha]
			if !ok {
				at = len(commits)
				index[sha] = at
				commits = append(commits, Commit{SHA: sha, Short: shortSHA(sha), Pending: isZeroSHA(sha)})
			}
			current = at
			continue
		}
		if current < 0 {
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "author":
			commits[current].Author = value
		case "author-time":
			commits[current].Time, _ = strconv.ParseInt(value, 10, 64)
		case "summary":
			commits[current].Summary = value
		case "filename":
			if commits[current].Path == "" {
				commits[current].Path = value
			}
		case "previous":
			if _, previous, found := strings.Cut(value, " "); found && commits[current].PreviousPath == "" {
				commits[current].PreviousPath = previous
			}
		}
	}
	return commits, lines
}

// shortSHA is the abbreviation git itself would print, kept at a fixed width so
// a gutter of them lines up.
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// isZeroSHA marks the commit blame uses for a line that is not committed at
// all: all zeroes, because there is nothing to point at yet.
func isZeroSHA(sha string) bool {
	return strings.Trim(sha, "0") == ""
}
