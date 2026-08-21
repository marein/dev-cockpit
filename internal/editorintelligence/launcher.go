package editorintelligence

import (
	"context"
	"os/exec"
)

// Launcher is one way to run a language server process; the Docker
// container is the one way today, a second container runtime would be the
// next. Everything that differs between the ways lives behind this
// interface: whether the way can run at all, what must stand before a
// start, the command line and environment that start the server, and what
// a death means. The service programs against the interface and carries no
// flavor branches, so another way is one more implementation and nothing
// else.
type Launcher interface {
	// ID tells launchers apart in the connection slots: a server whose
	// way to run changed is evicted and restarted the way a changed root
	// evicts it. The Docker way includes its configured daemon, so a
	// moved docker-host setting evicts too.
	ID() string
	// Detect answers whether the profile's server can run this way, and
	// where the way's own executable was found.
	Detect(p *Profile) Detection
	// Prepare stands up what one start needs, bounded by ctx. The Docker
	// way builds its image on first use, creates the labeled cache volume
	// and clears the stale container's name; a way with nothing to
	// prepare returns nil. projectsRoot is the ownership boundary the
	// labels carry.
	Prepare(ctx context.Context, projectsRoot, project string, p *Profile) error
	// Argv is the command line that starts the profile's server for the
	// workspace at root. projectsRoot is the directory a container mounts
	// at its own path so file URIs match inside and outside.
	Argv(projectsRoot, project, root string, p *Profile) []string
	// ProcEnv is the extra process environment the start command needs,
	// nil for none. The Docker way carries DOCKER_HOST when the cockpit
	// is configured for a daemon of its own, so the server runs on the
	// same daemon the availability gate read.
	ProcEnv() []string
	// InitOptions are the initializationOptions this way hands the server
	// for the project, nil for none. The container way points the server's
	// index storage into its cache mount, whose per-project directory is
	// the project boundary.
	InitOptions(project string, p *Profile) any
	// SourceRoots are the directories outside the project this way lets a
	// definition land in and read back: the dependency sources the server
	// downloaded, the standard library, the server's stubs. They are the
	// whole allowlist, an answer pointing anywhere else stays counted and
	// unopened.
	SourceRoots(project string, p *Profile) []SourceRoot
	// ReadSource answers the text of one file under one of its own roots.
	// The caller has checked that the root holds the path; whether the
	// file can be read at all, and how, is this way's business.
	ReadSource(ctx context.Context, root SourceRoot, path string) (string, error)
	// WantsRestart reads a dead server's exit code: true means the way
	// asks for an immediate fresh start (the container's workspace watcher
	// ends the container with an agreed code on a relevant change), every
	// other death stays an error.
	WantsRestart(exitCode int) bool
}

// DockerLauncher runs the server inside a container the cockpit builds
// and names itself. cacheRoot is where the per project cache directories
// live, the binds that make a dependency's sources readable from both
// sides. dockerHost answers the configured daemon, nil or empty for the
// ambient one.
func DockerLauncher(cacheRoot string, dockerHost func() string) Launcher {
	return dockerLauncher{cacheRoot: cacheRoot, host: dockerHost}
}

// lookPath is the detection primitive every launcher shares.
func lookPath(name string) Detection {
	resolved, err := exec.LookPath(name)
	if err != nil {
		return Detection{}
	}
	return Detection{Found: true, Path: resolved}
}

type dockerLauncher struct {
	cacheRoot string
	host      func() string
}

func (l dockerLauncher) hostValue() string {
	if l.host == nil {
		return ""
	}
	return l.host()
}

func (l dockerLauncher) ID() string { return "docker@" + l.hostValue() }

func (dockerLauncher) Detect(p *Profile) Detection { return lookPath("docker") }

func (l dockerLauncher) ProcEnv() []string { return dockerHostEnv(l.hostValue()) }

func (l dockerLauncher) Prepare(ctx context.Context, projectsRoot, project string, p *Profile) error {
	dockerPath := l.Detect(p).Path
	env := l.ProcEnv()
	if err := ensureImage(ctx, dockerPath, env, p); err != nil {
		return err
	}
	dir := l.cacheDir(project, p)
	// A cache another user wrote is unusable for the server that runs as
	// the cockpit's user now, so it goes before the start and the server
	// runs cold once, see migrateForeignCacheDir. Checked behind the
	// image, whose container the removal may need.
	if err := migrateForeignCacheDir(dir, dockerPath, env); err != nil {
		return err
	}
	if err := ensureCacheDir(dir); err != nil {
		return err
	}
	removeStaleContainer(ctx, dockerPath, env, containerName(p.Server, project))
	return nil
}

func (l dockerLauncher) Argv(projectsRoot, project, root string, p *Profile) []string {
	name := containerName(p.Server, project)
	return dockerArgv(l.Detect(p).Path, projectsRoot, l.cacheDir(project, p), root, name, p)
}

func (l dockerLauncher) InitOptions(project string, p *Profile) any {
	if p.container.InitOptions == nil {
		return nil
	}
	return p.container.InitOptions(l.cacheDir(project, p))
}

func (l dockerLauncher) SourceRoots(project string, p *Profile) []SourceRoot {
	return dockerSourceRoots(l.cacheRoot, project, p)
}

func (l dockerLauncher) ReadSource(ctx context.Context, root SourceRoot, path string) (string, error) {
	if root.Image == "" {
		return readHostSource(root, path)
	}
	dockerPath, err := lookDocker()
	if err != nil {
		return "", err
	}
	return readImageSource(ctx, dockerPath, l.ProcEnv(), root.Image, path)
}

func (l dockerLauncher) cacheDir(project string, p *Profile) string {
	return cacheDir(l.cacheRoot, p.Server, project)
}

// dockerHostEnv is the environment a configured daemon travels in, nil for
// the ambient one.
func dockerHostEnv(host string) []string {
	if host == "" {
		return nil
	}
	return []string{"DOCKER_HOST=" + host}
}

// watcherRestartCode is the agreed exit code the container's workspace
// watcher ends the container with on a relevant change: the cockpit reads
// exactly this code as a restart wish. The shared entrypoint carries the
// same number.
const watcherRestartCode = 64

func (dockerLauncher) WantsRestart(exitCode int) bool { return exitCode == watcherRestartCode }
