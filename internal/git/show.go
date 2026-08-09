package git

import (
	"context"
	"errors"
	"path"
	"regexp"
	"strings"
)

// ErrRevision says the revision the caller named cannot be resolved, which is
// a wrong name and not a failing repository: the handler answers it as the
// caller's mistake, not as a bad gateway.
var ErrRevision = errors.New("The revision is not known to this repository.")

// revPattern keeps a revision usable as a bare argument: branch and tag
// names, hashes, and the operators a person types (HEAD~2, @{upstream}). A
// leading dash could read as an option and is out, like every character git
// itself refuses in a ref name. The underscore belongs in front as much as
// anywhere else: git takes "_wip" and the editor's own branch names are built
// from \w, so leaving it out of the first position refused a name this app
// creates itself. Whether the name exists is the rev-parse below, this only
// keeps an option out of an argument.
var revPattern = regexp.MustCompile(`^[A-Za-z0-9_@][A-Za-z0-9._/~^@{}-]*$`)

// FileAt returns a file's bytes at a revision, HEAD when rev is empty. The
// second value is false when the path simply does not exist there, which is
// what a new file looks like and no error at all. Any revision but HEAD is
// verified first: HEAD not resolving is the ordinary unborn repository, while
// a name the repository cannot resolve is the caller's mistake and answers
// ErrRevision instead of reading as "no file here".
func (r *Repo) FileAt(ctx context.Context, rev, file string) ([]byte, bool, error) {
	clean, err := repoPath(file)
	if err != nil {
		return nil, false, err
	}
	if rev == "" {
		rev = "HEAD"
	}
	if _, ok := r.resolve(ctx); !ok {
		return nil, false, nil
	}
	if rev != "HEAD" {
		if !revPattern.MatchString(rev) {
			return nil, false, ErrRevision
		}
		if _, err := r.run(ctx, []string{"rev-parse", "--verify", "--quiet", rev + "^{commit}"}, nil); err != nil {
			return nil, false, ErrRevision
		}
	}
	// The path always carries a leading "./", which git reads relative to the
	// directory the call runs in, so a project below the repository root asks
	// for its own files. A repository without a single commit has no HEAD to
	// resolve, and there the honest answer is the one below: the file is not in
	// it yet.
	spec := rev + ":" + clean
	// cat-file blob, not show: show prints a directory's listing as if it were
	// content, so a path that is a directory in the revision would come back as
	// a file whose text is that listing. cat-file refuses anything that is not
	// a blob.
	out, showErr := r.run(ctx, []string{"cat-file", "blob", spec}, nil)
	if showErr == nil {
		return out, true, nil
	}
	// The call failed because there is no file of that name in the revision, or
	// because something is actually wrong. Only the object lookup can tell those
	// apart, guessing from the message would break with the next git release.
	// Peeling to a blob is what makes a directory answer "no file here" rather
	// than a bad gateway.
	if _, err := r.run(ctx, []string{"rev-parse", "--verify", "--quiet", spec + "^{blob}"}, nil); err != nil {
		return nil, false, nil
	}
	return nil, false, showErr
}

// repoPath turns a path from the browser into one git may be handed: cleaned,
// relative, and with the leading "./" that makes git resolve it against the
// directory the call runs in, so a project below the repository root asks for
// its own files, and a name that starts with a dash cannot read as an option.
//
// It clamps an upward path instead of refusing it ("../../etc/passwd" becomes
// "./etc/passwd"), so it is not the guard against leaving the project. That
// guard is `filesystem.ResolveUnder`, which every handler runs before it gets
// here; a caller that skips it would quietly ask about a different file.
func repoPath(file string) (string, error) {
	clean := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(strings.TrimSpace(file), "\\", "/")), "/")
	if clean == "" || clean == "." {
		return "", errors.New("A file path is required.")
	}
	return "./" + clean, nil
}
