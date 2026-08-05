// Package docker shows what the Docker daemon runs next to the projects. It
// keeps one connection for the whole cockpit: the container list is fetched
// once, then the daemon's event stream keeps the cache fresh, so a hundred
// projects cost one list call and not a compose invocation each. Containers
// are matched to a project through the compose labels, and the join key is
// the compose working directory, not the compose project name, because the
// name is a normalised folder name and two folders may share it.
//
// A machine without a reachable daemon is a normal state, not an error: the
// cache answers empty, the surfaces leave the docker parts out, and the
// watcher quietly keeps trying.
package docker

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Container is one entry of the cached list, reduced to what the surfaces
// show and the join needs.
type Container struct {
	ID      string
	Name    string
	Image   string
	State   string
	Status  string
	Health  string
	Project string
	Service string
	// WorkingDir is the compose working directory label, the join key to a
	// cockpit project. Empty for containers compose did not start.
	WorkingDir string
	Ports      []Port
	// Labels is what the daemon reported about the container, kept whole
	// because a reverse proxy in front of it writes its address in here and
	// which label that is, is configuration (see links.go). It rides along
	// with the list, so reading it costs no call of its own.
	Labels  map[string]string
	Created int64
}

// Port is one published port mapping.
type Port struct {
	Public  int
	Private int
	// Proto is empty for tcp, the unspoken default everywhere.
	Proto string
}

// Label writes the mapping the way docker ps does, public first.
func (p Port) Label() string {
	label := strconv.Itoa(p.Public) + ":" + strconv.Itoa(p.Private)
	if p.Proto != "" {
		label += "/" + p.Proto
	}
	return label
}

// Running reports whether the container is up, the one distinction the
// status color makes.
func (c Container) Running() bool {
	return c.State == "running" || c.State == "restarting" || c.State == "paused"
}

// Unwell reports the states worth an error color: a failing healthcheck or a
// daemon that gave up on the container.
func (c Container) Unwell() bool {
	return c.Health == "unhealthy" || c.State == "dead"
}

// DisplayName is what the chip shows: the compose service, the name a person
// gave the thing, with the container name as the fallback.
func (c Container) DisplayName() string {
	if c.Service != "" {
		return c.Service
	}
	return c.Name
}

// PortsLabel joins the published ports for the status line.
func (c Container) PortsLabel() string {
	labels := make([]string, len(c.Ports))
	for i, port := range c.Ports {
		labels[i] = port.Label()
	}
	return strings.Join(labels, ", ")
}

// Link is one address a browser can open for a container. The two ways a
// container is reachable share the shape:
//
// A published port is the address this page was reached on, at that port:
// Host is empty, which the client reads as its own location, and the scheme
// follows the container side of the mapping, a service listening on 443
// speaks TLS.
//
// A route a link rule read out of the labels is a host of its own, with the
// path it is routed under when it has one, and usually no port at all: the
// proxy answers on the scheme default. Its Scheme is whatever the rule pins,
// and empty, the usual answer, means the scheme of the page the link is
// opened from. Only the browser knows that one: what terminates TLS may sit
// above the proxy, where neither a label nor this server can see it.
type Link struct {
	Scheme string
	Host   string
	Port   int
	Path   string
}

// Address is the link as a person reads it, which is what the rule preview on
// the settings page shows: a route is its host and path, a published port is
// the port alone, and a scheme only appears where the link pins one.
func (l Link) Address() string {
	if l.Host == "" {
		return ":" + strconv.Itoa(l.Port)
	}
	address := l.Host
	if l.Port != 0 {
		address += ":" + strconv.Itoa(l.Port)
	}
	address += l.Path
	if l.Scheme != "" {
		return l.Scheme + "://" + address
	}
	return address
}

// State is one reading of the cache: whether a daemon answers, which host it
// is, and what it runs.
type State struct {
	Available  bool
	Host       string
	Containers []Container
}

// ForDir answers the containers whose compose working directory lies in dir,
// the per project view of the cache. The comparison also tries the resolved
// path, because compose records the path the user stood in, which may reach
// the same directory through a symlink.
func (s State) ForDir(dir string) []Container {
	if dir == "" {
		return nil
	}
	keys := []string{filepath.Clean(dir)}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		resolved = filepath.Clean(resolved)
		if resolved != keys[0] {
			keys = append(keys, resolved)
		}
	}
	var out []Container
	for _, c := range s.Containers {
		if c.WorkingDir == "" {
			continue
		}
		for _, key := range keys {
			if c.WorkingDir == key || strings.HasPrefix(c.WorkingDir, key+string(filepath.Separator)) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// composeFileNames are the file names the compose CLI looks up, in its order.
var composeFileNames = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}

// ComposeFile answers the compose file a directory carries, the way the
// compose CLI finds it.
func ComposeFile(dir string) (string, bool) {
	for _, name := range composeFileNames {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, true
		}
	}
	return "", false
}

// Stack is one compose control point of a project: a directory compose runs
// in, with what currently runs out of it. Dir carries the project relative
// label ("" for the project root) next to the absolute path.
type Stack struct {
	Dir   string
	Label string
	// Project is the compose project name the daemon reports for the
	// containers of this directory, empty while none runs. It is what a
	// command has to name explicitly to reach exactly this stack: compose
	// otherwise derives the name from the directory it stands in.
	Project string
	Running int
	Total   int
}

// StacksForDir answers the compose control points inside dir: every distinct
// compose working directory of the project's containers, plus the project
// root while it carries a compose file of its own. A project without any of
// that answers none, which is the quiet everywhere rule.
func (s State) StacksForDir(dir string) []Stack {
	if dir == "" {
		return nil
	}
	byDir := map[string]*Stack{}
	var order []string
	for _, c := range s.ForDir(dir) {
		stack, ok := byDir[c.WorkingDir]
		if !ok {
			stack = &Stack{Dir: c.WorkingDir}
			byDir[c.WorkingDir] = stack
			order = append(order, c.WorkingDir)
		}
		stack.Total++
		if stack.Project == "" {
			stack.Project = c.Project
		}
		if c.Running() {
			stack.Running++
		}
	}
	root := filepath.Clean(dir)
	if _, ok := ComposeFile(root); ok {
		if _, exists := byDir[root]; !exists {
			byDir[root] = &Stack{Dir: root}
			order = append(order, root)
		}
	}
	sort.Strings(order)
	out := make([]Stack, 0, len(order))
	for _, key := range order {
		stack := *byDir[key]
		if rel, err := filepath.Rel(root, stack.Dir); err == nil && rel != "." {
			stack.Label = rel
		}
		out = append(out, stack)
	}
	return out
}

// sortContainers keeps the list in one stable order, compose project first,
// then service, then name, so a refresh never reshuffles the chips.
func sortContainers(list []Container) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.Name < b.Name
	})
}
