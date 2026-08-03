// Package git reads what a repository says about a project's working copy. It
// only ever reads: the editor shows git state, everything that writes a
// repository happens in a coder or on the command line.
//
// Every call goes through run, which is the one place the safety rules live,
// because a status poll runs next to a coder that may be committing right now:
//
//   - GIT_OPTIONAL_LOCKS=0 on every single call, so a read never takes the
//     index.lock away from that coder.
//   - no shell anywhere, the process is started with an argument list.
//   - "--" before any path, so a file named like a flag stays a file.
//   - core.quotepath=false and -z where git offers it, so paths arrive as
//     bytes instead of escapes.
//   - a timeout on every process and a cap on how much output is kept.
//
// A directory that is not a repository is not an error: the calls answer "no
// repo" and the editor keeps looking exactly like it does without git.
package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout caps one git process. Status on a normal repository answers in
// milliseconds; this is the ceiling for a repository on a slow or stalled disk,
// after which the caller gets an error instead of a hanging request.
const DefaultTimeout = 5 * time.Second

// maxOutput caps what one call keeps in memory. A repository with an
// implausible number of changed files truncates instead of filling the heap.
const maxOutput = 8 << 20

// Repo reads one repository, addressed by a directory inside it. The directory
// is usually the project root, which may sit below the repository root; the
// paths git reports are always relative to the repository root, so they are cut
// back to the project in the calls that report them.
type Repo struct {
	dir     string
	timeout time.Duration
}

// New returns a reader for the repository the given directory belongs to.
// Nothing runs yet, and the directory does not have to be a repository.
func New(dir string) *Repo {
	return &Repo{dir: dir, timeout: DefaultTimeout}
}

// repoInfo is the resolved repository around the directory: where its git
// directory is, and where the directory itself sits inside the work tree.
type repoInfo struct {
	gitDir string
	// prefix is the directory's path inside the work tree, empty at the root,
	// otherwise with a trailing slash, exactly as git reports it.
	prefix string
}

// resolve answers whether the directory is inside a repository and where. It is
// the one place that decides "no repo", every other call starts here.
func (r *Repo) resolve(ctx context.Context) (repoInfo, bool) {
	out, err := r.run(ctx, []string{"rev-parse", "--absolute-git-dir", "--show-prefix"}, nil)
	if err != nil {
		return repoInfo{}, false
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return repoInfo{}, false
	}
	info := repoInfo{gitDir: lines[0]}
	if len(lines) > 1 {
		info.prefix = lines[1]
	}
	return info, true
}

// run executes one git call in the repository directory. args carry the
// subcommand and its options, paths carry repository paths and always go behind
// the "--" separator. There is no shell on this path, and the environment the
// process gets differs from ours in one value only.
func (r *Repo) run(ctx context.Context, args []string, paths []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	full := make([]string, 0, len(args)+len(paths)+3)
	full = append(full, "-c", "core.quotepath=false")
	full = append(full, args...)
	if len(paths) > 0 {
		full = append(full, "--")
		full = append(full, paths...)
	}

	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.dir
	// os/exec keeps the last value of a duplicated key, so this wins over an
	// inherited one. Without it a status poll can take the index lock from a
	// coder that is committing at that moment.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out := &cappedBuffer{max: maxOutput}
	errOut := &cappedBuffer{max: 4096}
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(errOut.buf.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", args[0], message)
	}
	return out.buf.Bytes(), nil
}

// cappedBuffer keeps at most max bytes and swallows the rest. It keeps
// accepting writes, so a chatty git process finishes instead of blocking on a
// reader that stopped listening.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

// Fingerprint is what the poller compares between two rounds. It carries two
// parts, because they answer two different questions and one of them is
// expensive to answer wrongly.
//
// Base is the commit HEAD points at, the one thing the editor's diff is built
// against. It moves on a commit and on nothing cheaper.
//
// Worktree is what the working copy looks like, the status output itself. It
// moves on every keystroke that reaches the disk, this editor's own saves
// included.
//
// Both empty means the directory is no repository, which is a state like any
// other and stays that way between rounds. "git could not be asked" is not that
// state, and Fingerprint says so with its second return value instead of
// answering the zero value: a round that failed knows nothing, and treating
// nothing as a change publishes a move that never happened, twice, once on the
// failure and once when the next healthy round finds the old value again.
type Fingerprint struct {
	Base     string
	Worktree string
}

// Moved reports whether anything at all changed between two rounds.
func (f Fingerprint) Moved(other Fingerprint) bool {
	return f.Base != other.Base || f.Worktree != other.Worktree
}

// Fingerprint reads both parts in one pass. Splitting them is what lets a
// client tell "somebody saved a file" from "the commit I am comparing against
// moved": the first needs a fresh status and nothing else, the second is the
// only reason to fetch the revision again.
func (r *Repo) Fingerprint(ctx context.Context) (Fingerprint, bool) {
	if _, ok := r.resolve(ctx); !ok {
		return Fingerprint{}, true
	}
	status, err := r.run(ctx, statusArgs, nil)
	if err != nil {
		return Fingerprint{}, false
	}
	base := ""
	if head, err := r.run(ctx, []string{"rev-parse", "HEAD"}, nil); err == nil {
		base = strings.TrimSpace(string(head))
	}
	work := sha256.Sum256(status)
	return Fingerprint{
		Base:     base,
		Worktree: hex.EncodeToString(work[:]),
	}, true
}
