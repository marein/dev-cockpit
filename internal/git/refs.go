package git

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// Ref is one name the repository can be asked about: a local branch, a remote
// one, or a tag. Branch carries the local name a checkout of a remote branch
// would create, which is the remote branch's name without the remote in
// front. Head marks the branch HEAD is on right now.
type Ref struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Branch string `json:"branch,omitempty"`
	Head   bool   `json:"head,omitempty"`
}

// The kinds a picker may ask for. The first three are names and come out of
// for-each-ref; the fourth is the history and comes out of git log, which is
// why it answers commits and not refs.
const (
	KindBranch = "branch"
	KindRemote = "remote"
	KindTag    = "tag"
	KindCommit = "commit"
)

// refNamespaces maps the three name kinds to what git calls them, in the order
// they are offered in. Each one is asked for on its own and carries the limit
// by itself, because a shared count is a competition: a repository with a
// thousand tags would push the branches out of the branch picker, and that is
// the one list in which a name has to be reachable.
var refNamespaces = []struct {
	kind      string
	namespace string
}{
	{KindBranch, "refs/heads"},
	{KindRemote, "refs/remotes"},
	{KindTag, "refs/tags"},
}

// hashPrefix is what may be handed to rev-parse as a revision. The search text
// is somebody's typing and travels into a git argument, so the one place it is
// used as a revision is gated on hex: a hash prefix cannot then start with a
// dash and be read as an option, and a name like "master" does not resolve
// here and come back as a commit row beside its own branch row. Four is git's
// own shortest abbreviation.
var hashPrefix = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)

// maxRefSearch bounds the typed text. It is one line of an autocomplete, and
// what travels into a git argument is bounded before it goes.
const maxRefSearch = 200

// RefSearch is one round of a picker's autocomplete: the text somebody typed,
// which kinds to look through, and the cap per kind. An empty text is the
// picker as it opens and answers the recently moved names, never commits: a
// list of the newest commits is what the sheet's history is for, and the
// picker's job before anything is typed is to show where the repository
// stands.
type RefSearch struct {
	Text  string
	Kinds []string
	Limit int
}

// RefMatches is what one round answers. Names and commits stay two lists,
// because they are two things: a name is a place the repository keeps, a
// commit is a point in its history, and only one of them can be checked out.
type RefMatches struct {
	Refs    []Ref    `json:"refs"`
	Commits []Commit `json:"commits"`
}

func (s RefSearch) wants(kind string) bool {
	for _, k := range s.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Refs answers the names and commits one round of a picker's search found.
// Without a text it is the plain listing it always was, ordered by how
// recently each name was made or moved, which is what an autocomplete wants at
// the top. With one, git is asked with it and only the hits come back; the
// limit applies per kind either way. The remotes' HEAD pointers are symbolic
// and stay out, they name a branch that is already in the list. A directory
// without a repository answers empty and no error.
func (r *Repo) Refs(ctx context.Context, search RefSearch) (RefMatches, error) {
	limit := search.Limit
	if limit < 1 {
		limit = 1
	}
	text := strings.TrimSpace(search.Text)
	if len(text) > maxRefSearch {
		text = text[:maxRefSearch]
	}
	found := RefMatches{Refs: []Ref{}, Commits: []Commit{}}
	if _, ok := r.resolve(ctx); !ok {
		return found, nil
	}
	// Each kind answers for itself. One that cannot be read must not take the
	// others with it: the branches are the list a name has to be reachable in,
	// and losing them because a tag object is damaged is the worse answer. Only
	// a round in which none of the asked kinds answered is a failure.
	var failed error
	asked, answered := 0, 0
	for _, space := range refNamespaces {
		if !search.wants(space.kind) {
			continue
		}
		asked++
		refs, err := r.namespaceRefs(ctx, space.namespace, text, limit)
		if err != nil {
			failed = err
			continue
		}
		answered++
		found.Refs = append(found.Refs, refs...)
	}
	if search.wants(KindCommit) && text != "" {
		asked++
		commits, err := r.searchCommits(ctx, text, limit)
		if err != nil {
			failed = err
		} else {
			answered++
			found.Commits = commits
		}
	}
	if answered == 0 && failed != nil {
		return RefMatches{Refs: []Ref{}, Commits: []Commit{}}, failed
	}
	return found, nil
}

// namespaceRefs reads one namespace and answers the names that match.
//
// The match is made here and not by for-each-ref's own pattern, because that
// pattern is a wildmatch over the full ref name and its "*" does not cross a
// slash: "refs/heads/*alpha*" finds the branch "alpha" and never
// "feature/alpha", which is the branch somebody typing "alpha" is looking for.
// So git is asked for the namespace and the substring decides here, one round
// trip either way.
//
// The count is git's while nothing is typed, and ours while something is: with
// a text the cap has to apply to the matches, or a name below the newest five
// hundred could never be found, which is the whole reason the search moved off
// the client.
func (r *Repo) namespaceRefs(ctx context.Context, namespace, text string, limit int) ([]Ref, error) {
	args := []string{
		"for-each-ref",
		"--format=%(refname)%00%(refname:short)%00%(HEAD)%00%(symref)",
		// creatordate and not committerdate: an annotated tag is a tag object
		// and carries no committer date at all, so it would sort by nothing and
		// sink to the end whatever its age. creatordate answers for both, the
		// commit's date for a branch and a lightweight tag, the tagger's for an
		// annotated one.
		"--sort=-creatordate",
	}
	if text == "" {
		args = append(args, "--count="+strconv.Itoa(limit))
	}
	args = append(args, namespace)
	out, err := r.run(ctx, args, nil)
	if err != nil {
		return nil, err
	}
	refs := parseRefs(out)
	if text != "" {
		needle := strings.ToLower(text)
		kept := make([]Ref, 0, limit)
		for _, ref := range refs {
			if !strings.Contains(strings.ToLower(ref.Name), needle) {
				continue
			}
			kept = append(kept, ref)
			if len(kept) == limit {
				break
			}
		}
		refs = kept
	}
	return refs, nil
}

// searchCommits answers the commits the text names: the one a hash prefix
// resolves to, then the ones whose subject carries the text.
//
// The subject search is git's own (--grep), asked with --fixed-strings so a
// dot or a bracket somebody typed stays the character they typed instead of
// becoming a pattern, and case insensitively because an autocomplete is. The
// text rides in the attached "--grep=" form, so a leading dash is part of the
// value and never a second option. --all, because a commit worth diffing
// against may sit on a branch this working copy is not on.
func (r *Repo) searchCommits(ctx context.Context, text string, limit int) ([]Commit, error) {
	commits := []Commit{}
	if !r.hasCommit(ctx) {
		return commits, nil
	}
	seen := map[string]bool{}
	// A hash prefix is not a subject, so --grep would never find it. rev-parse
	// is what resolves one, and a prefix that names nothing is an exit code and
	// not a failure: the round goes on with the subject search.
	if hashPrefix.MatchString(text) {
		if out, err := r.run(ctx, []string{"rev-parse", "--verify", "--quiet", text + "^{commit}"}, nil); err == nil {
			if sha := strings.TrimSpace(string(out)); sha != "" {
				if one, err := r.commitByHash(ctx, sha); err == nil && one.SHA != "" {
					seen[one.SHA] = true
					commits = append(commits, one)
				}
			}
		}
	}
	out, err := r.run(ctx, []string{
		"log", "--all", commitFormat,
		"--regexp-ignore-case", "--fixed-strings", "--grep=" + text,
		"-n", strconv.Itoa(limit),
	}, nil)
	if err != nil {
		return nil, err
	}
	for _, commit := range parseCommits(out) {
		if seen[commit.SHA] {
			continue
		}
		seen[commit.SHA] = true
		commits = append(commits, commit)
		if len(commits) == limit {
			break
		}
	}
	return commits, nil
}

// commitByHash reads the one commit a resolved hash names, in the same shape
// the history answers.
func (r *Repo) commitByHash(ctx context.Context, sha string) (Commit, error) {
	out, err := r.run(ctx, []string{"log", "-n", "1", commitFormat, sha}, nil)
	if err != nil {
		return Commit{}, err
	}
	list := parseCommits(out)
	if len(list) == 0 {
		return Commit{}, nil
	}
	return list[0], nil
}

// parseRefs reads one namespace's worth of for-each-ref output.
func parseRefs(out []byte) []Ref {
	refs := []Ref{}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 4)
		if len(parts) < 4 || parts[3] != "" {
			continue
		}
		ref := Ref{Name: parts[1], Head: parts[2] == "*"}
		full := parts[0]
		switch {
		case strings.HasPrefix(full, "refs/heads/"):
			ref.Kind = KindBranch
		case strings.HasPrefix(full, "refs/remotes/"):
			ref.Kind = KindRemote
			// refs/remotes/<remote>/<branch>: the branch may itself contain
			// slashes, so only the three fixed segments come off.
			if seg := strings.SplitN(full, "/", 4); len(seg) == 4 {
				ref.Branch = seg[3]
			}
		case strings.HasPrefix(full, "refs/tags/"):
			ref.Kind = KindTag
		default:
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}
