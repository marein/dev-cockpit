// Command dev-cockpit runs the tmux-backed developer cockpit. The whole CLI
// lives in the importable distro package, this main only carries the build
// vars and wires the first party plugins: the vars stay in a main package so
// the documented -X main.<name> injection keeps working for release builds
// and forks, and a custom distribution main (see the README) carries vars of
// its own the same way.
package main

import (
	"github.com/marein/dev-cockpit/distro"
	"github.com/marein/dev-cockpit/plugin"
	"github.com/marein/dev-cockpit/plugins/musicplayer"
)

var (
	version          string
	repoURL          string
	updateFeedURL    string
	updateFeedFormat string
)

func main() {
	distro.Main(distro.Build{
		Version:          version,
		RepoURL:          repoURL,
		UpdateFeedURL:    updateFeedURL,
		UpdateFeedFormat: updateFeedFormat,
		ServePlugins: []plugin.Named[plugin.ServePlugin]{
			{ID: "music", Plugin: musicplayer.Plugin{}},
		},
	})
}
