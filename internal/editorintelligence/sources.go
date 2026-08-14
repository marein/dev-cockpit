package editorintelligence

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/filesystem"
)

// SourceRoot is one directory outside the project a navigation answer may
// legitimately point into: the downloaded sources of a dependency, the
// standard library, a server's own stubs. Together they are the allowlist
// the read route stands on, and nothing outside them is readable through
// it, so the editor never becomes a way to read this machine.
//
// Path is the same path inside the container and outside, which is what
// the cache mount is arranged for and what the image roots are by nature.
// Image is empty for a directory on this host and otherwise names the
// image the tree lives in, which is then the only way to read it.
type SourceRoot struct {
	Path  string
	Image string
}

// Holds reports whether the file path lies inside the root. The path has
// to be absolute and already clean: a relative path, a "..", a doubled or
// a trailing separator is refused and never repaired, because a repaired
// path is a second spelling of a file and the check would then be about a
// path nobody asked for. The root itself is a directory and no file, so it
// is not in it either.
func (r SourceRoot) Holds(p string) bool {
	if r.Path == "" || p == "" {
		return false
	}
	if !path.IsAbs(p) || path.Clean(p) != p {
		return false
	}
	return strings.HasPrefix(p, strings.TrimSuffix(r.Path, "/")+"/")
}

// ErrNoSourceRoot is what a path outside every root answers, the read
// route's 400.
var ErrNoSourceRoot = errors.New("The file is outside the language servers' source directories.")

// FindSourceRoot answers the root holding the path, in the order the roots
// were given.
func FindSourceRoot(roots []SourceRoot, p string) (SourceRoot, bool) {
	for _, root := range roots {
		if root.Holds(p) {
			return root, true
		}
	}
	return SourceRoot{}, false
}

// readHostSource reads a file inside a root on this host. The root is the
// boundary the filesystem package resolves against, so a symlink pointing
// out of it is refused there rather than here, and the file goes through
// the same size and binary rules every editor buffer does.
func readHostSource(root SourceRoot, p string) (string, error) {
	rel, ok := strings.CutPrefix(p, strings.TrimSuffix(root.Path, "/")+"/")
	if !ok {
		return "", ErrNoSourceRoot
	}
	content, _, err := filesystem.ReadFileText(root.Path, rel)
	return content, err
}

// imageReadTimeout bounds one read out of an image. It is a container
// start plus a copy of a source file, nothing that may take minutes.
const imageReadTimeout = 30 * time.Second

// readImageSource reads a file that lies in the image and in no filesystem
// of this host: the standard library and the stubs are installed inside the
// server's image, and the server names them by the path they have in there.
//
// It runs a throwaway container of the same pinned image with cat as its
// whole command, no mount, no network and no name. Deliberately not an exec
// into the running server: a read must work when the server has idled out,
// must not depend on which container happens to be up, and must reach
// nothing a workspace mount would carry. What it can read is bounded by the
// image the cockpit built itself, and the path is checked against the
// allowlist before it ever becomes an argument.
func readImageSource(ctx context.Context, dockerPath string, env []string, image, p string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, imageReadTimeout)
	defer cancel()
	// --pull=never: the tag is this host's own build and exists nowhere
	// else, so a missing one is an answer and never a registry round trip
	// that ends in the same no a minute later.
	cmd := dockerCmd(ctx, dockerPath, env, "run", "--rm", "--pull=never", "--network", "none", "--entrypoint", "cat", image, p)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	// One byte over the limit is enough to know it is over it, and the
	// rest of a file that large is never read into this process at all.
	data, readErr := io.ReadAll(io.LimitReader(stdout, filesystem.MaxEditableBytes+1))
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	if readErr != nil {
		return "", readErr
	}
	if waitErr != nil {
		return "", fmt.Errorf("%s: %w: %s", image, waitErr, strings.TrimSpace(lastLine(stderr.String())))
	}
	if err := filesystem.CheckEditableText(data); err != nil {
		return "", err
	}
	return string(data), nil
}

// lastLine keeps an error message to the line that says what went wrong.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// dockerSourceRoots are the roots the Docker way of one profile answers
// for a project: the readable parts of the project's cache directory, then
// the trees inside the image.
func dockerSourceRoots(cacheRoot, project string, p *Profile) []SourceRoot {
	dir := cacheDir(cacheRoot, p.Server, project)
	roots := make([]SourceRoot, 0, len(p.container.CacheSources)+len(p.container.ImageRoots))
	for _, sub := range p.container.CacheSources {
		roots = append(roots, SourceRoot{Path: dir + "/" + sub})
	}
	for _, root := range p.container.ImageRoots {
		roots = append(roots, SourceRoot{Path: root, Image: imageRef(p)})
	}
	return roots
}

// lookDocker resolves the docker client for a read; a host without one can
// answer nothing out of an image.
func lookDocker() (string, error) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return "", errors.New("The docker client is not installed.")
	}
	return dockerPath, nil
}
