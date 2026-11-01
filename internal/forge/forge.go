// Package forge connects a project to the forge its code lives on, GitLab or
// GitHub, the public instances and self hosted ones alike. It holds the
// neutral model; what is particular to one provider lives in the provider's
// own subpackage.
package forge

import (
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/marein/dev-cockpit/internal/gitfacts"
)

// Kind is a sort of forge, what a provider subpackage declares about itself:
// its name and icon, what its users call a change request and the mark its
// number wears, "merge request" and "!" on GitLab, and the verb they run a
// failed job again with, "Retry" on GitLab, "" where the cockpit runs none
// again.
type Kind struct {
	ID         string
	Label      string
	Icon       string
	ChangeNoun string
	ChangeMark string
	Retry      string
}

// ChangeRef is a change request's number the way the forge writes it.
func (k Kind) ChangeRef(number int) string {
	return k.ChangeMark + strconv.Itoa(number)
}

// Host is one forge server. Aliases are the other names an origin may reach
// it under, an SSH host of its own or an ~/.ssh/config alias. CABundle holds
// the PEM certificates a server with an internal CA is trusted by, next to
// the system's. A host the user added carries the ID it is stored under, a
// built in one the name of its server.
type Host struct {
	ID       string
	Kind     Kind
	WebURL   string
	Aliases  []string
	CABundle string

	builtin bool
}

// BuiltIn is a host a provider comes with, which carries the name of its
// server as its ID.
func BuiltIn(kind Kind, webURL string, aliases ...string) Host {
	id := webURL
	if u, err := url.Parse(webURL); err == nil {
		id = u.Hostname()
	}
	return Host{ID: id, Kind: kind, WebURL: webURL, Aliases: aliases, builtin: true}
}

// Builtin reports whether the host comes with the cockpit, which nobody
// changes or deletes.
func (h Host) Builtin() bool {
	return h.builtin
}

// Binding is where a repository lives: the host and the repository path on
// it, "group/sub/repo".
type Binding struct {
	Host Host
	Path string
}

// WebURL is the repository's page on its host.
func (b Binding) WebURL() string {
	return strings.TrimRight(b.Host.WebURL, "/") + "/" + b.Path
}

// Address is the repository's page without its scheme.
func (b Binding) Address() string {
	web := b.WebURL()
	if _, rest, ok := strings.Cut(web, "://"); ok {
		return rest
	}
	return web
}

// Hosts are the hosts an origin resolves against, the built in ones first.
type Hosts []Host

// Resolve binds an origin remote url to the host it names. A host nobody
// knows, a local path and a path without namespace and name bind to nothing.
func (hs Hosts) Resolve(origin string) (Binding, bool) {
	remote, ok := gitfacts.ParseRemote(origin)
	if !ok {
		return Binding{}, false
	}
	for _, h := range hs {
		if !h.names(remote.Host) {
			continue
		}
		path := h.repositoryPath(remote)
		if !repositoryPath(path) {
			return Binding{}, false
		}
		return Binding{Host: h, Path: path}, true
	}
	return Binding{}, false
}

// host answers the host with that id.
func (hs Hosts) host(id string) (Host, bool) {
	at := slices.IndexFunc(hs, func(h Host) bool { return h.ID == id && id != "" })
	if at < 0 {
		return Host{}, false
	}
	return hs[at], true
}

// names reports whether the host answers under the given host name.
func (h Host) names(name string) bool {
	if u, err := url.Parse(h.WebURL); err == nil && strings.EqualFold(u.Hostname(), name) {
		return true
	}
	for _, alias := range h.Aliases {
		if strings.EqualFold(alias, name) {
			return true
		}
	}
	return false
}

// repositoryPath is where a remote's path names the repository on the host.
// A web address with a path, a server under /gitlab, puts that path in front
// of every http remote, while SSH never carries it.
func (h Host) repositoryPath(remote gitfacts.Remote) string {
	if remote.Scheme != "http" && remote.Scheme != "https" {
		return remote.Path
	}
	u, err := url.Parse(h.WebURL)
	if err != nil {
		return remote.Path
	}
	if base := strings.Trim(u.Path, "/"); base != "" {
		if rest, ok := strings.CutPrefix(remote.Path, base+"/"); ok {
			return rest
		}
	}
	return remote.Path
}

// repositoryPath reports whether a path can name a repository on a forge,
// which always stands in a namespace: at least two segments, none empty.
func repositoryPath(path string) bool {
	segments := strings.Split(path, "/")
	if len(segments) < 2 {
		return false
	}
	for _, segment := range segments {
		if segment == "" {
			return false
		}
	}
	return true
}
