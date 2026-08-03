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
	Pending bool   `json:"pending,omitempty"`
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
	if _, ok := r.resolve(ctx); !ok {
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
	blame.Commits, blame.Lines = parseBlame(out)
	return blame, nil
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
