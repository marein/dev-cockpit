// Package editorintelligence provides code navigation for the project
// editor: go to definition and find usages answered by language servers over
// stdio JSON-RPC. The servers run server side, the browser only exchanges
// bounded document snapshots and locations.
package editorintelligence

import (
	"path"
	"sort"
	"strings"
)

// Profile is one fixed language server profile compiled into the binary. The
// command, and the container recipe of the Docker option, are never
// configurable, so no setting can become a command execution surface; a
// setting only picks which of the fixed ways runs, or none.
type Profile struct {
	// ID is the stable profile identifier, used by the settings key.
	ID string
	// Label names the language on the settings page and in the indexing
	// indicator.
	Label string
	// Command is the server's argv, run inside the profile's container
	// over the Docker option.
	Command []string
	// Server is the short name this server wears everywhere the cockpit
	// names it itself: the image, the container, the per project cache
	// directory and the stored settings value. It is deliberately its own
	// field and not the command's leading token, which is a program name
	// and may be long enough to eat the room a container name leaves for
	// the project.
	Server string
	// Marker is the file at a project root whose presence already says
	// the project holds this language, so the warm skips the tree walk.
	Marker string
	// SilentStart marks a server that announces no work when it starts
	// because it has none: it has the workspace ready by the time it
	// answers the first request, so a lookup is never held back waiting
	// for an announcement that is not coming. The servers that index the
	// workspace at the handshake announce that run seconds late, and their
	// silence until then has to be waited out, which is the zero value
	// here: a profile that says nothing keeps the careful behaviour.
	SilentStart bool
	// languageIDs maps the owned file extensions (lowercase, without dot)
	// to the LSP language identifier sent in didOpen.
	languageIDs map[string]string
	// container is how the Docker option runs this server.
	container container
}

// profiles is the fixed registry, ordered for stable rendering. Deliberately
// only the languages the navigation is verified against; a profile joins the
// list with that verification, not before.
var profiles = []*Profile{
	{
		ID:          "go",
		Label:       "Go",
		Command:     []string{"gopls"},
		Server:      "gopls",
		Marker:      "go.mod",
		languageIDs: map[string]string{"go": "go"},
		container: container{
			Image:      "dev-cockpit-gopls",
			Dockerfile: goplsDockerfile + entrypointDockerfile,
			// The module downloads are what a jump into a dependency lands
			// in, so they lie in the cache directory and are readable from
			// the host under the very path the server names them by. The
			// file cache would die with the container under its default
			// XDG_CACHE_HOME and sits beside them, deliberately not among
			// the sources: it holds no source and the read route reaches
			// nothing it does not have to.
			// -modcacherw is what keeps the downloads deletable: the module
			// cache is written read only by default, and the cockpit has to
			// be able to take a deleted project's cache away again.
			CacheEnv: func(dir string) []string {
				return []string{
					"GOMODCACHE=" + dir + "/mod",
					"GOFLAGS=-modcacherw",
					"XDG_CACHE_HOME=" + dir + "/cache",
				}
			},
			CacheSources: []string{"mod"},
			ImageRoots:   []string{"/usr/local/go/src"},
		},
	},
	{
		ID:          "php",
		Label:       "PHP",
		Command:     []string{"intelephense", "--stdio"},
		Server:      "intelephense",
		Marker:      "composer.json",
		languageIDs: map[string]string{"php": "php"},
		container: container{
			Image:      "dev-cockpit-intelephense",
			Dockerfile: intelephenseDockerfile + entrypointDockerfile,
			// The server stores its index under its own storage paths, not
			// under os.tmpdir: without pointing them into the cache mount
			// the index died with the container and every start ran cold.
			// The per-project directory is the project boundary, the paths
			// inside it stay plain. A dependency of a PHP project lies in
			// its own vendor folder and therefore inside the project, so
			// only the server's stubs stand outside it.
			InitOptions: func(dir string) map[string]any {
				return map[string]any{
					"storagePath":       dir + "/storage",
					"globalStoragePath": dir + "/global",
				}
			},
			ImageRoots: []string{"/usr/local/lib/node_modules/intelephense/lib/stub"},
		},
	},
	{
		ID:          "typescript",
		Label:       "JS / TS",
		Command:     []string{"tsgo", "--lsp", "-stdio"},
		Server:      "tsgo",
		Marker:      "package.json",
		SilentStart: true,
		languageIDs: map[string]string{
			"ts":  "typescript",
			"mts": "typescript",
			"cts": "typescript",
			"tsx": "typescriptreact",
			"js":  "javascript",
			"mjs": "javascript",
			"cjs": "javascript",
			"jsx": "javascriptreact",
		},
		container: container{
			Image:      "dev-cockpit-tsgo",
			Dockerfile: typescriptDockerfile + entrypointDockerfile,
			// A dependency of a node project lies in its own node_modules and
			// therefore inside the project. The lib.*.d.ts files are the
			// server's own and live in the image, under a directory whose name
			// carries the architecture it was built for, so the image root is
			// the installation above it. What lands outside both is what the
			// server downloads itself, the automatic type acquisition a plain
			// JavaScript project gets its @types from: it keeps those under
			// XDG_CACHE_HOME, so without pointing it into the cache mount they
			// would die with the container and a definition would land in a
			// directory nothing could read back. Only the typings subtree holds
			// source; what npm writes beside it is no more readable than gopls'
			// file cache and stays out of the roots.
			CacheEnv: func(dir string) []string {
				return []string{"XDG_CACHE_HOME=" + dir + "/cache"}
			},
			CacheSources: []string{"cache/typescript"},
			ImageRoots:   []string{"/usr/local/lib/node_modules/@typescript"},
			// A project without a configuration of its own gets one from the
			// image, which is what makes a usages list cover the project
			// instead of the opened file and its imports. What that file is
			// and when it is written stands in the build file.
			DefaultConfig: true,
		},
	},
}

// Profiles returns the fixed profile registry in rendering order.
func Profiles() []*Profile {
	return profiles
}

// Extensions returns the file extensions the profile owns, sorted.
func (p *Profile) Extensions() []string {
	exts := make([]string, 0, len(p.languageIDs))
	for ext := range p.languageIDs {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	return exts
}

// ProfileForPath returns the profile owning the file's extension and the
// LSP language id for it.
func ProfileForPath(rel string) (*Profile, string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(rel), "."))
	if ext == "" {
		return nil, "", false
	}
	for _, p := range profiles {
		if id, ok := p.languageIDs[ext]; ok {
			return p, id, true
		}
	}
	return nil, "", false
}

// Detection is a launcher's answer whether its way can run, see
// Launcher.Detect. It never starts a process.
type Detection struct {
	Found bool
	// Path is the resolved executable when found, the docker client for
	// the Docker way.
	Path string
}
