// Command distro is an example distribution: the stock cockpit with the
// compiled plugins stamp, always on, and pulse, optional and off until
// switched on under Settings › Plugins. See the README on custom
// distributions.
package main

import (
	"github.com/marein/dev-cockpit/distro"
	"github.com/marein/dev-cockpit/plugin"
)

func main() {
	distro.Main(distro.Build{
		// An example must never update itself to the stock binary of the
		// default feed, so its feed points at a port nothing listens on.
		UpdateFeedURL: "http://127.0.0.1:9/no-releases",
		ServePlugins: []plugin.Named[plugin.ServePlugin]{
			{ID: "stamp", Plugin: stamp{}},
			{ID: "pulse", Plugin: &pulse{}, Optional: true},
		},
	})
}
