package editorintelligence

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

//go:embed dockerfiles/gopls.Dockerfile
var goplsDockerfile string

//go:embed dockerfiles/intelephense.Dockerfile
var intelephenseDockerfile string

// entrypointDockerfile is the tail both build files share: the entrypoint
// runs the server next to a recursive workspace watcher, .git excluded,
// with a settle window so a checkout is one shove, and ends the container
// with the agreed exit code 64 on a relevant change (watcherRestartCode,
// the cockpit reads exactly that code as a restart wish). A refused watch
// (inotify limit) is loud on stderr and never a silently blind watcher.
// One copy here, or the exit code contract and the watch rules could
// drift apart between the languages.
const entrypointDockerfile = `RUN printf '%s\n' \
  '#!/bin/sh' \
  'flag=/run/dev-cockpit-restart' \
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
// cache volume whose mount keeps the server's index warm across container
// starts. The volumes are the ones hand-rolled wrappers used before the
// option existed, so an index survives the cutover.
type container struct {
	Image      string
	CacheMount string
	Dockerfile string
	// Env rides into the container run; the Go way persists its file
	// cache through it, next to the module downloads in the volume.
	Env []string
	// InitOptions builds the server's initializationOptions for one
	// container run: a server that stores its index outside the mount
	// would lose it with the container, so the storage is pointed into
	// the mount explicitly, into a per-project directory the cockpit
	// names, which keeps a later per-project cleanup exact. Nil for
	// servers without such options.
	InitOptions func(project string) map[string]any
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

// ensureVolume makes the cache volume exist with the root label before the
// run auto-creates it bare: the label is what the boot sweep scopes its
// ownership by. An existing volume is left as it is, the hand-rolled ones
// from before the option and every already labeled one alike.
func ensureVolume(ctx context.Context, dockerPath string, env []string, name, projectsRoot string) {
	if dockerCmd(ctx, dockerPath, env, "volume", "inspect", name).Run() == nil {
		return
	}
	_ = dockerCmd(ctx, dockerPath, env, "volume", "create", "--label", lspRootLabel+"="+projectsRoot, name).Run()
}

// dockerArgv is the container run of a profile's server: stdin carries the
// protocol, --init reaps, the projects directory mounts at its own path so
// file URIs match inside and outside, the workspace is the working
// directory, the name says in docker ps who runs what for whom, and the
// root label says which serve process owns it. The cache volume carries
// the container's name too, one volume per project and server: the volume
// is the project boundary, so a per-project cleanup is exact.
func dockerArgv(dockerPath, projectsRoot, root, name string, p *Profile) []string {
	argv := []string{dockerPath, "run", "--rm", "-i", "--init",
		"--name", name,
		"--label", lspRootLabel + "=" + projectsRoot,
		"-v", projectsRoot + ":" + projectsRoot,
		"-v", name + ":" + p.container.CacheMount,
		"-w", root,
	}
	for _, env := range p.container.Env {
		argv = append(argv, "-e", env)
	}
	argv = append(argv, imageRef(p))
	return append(argv, p.Command...)
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
// scheme labeled with this service's own projects root, and every such
// volume whose project no longer exists on disk: at serve start none of
// them has a living owner, the previous process and its pipes are gone,
// while the lazy removal before a start only ever covers the same project
// and language starting again. The root label is the ownership boundary,
// so another live instance's servers and caches on the same daemon are
// never touched. Call once, right after New.
func (s *Service) SweepStale() {
	done := make(chan struct{})
	s.mu.Lock()
	s.sweepDone = done
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(done)
		sweepStale(s.prepCtx, s.projectsRoot, dockerHostEnv(hostOf(s.dockerHost)))
	}()
}

func hostOf(host func() string) string {
	if host == nil {
		return ""
	}
	return host()
}

func sweepStale(ctx context.Context, projectsRoot string, env []string) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
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
	// The volumes: one per project and server, wearing the container's
	// name and the root label, so a volume whose name no living project
	// would produce is a leftover of a delete the cockpit did not see.
	valid := map[string]bool{}
	if entries, err := os.ReadDir(projectsRoot); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				for _, p := range profiles {
					valid[containerName(p.Command[0], entry.Name())] = true
				}
			}
		}
	}
	out, err = dockerCmd(ctx, dockerPath, env, "volume", "ls", "--filter", ownLabel, "--format", "{{.Name}}").Output()
	if err != nil {
		return
	}
	orphans := 0
	for _, name := range strings.Fields(string(out)) {
		if lspSchemeName(name) && !valid[name] {
			_ = dockerCmd(ctx, dockerPath, env, "volume", "rm", name).Run()
			orphans++
		}
	}
	if orphans > 0 {
		log.Printf("editor intelligence: swept %d orphaned server volume(s)", orphans)
	}
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

// lspSchemeName reports whether the name belongs to the LSP naming scheme,
// container and volume alike.
func lspSchemeName(name string) bool {
	for _, p := range profiles {
		if strings.HasPrefix(name, containerPrefix(p.Command[0])) {
			return true
		}
	}
	return false
}

// RemoveProjectVolumes takes the project's per-server volumes away, close
// its servers first: the delete owns the whole project, index and module
// caches included. dockerHost names the configured daemon, empty for the
// ambient one. Removal is retried briefly, a container still draining
// its exit holds the volume for a moment; a volume that never existed is
// skipped without a wait.
func RemoveProjectVolumes(project, dockerHost string) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return
	}
	env := dockerHostEnv(dockerHost)
	for _, p := range profiles {
		name := containerName(p.Command[0], project)
		// The dying container drains its exit for a few seconds and holds
		// the volume meanwhile, the watcher subshell included; the budget
		// is generous, the caller runs in the background.
		for attempt := 0; attempt < 15; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			exists := dockerCmd(ctx, dockerPath, env, "volume", "inspect", name).Run() == nil
			if !exists {
				cancel()
				break
			}
			err := dockerCmd(ctx, dockerPath, env, "volume", "rm", name).Run()
			cancel()
			if err == nil {
				break
			}
			time.Sleep(2 * time.Second)
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
