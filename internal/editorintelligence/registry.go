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
	// over the Docker option; its leading token also names the server in
	// container and volume names.
	Command []string
	// Marker is the file at a project root whose presence already says
	// the project holds this language, so the warm skips the tree walk.
	Marker string
	// languageIDs maps the owned file extensions (lowercase, without dot)
	// to the LSP language identifier sent in didOpen.
	languageIDs map[string]string
	// container is how the Docker option runs this server.
	container container
}

// profiles is the fixed registry, ordered for stable rendering. Deliberately
// only the two languages the navigation is verified against; a profile joins
// the list with that verification, not before.
var profiles = []*Profile{
	{
		ID:          "go",
		Label:       "Go",
		Command:     []string{"gopls"},
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
