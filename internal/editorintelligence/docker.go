package editorintelligence

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/filesystem"
)

//go:embed dockerfiles/gopls.Dockerfile
var goplsDockerfile string

//go:embed dockerfiles/intelephense.Dockerfile
var intelephenseDockerfile string

//go:embed dockerfiles/tsgo.Dockerfile
var typescriptDockerfile string

// entrypointDockerfile is the tail both build files share: the entrypoint
// runs the server next to a recursive workspace watcher, .git excluded,
// with a settle window so a checkout is one shove, and ends the container
// with the agreed exit code 64 on a relevant change (watcherRestartCode,
// the cockpit reads exactly that code as a restart wish). A refused watch
// (inotify limit) is loud on stderr and never a silently blind watcher.
// One copy here, or the exit code contract and the watch rules could
// drift apart between the languages. The flag lives under /tmp, because
// the server runs as the cockpit's own user and /run inside the images is
// root's: a flag it cannot write is a restart that never happens.
const entrypointDockerfile = `RUN printf '%s\n' \
  '#!/bin/sh' \
  'flag=/tmp/dev-cockpit-restart' \
  'rm -f "$flag"' \
  '# The async list would hand the server /dev/null as stdin; the real' \
  '# one survives on fd 3, the protocol pipe must reach the server.' \
  'exec 3<&0' \
  '"$@" <&3 &' \
  'srv=$!' \
  '(' \
  '  # Stdout is the LSP stream: the event line must never reach it.' \
  '  if ! inotifywait -r -q -e create -e delete -e move -e modify --exclude "/\.git(/|$)" . >/dev/null ; then' \
  '    echo "dev-cockpit: workspace watch failed (inotify limit?), live reindex is off" >&2' \
  '    exit 0' \
  '  fi' \
  '  while inotifywait -r -q -t 2 -e create -e delete -e move -e modify --exclude "/\.git(/|$)" . >/dev/null 2>&1; do :; done' \
  '  : > "$flag"' \
  '  kill "$srv" 2>/dev/null' \
  ') &' \
  'wait "$srv"' \
  'code=$?' \
  '[ -f "$flag" ] && exit 64' \
  'exit $code' \
  > /entrypoint.sh && chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
`

// imageBuildTimeout bounds one local image build; the build installs the
// server from its registry, so the network rides in it.
const imageBuildTimeout = 15 * time.Minute

// lspRootLabel marks a container or volume as belonging to one projects
// root. The root is the ownership boundary: a second serve process with
// another projects directory, the throwaway test instance next to the
// live one, must never touch what this one runs, so the boot sweep only
// reads names carrying its own root's label.
const lspRootLabel = "dev-cockpit.lsp-projects-root"

// container describes how a profile's server runs over the Docker option:
// the image the cockpit builds locally from the shipped build file, and the
// cache directory whose mount keeps the server's index and its downloads
// warm across container starts.
//
// That directory is a host bind and no named volume, and the mount puts it
// at the very path it has outside, because a module cache is not only a
// cache: it is where the sources of every dependency lie, and a definition
// in one of them is answered as the path the server sees. Only an equal
// path on both sides lets the cockpit read that file back and open it, and
// it is the same trick the workspace mount already uses. A bind wants a
// daemon on this machine, which the workspace mount wants anyway.
type container struct {
	Image      string
	Dockerfile string
	// CacheEnv is the environment that points the server at its cache
	// directory, built from the directory's host path. Nil for a server
	// that takes its storage through InitOptions instead.
	CacheEnv func(cacheDir string) []string
	// CacheSources are the subdirectories of the cache directory that hold
	// readable source, the module downloads for the Go way; empty for a
	// server whose cache holds nothing but its own index.
	CacheSources []string
	// ImageRoots are the directories inside the image a definition may
	// point into, the standard library and the server's own stubs. They
	// live in no filesystem of this host, so they are read through a
	// container of the same image, see dockerLauncher.ReadSource.
	ImageRoots []string
	// InitOptions builds the server's initializationOptions for one
	// container run: a server that stores its index outside the mount
	// would lose it with the container, so the storage is pointed into
	// the cache directory explicitly, which keeps a per-project cleanup
	// exact. Nil for servers without such options.
	InitOptions func(cacheDir string) map[string]any
	// DefaultConfig marks a server whose image writes a configuration file
	// for a project that brings none of its own, because without one it
	// sees only the file it was handed and whatever that file imports: a
	// usages list then answers a fraction and reads like the whole.
	//
	// Nothing of ours may appear in the working copy, so the file goes into
	// the directory above the project. That directory belongs to the
	// container only because such a profile has its project mounted alone
	// instead of the whole projects directory, and it is what the handshake
	// announces as the workspace (workspaceDir), because a server looks for
	// a configuration inside its workspace and nowhere else. Which file,
	// with what in it, and whether the project already brought one is the
	// image's business, see the profile's build file.
	DefaultConfig bool
}

// dockerCmd builds one docker CLI call carrying the launcher's extra
// environment, DOCKER_HOST for a configured daemon, so every call of the
// feature reaches the same daemon the availability gate read.
func dockerCmd(ctx context.Context, dockerPath string, env []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, dockerPath, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd
}

// buildMu serializes local image builds: two projects warming at the same
// moment must wait for one build, not race two.
var buildMu sync.Mutex

// imageRef is the profile's image with a tag that is a short stable hash
// of its build file's content: a release that changes the build file, a
// new entrypoint or a new server pin, misses its tag on existing hosts
// and builds on next start, while an unchanged file never rebuilds. The
// boot sweep removes tags of this scheme no shipped build file produces
// anymore, so the cockpit never has to understand a historical image.
func imageRef(p *Profile) string {
	sum := sha256.Sum256([]byte(p.container.Dockerfile))
	return p.container.Image + ":" + hex.EncodeToString(sum[:6])
}

// ensureImage makes the profile's image exist locally under the tag its
// current build file hashes to, building it from that file when the tag
// is missing. The build reads the file from stdin with an empty context,
// so nothing of the host rides into the image, and the image is never
// pulled prebuilt with a server inside: whoever builds holds the
// licenses.
func ensureImage(ctx context.Context, dockerPath string, env []string, p *Profile) error {
	buildMu.Lock()
	defer buildMu.Unlock()
	ref := imageRef(p)
	if dockerCmd(ctx, dockerPath, env, "image", "inspect", ref).Run() == nil {
		return nil
	}
	log.Printf("editor intelligence: building %s for %s", ref, p.ID)
	buildCtx, cancel := context.WithTimeout(ctx, imageBuildTimeout)
	defer cancel()
	build := dockerCmd(buildCtx, dockerPath, env, "build", "-t", ref, "-")
	build.Stdin = strings.NewReader(p.container.Dockerfile)
	out, err := build.CombinedOutput()
	if err != nil {
		tail := string(out)
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		return fmt.Errorf("build %s: %w: %s", ref, err, strings.TrimSpace(tail))
	}
	log.Printf("editor intelligence: built %s", ref)
	return nil
}

// CacheRoot is where the per project cache directories live, one place
// under the state directory this serve process owns. Resolved, because the
// path travels into a container as a mount and comes back out inside the
// file URIs the server answers.
func CacheRoot(stateDir string) string {
	return filepath.Join(filesystem.AbsDir(stateDir), "editor-lsp")
}

// cacheDir is one server's cache directory for one project. It wears the
// container's name, so what the naming rule keeps apart stays apart here
// too and the orphan sweep can read a project back out of it.
func cacheDir(cacheRoot, server, project string) string {
	return filepath.Join(cacheRoot, containerName(server, project))
}

// processUID and processGID are the identity the server containers run as,
// this process's own ids: what a server writes into the cache bind then
// belongs to the cockpit and comes off the disk without help. A root
// cockpit reads 0:0, exactly the previous behavior. Variables, because a
// test cannot create another user's files without being root, so faking
// the foreign cache the migration looks for means shifting the uid here.
var processUID, processGID = os.Getuid(), os.Getgid()

// ensureCacheDir makes the directory exist before the run binds it: docker
// would create a missing bind source itself, owned by root and with no say
// in the mode. The home subdirectory comes with it, HOME points there
// inside the container, so a server writing dotfiles lands in the mount
// instead of a home directory its uid does not have in the image.
func ensureCacheDir(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "home"), 0o755); err != nil {
		return fmt.Errorf("cache directory %s: %w", dir, err)
	}
	return nil
}

// dockerArgv is the container run of a profile's server: stdin carries the
// protocol, --init reaps, the projects directory and the cache directory
// mount at their own paths so file URIs match inside and outside, the
// workspace is the working directory, the name says in docker ps who runs
// what for whom, and the root label says which serve process owns it. The
// server runs as the cockpit's own user, never as the image's root: what
// it writes into the cache bind stays the cockpit's to remove, and HOME
// points into that bind, because the uid has no passwd entry in the image
// and -e wins over the home such an entry would name.
func dockerArgv(dockerPath, projectsRoot, cache, root, name string, p *Profile) []string {
	argv := []string{dockerPath, "run", "--rm", "-i", "--init",
		"--name", name,
		"--label", lspRootLabel + "=" + projectsRoot,
		"--user", fmt.Sprintf("%d:%d", processUID, processGID),
	}
	if p.container.DefaultConfig {
		// The project alone, so the directory above it belongs to the
		// container and the image may write its default configuration
		// there. It travels as the environment the image writes into, and
		// the handshake announces the very same directory, so the place the
		// file lands and the place the server looks for it cannot drift.
		// The tmpfs makes that directory writable for the cockpit's uid,
		// docker's default mode there is 1777, and docker mounts the
		// shorter path first, so the project bind below stays what it is.
		argv = append(argv, "-v", root+":"+root, "--tmpfs", workspaceDir(root), "-e", "DC_WORKSPACE="+workspaceDir(root))
	} else {
		argv = append(argv, "-v", projectsRoot+":"+projectsRoot)
	}
	argv = append(argv,
		"-v", cache+":"+cache,
		"-w", root,
		"-e", "HOME="+cache+"/home",
	)
	if p.container.CacheEnv != nil {
		for _, env := range p.container.CacheEnv(cache) {
			argv = append(argv, "-e", env)
		}
	}
	argv = append(argv, imageRef(p))
	return append(argv, p.Command...)
}

// workspaceDir is the directory a server with a default configuration works
// from: the one above the project. The handshake announces it and the image
// writes into it, and both take it from here, so the place the file lands
// and the place the server looks cannot drift apart.
func workspaceDir(root string) string {
	return filepath.Dir(root)
}

// containerPrefix is the naming scheme's per-server start; the name
// builder and the boot sweep both read it, so what one creates the other
// recognizes.
func containerPrefix(server string) string {
	return "dev-cockpit-" + server + "-"
}

// containerNameMax caps the full container name; 63 keeps every tool
// happy, hostname rules included.
const containerNameMax = 63

// projectSlug is the project part the container name and the per-project
// storage directory share. The rule is deliberately boring and
// deterministic, the stale removal, the boot sweep and a later per-project
// cleanup depend on the same project always wearing the same name: the
// project is sanitized to docker's charset ([a-zA-Z0-9_.-], every other
// rune becomes a dash), capped at max, and once sanitizing or capping
// rewrote anything, a short stable hash of the raw name joins the end, so
// no two projects ever share a slug and the cockpit owns the naming.
func projectSlug(project string, max int) string {
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			return r
		}
		return '-'
	}, project)
	if sanitized != "" && sanitized == project && len(sanitized) <= max {
		return sanitized
	}
	sum := sha256.Sum256([]byte(project))
	suffix := hex.EncodeToString(sum[:3])
	room := max - len(suffix) - 1
	if room < 0 {
		room = 0
	}
	if len(sanitized) > room {
		sanitized = sanitized[:room]
	}
	if sanitized == "" {
		return suffix
	}
	return sanitized + "-" + suffix
}

// containerName is the server container's name: the cockpit, the server,
// the project's slug, capped at containerNameMax as a whole.
func containerName(server, project string) string {
	prefix := containerPrefix(server)
	return prefix + projectSlug(project, containerNameMax-len(prefix))
}

// SweepStale starts the boot sweep in the background and gates the first
// server starts behind it. It removes every container of the LSP naming
// scheme labeled with this service's own projects root, and every cache
// directory whose project no longer exists on disk: at serve start none of
// them has a living owner, the previous process and its pipes are gone,
// while the lazy removal before a start only ever covers the same project
// and language starting again. The root label is the ownership boundary,
// so another live instance's servers on the same daemon are never touched.
// Call once, right after New.
func (s *Service) SweepStale() {
	done := make(chan struct{})
	s.mu.Lock()
	s.sweepDone = done
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(done)
		sweepStale(s.prepCtx, s.projectsRoot, s.cacheRoot, dockerHostEnv(hostOf(s.dockerHost)))
	}()
}

func hostOf(host func() string) string {
	if host == nil {
		return ""
	}
	return host()
}

func sweepStale(ctx context.Context, projectsRoot, cacheRoot string, env []string) {
	// The cache directories are this process's own files and are swept
	// whether or not there is a docker client to talk to; with one, the
	// removal can fall back to a container for a cache a server wrote as
	// root, see removeCacheDir. Deliberately ahead of the tag sweep below,
	// an outdated tag is still a removal candidate here.
	dockerPath, lookErr := exec.LookPath("docker")
	if lookErr != nil {
		dockerPath = ""
	}
	sweepCacheDirs(projectsRoot, cacheRoot, dockerPath, env)
	if lookErr != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ownLabel := "label=" + lspRootLabel + "=" + projectsRoot
	out, err := dockerCmd(ctx, dockerPath, env, "ps", "-a", "--filter", ownLabel, "--format", "{{.Names}}").Output()
	if err != nil {
		return
	}
	removed := 0
	for _, name := range strings.Fields(string(out)) {
		if lspSchemeName(name) {
			_ = dockerCmd(ctx, dockerPath, env, "rm", "-f", name).Run()
			removed++
		}
	}
	if removed > 0 {
		log.Printf("editor intelligence: swept %d stale server container(s)", removed)
	}
	sweepLegacyVolumes(ctx, dockerPath, env)
	// Image tags of the scheme that no shipped build file produces
	// anymore: a release changed the file, its hash tag moved on, the old
	// tag is dead weight nothing will ever start again. Images are shared
	// across instances, so a tag still referenced by any container, an
	// older binary's running server, stays.
	current := map[string]bool{}
	repos := map[string]bool{}
	for _, p := range profiles {
		current[imageRef(p)] = true
		repos[p.container.Image] = true
	}
	out, err = dockerCmd(ctx, dockerPath, env, "images", "--format", "{{.Repository}}:{{.Tag}}").Output()
	if err != nil {
		return
	}
	staleTags := 0
	for _, ref := range strings.Fields(string(out)) {
		repo, _, found := strings.Cut(ref, ":")
		if !found || !repos[repo] || current[ref] {
			continue
		}
		if used, err := dockerCmd(ctx, dockerPath, env, "ps", "-a", "--filter", "ancestor="+ref, "-q").Output(); err != nil || len(strings.TrimSpace(string(used))) > 0 {
			continue
		}
		_ = dockerCmd(ctx, dockerPath, env, "rmi", ref).Run()
		staleTags++
	}
	if staleTags > 0 {
		log.Printf("editor intelligence: swept %d outdated server image tag(s)", staleTags)
	}
}

// sweepCacheDirs takes the cache directories of projects that are no longer
// on the disk away: they wear the container's name, so a name no living
// project would produce is the leftover of a delete the cockpit did not
// see. Anything else in that directory is left alone, it is not ours.
// dockerPath and env carry the docker client for removeCacheDir's
// container fallback, dockerPath empty without one.
func sweepCacheDirs(projectsRoot, cacheRoot, dockerPath string, env []string) {
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		return
	}
	valid := map[string]bool{}
	if projects, err := os.ReadDir(projectsRoot); err == nil {
		for _, project := range projects {
			if project.IsDir() {
				for _, p := range profiles {
					valid[containerName(p.Server, project.Name())] = true
				}
			}
		}
	}
	orphans := 0
	for _, entry := range entries {
		if !entry.IsDir() || !lspSchemeName(entry.Name()) || valid[entry.Name()] {
			continue
		}
		if removeCacheDir(filepath.Join(cacheRoot, entry.Name()), dockerPath, env) == nil {
			orphans++
		}
	}
	if orphans > 0 {
		log.Printf("editor intelligence: swept %d orphaned server cache director(ies)", orphans)
	}
}

// sweepLegacyVolumes removes the named cache volumes the Docker option used
// before the caches became host directories. Every volume of the scheme is
// one, because nothing creates one anymore: a warm index no server will
// ever be started against again. This is the one sweep that reads names
// alone and not the root label, which the others are scoped by. It can:
// the volumes the label protected from another instance are as dead for
// that instance as for this one, and the ones created before the label
// existed carry none at all, which is exactly the pile this is here to
// clear. TODO(v2.0.0): drop once no install can still carry one.
func sweepLegacyVolumes(ctx context.Context, dockerPath string, env []string) {
	out, err := dockerCmd(ctx, dockerPath, env, "volume", "ls", "--format", "{{.Name}}").Output()
	if err != nil {
		return
	}
	removed := 0
	for _, name := range strings.Fields(string(out)) {
		if lspSchemeName(name) && dockerCmd(ctx, dockerPath, env, "volume", "rm", name).Run() == nil {
			removed++
		}
	}
	if removed > 0 {
		log.Printf("editor intelligence: removed %d server cache volume(s) of the previous scheme", removed)
	}
}

// removeAll is a seam for the fallback test alone: a local removal that
// fails cannot be staged on a plain filesystem, the suite may run as root
// and root deletes anything.
var removeAll = os.RemoveAll

// removeCacheDir takes one cache directory away. A module cache is written
// read only unless the toolchain was told otherwise (-modcacherw), and a
// directory without the write bit refuses to give its entries up, so the
// walk hands the modes back before the removal. Directories written by an
// older release are exactly that case. What the walk cannot repair is a
// cache a container wrote as root on a host where the cockpit is not:
// chmod on another user's files is forbidden. Whoever wrote those files
// can take them away, so the content is deleted through a container of a
// cockpit built image, root inside, and os.Remove clears the then empty
// top level, which ensureCacheDir made and the cockpit owns. An empty
// dockerPath means no docker client, the local error then stands as it
// always has.
func removeCacheDir(dir, dockerPath string, env []string) error {
	if err := removeAll(dir); err == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	err := removeAll(dir)
	if err == nil || dockerPath == "" {
		return err
	}
	p := cacheDirProfile(dir)
	if p == nil {
		return err
	}
	if cErr := emptyCacheDirInContainer(dockerPath, env, p, dir); cErr != nil {
		// No image at all is a state, not a failure: the first start
		// builds one, so the directory simply waits for a later sweep,
		// and the sentinel's own words are the whole message.
		if errors.Is(cErr, errImageNotBuilt) {
			log.Printf("editor intelligence: cache %s stays for a later sweep, %v", dir, cErr)
		} else {
			log.Printf("editor intelligence: emptying %s through a container failed: %v", dir, cErr)
		}
		return err
	}
	return os.Remove(dir)
}

// cacheDirProfile reads the profile back out of the directory's name,
// which wears the container's name and with it the server's prefix, so
// the matching image is known without any state. It takes a bare name as
// well, which is what lspSchemeName stands on.
func cacheDirProfile(dir string) *Profile {
	base := filepath.Base(dir)
	for _, p := range profiles {
		if strings.HasPrefix(base, containerPrefix(p.Server)) {
			return p
		}
	}
	return nil
}

// cacheRemoveTimeout bounds the container run that empties one cache
// directory. Minutes, not seconds: a module cache holds tens of thousands
// of files.
const cacheRemoveTimeout = 5 * time.Minute

// errImageNotBuilt says the fallback found no image at all to run: any
// cockpit built image does for the removal, find is in every one, so only
// a host that has never built one is out of candidates. An expected state
// the caller reports calmly, never a failure.
var errImageNotBuilt = errors.New("no cockpit built image exists on this host yet")

// removalImage picks the image the cache removal runs through. Any of the
// cockpit's own images does, find is in every one and root inside is the
// whole point, so the matching profile's current tag is only the first
// choice: a release that touched the build file moved the hash tags, at
// boot the new tag does not exist yet, and an orphan of a language this
// host never opens again must not wait forever. Preferred next is any tag
// of the profile's own repository, then another profile's image. The local
// images are listed once, the same call the tag sweep reads.
func removalImage(ctx context.Context, dockerPath string, env []string, p *Profile) (string, error) {
	out, err := dockerCmd(ctx, dockerPath, env, "images", "--format", "{{.Repository}}:{{.Tag}}").Output()
	if err != nil {
		return "", fmt.Errorf("list images: %w", err)
	}
	repos := map[string]bool{}
	for _, other := range profiles {
		repos[other.container.Image] = true
	}
	current := imageRef(p)
	ownRepo, other := "", ""
	for _, ref := range strings.Fields(string(out)) {
		repo, _, found := strings.Cut(ref, ":")
		if !found || !repos[repo] {
			continue
		}
		if ref == current {
			return ref, nil
		}
		if repo == p.container.Image {
			if ownRepo == "" {
				ownRepo = ref
			}
		} else if other == "" {
			other = ref
		}
	}
	if ownRepo != "" {
		return ownRepo, nil
	}
	if other != "" {
		return other, nil
	}
	return "", errImageNotBuilt
}

// emptyCacheDirInContainer deletes the directory's content through a
// throwaway container of a cockpit built image, the profile's own
// preferred (removalImage), started like the image source read:
// --pull=never, because the tags are this host's own builds and a missing
// one is an answer, never a registry round trip; no network; the
// entrypoint overridden, find below the mount is the whole command. The
// directory is the container's only mount, so nothing else of the host
// stands in it.
func emptyCacheDirInContainer(dockerPath string, env []string, p *Profile, dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cacheRemoveTimeout)
	defer cancel()
	ref, err := removalImage(ctx, dockerPath, env, p)
	if err != nil {
		return err
	}
	out, err := dockerCmd(ctx, dockerPath, env, "run", "--rm", "--pull=never", "--network", "none",
		"--entrypoint", "find", "-v", dir+":"+dir, ref,
		dir, "-mindepth", "1", "-delete").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, firstLine(string(out)))
	}
	return nil
}

// firstLine keeps an error message to docker's own reason, which stands
// first in its output; the trailing help hint line must never reach a log.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

// migrateForeignCacheDir clears a cache whose content another user wrote:
// the releases before the --user flag ran the server as the image's root,
// and a root owned index refuses the server that now runs as the cockpit's
// user its own storage. The check reads one level below the top, because
// the top level itself is ensureCacheDir's and always the cockpit's own,
// foreign ownership begins at the entries the container wrote. A removed
// cache means the server starts cold once.
func migrateForeignCacheDir(dir, dockerPath string, env []string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		uid, ok := fileUID(info)
		if !ok || uid == processUID {
			continue
		}
		log.Printf("editor intelligence: cache %s belongs to uid %d, removing it for one cold start", dir, uid)
		return removeCacheDir(dir, dockerPath, env)
	}
	return nil
}

// lspSchemeName reports whether the name belongs to the LSP naming scheme,
// container, cache directory and legacy volume alike: the scheme is the
// same server prefixes cacheDirProfile reads.
func lspSchemeName(name string) bool {
	return cacheDirProfile(name) != nil
}

// RemoveProjectCaches takes the project's per-server cache directories
// away, close its servers first: the delete owns the whole project, index
// and module downloads included. Removal is retried briefly, a container
// still draining its exit writes into the directory for a moment; a
// directory that is already gone is skipped without a wait.
//
// dockerHost names the configured daemon, empty for the ambient one; it is
// what removeCacheDir's container fallback and the removal of a volume an
// older release left behind travel on.
func RemoveProjectCaches(project, cacheRoot, dockerHost string) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		dockerPath = ""
	}
	env := dockerHostEnv(dockerHost)
	for _, p := range profiles {
		dir := cacheDir(cacheRoot, p.Server, project)
		for attempt := 0; attempt < 15; attempt++ {
			if _, err := os.Stat(dir); err != nil {
				break
			}
			if err := removeCacheDir(dir, dockerPath, env); err == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
	removeLegacyProjectVolumes(project, dockerHost)
}

// removeLegacyProjectVolumes takes the named cache volumes of the previous
// scheme with the project, for an install that has not been through a boot
// sweep since the caches became directories. TODO(v2.0.0): drop with
// sweepLegacyVolumes.
func removeLegacyProjectVolumes(project, dockerHost string) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return
	}
	env := dockerHostEnv(dockerHost)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, p := range profiles {
		name := containerName(p.Server, project)
		if dockerCmd(ctx, dockerPath, env, "volume", "inspect", name).Run() == nil {
			_ = dockerCmd(ctx, dockerPath, env, "volume", "rm", name).Run()
		}
	}
}

// removeStaleContainer clears the name right before a start: a container
// that outlived an unclean death, or one still draining its exit after a
// fast restart, would otherwise block the new server with a taken name.
// Nothing speaks to such a container anymore, its pipes died with the old
// process, so removal is the reuse.
func removeStaleContainer(ctx context.Context, dockerPath string, env []string, name string) {
	rmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = dockerCmd(rmCtx, dockerPath, env, "rm", "-f", name).Run()
}
