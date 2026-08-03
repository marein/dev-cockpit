package git

import (
	"context"
	"errors"
	"path"
	"strings"
)

// FileAt returns a file's bytes at HEAD, the one revision the editor's diff is
// built against. The second value is false when the path simply does not exist
// there, which is what a new file looks like and no error at all.
func (r *Repo) FileAt(ctx context.Context, file string) ([]byte, bool, error) {
	clean, err := repoPath(file)
	if err != nil {
		return nil, false, err
	}
	if _, ok := r.resolve(ctx); !ok {
		return nil, false, nil
	}
	// The path always carries a leading "./", which git reads relative to the
	// directory the call runs in, so a project below the repository root asks
	// for its own files. A repository without a single commit has no HEAD to
	// resolve, and there the honest answer is the one below: the file is not in
	// it yet.
	spec := "HEAD:" + clean
	// cat-file blob, not show: show prints a directory's listing as if it were
	// content, so a path that is a directory in HEAD would come back as a file
	// whose text is that listing. cat-file refuses anything that is not a blob.
	out, showErr := r.run(ctx, []string{"cat-file", "blob", spec}, nil)
	if showErr == nil {
		return out, true, nil
	}
	// The call failed because there is no file of that name in HEAD, or because
	// something is actually wrong. Only the object lookup can tell those apart,
	// guessing from the message would break with the next git release. Peeling
	// to a blob is what makes a directory answer "no file here" rather than a
	// bad gateway.
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
