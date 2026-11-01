package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrDetached says HEAD stands on no branch, so there is none to read.
var ErrDetached = errors.New("HEAD is detached, it stands on no branch.")

// Pushed is where a branch stands on a remote as this repository last saw
// it, by its fetches and pushes. Branch is the local name asked, Remote its
// name on the remote, the one its upstream there names, else the same. Head
// is the commit the remote tracking ref names, "" when there is none, and
// Local the local branch's commit, "" for a branch only the remote has.
type Pushed struct {
	Branch string
	Remote string
	Head   string
	Local  string
}

// PushedHead reads where branch stands on remote, the branch HEAD is on when
// it is "". It reaches no network, the remote tracking refs are the answer.
func (r *Repo) PushedHead(ctx context.Context, remote, branch string) (Pushed, error) {
	if branch == "" {
		out, err := r.run(ctx, []string{"symbolic-ref", "--quiet", "--short", "HEAD"}, nil)
		if errors.Is(err, ErrNoAnswer) {
			return Pushed{}, err
		}
		if branch = strings.TrimSpace(string(out)); err != nil || branch == "" {
			return Pushed{}, ErrDetached
		}
	}
	if err := validBranchArg(branch); err != nil {
		return Pushed{}, err
	}
	pushed := Pushed{Branch: branch, Remote: branch}
	tracking := "refs/remotes/" + remote + "/" + branch
	local, err := r.exactRef(ctx, "refs/heads/"+branch, "%(objectname)%00%(upstream:remotename)%00%(upstream:remoteref)%00%(upstream)")
	if err != nil {
		return Pushed{}, err
	}
	if fields := strings.Split(local, "\x00"); len(fields) == 4 {
		pushed.Local = fields[0]
		if name, ok := strings.CutPrefix(fields[2], "refs/heads/"); ok && fields[1] == remote && fields[3] != "" {
			pushed.Remote, tracking = name, fields[3]
		}
	}
	if pushed.Head, err = r.exactRef(ctx, tracking, "%(objectname)"); err != nil {
		return Pushed{}, err
	}
	return pushed, nil
}

// Apart counts the commits a holds that b lacks and the ones b holds that a
// lacks, both commits by their full hash.
func (r *Repo) Apart(ctx context.Context, a, b string) (int, int, error) {
	if !fullHash(a) || !fullHash(b) {
		return 0, 0, fmt.Errorf("%q or %q is no commit hash.", a, b)
	}
	out, err := r.run(ctx, []string{"rev-list", "--left-right", "--count", a + "..." + b}, nil)
	if err != nil {
		return 0, 0, err
	}
	counts := strings.Fields(string(out))
	if len(counts) != 2 {
		return 0, 0, fmt.Errorf("git counted %q.", out)
	}
	ahead, err := strconv.Atoi(counts[0])
	if err != nil {
		return 0, 0, err
	}
	behind, err := strconv.Atoi(counts[1])
	return ahead, behind, err
}

// exactRef answers the format of the one ref named, "" when there is none.
// for-each-ref takes a name as a pattern that also matches the refs below
// it, so the match is made here.
func (r *Repo) exactRef(ctx context.Context, ref, format string) (string, error) {
	out, err := r.run(ctx, []string{"for-each-ref", "--format=%(refname)%00" + format, ref}, nil)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, ref+"\x00"); ok {
			return rest, nil
		}
	}
	return "", nil
}
