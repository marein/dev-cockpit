// Package musicplayer is the song player of the status line, a first party
// plugin: the official build wires it in cmd/dev-cockpit, a custom
// distribution wires it the same way or leaves it out.
//
// On a desktop, the music note at the right of the status line opens the
// songs. Play starts the chill electronic and melodic house songs in random
// order, also across pages, stop ends them, next skips, and a title in the
// list, narrowed by the filter above it, plays that song. The filter and the
// list position stay where they were left, per device. The volume there is
// the music's own, stored per device and apart from the notification sound.
package musicplayer

import (
	"embed"
	"io/fs"
	"strings"
	"text/template"

	"github.com/marein/dev-cockpit/plugin"
)

//go:embed assets
var assets embed.FS

//go:embed player.html
var playerHTML string

var player = template.Must(template.New("player").Parse(playerHTML))

// Plugin is the music player, wired as one plugin.Named pair.
type Plugin struct{}

func (Plugin) ConfigureServe(s plugin.Serve) error {
	files, err := fs.Sub(assets, "assets")
	if err != nil {
		return err
	}
	s.AddAssets(files)
	names := struct{ Player, Volume string }{
		Player: s.AddCustomElement("player", "player.js").Name(),
		Volume: s.AddCustomElement("volume", "volume.js").Name(),
	}
	var markup strings.Builder
	if err := player.Execute(&markup, names); err != nil {
		return err
	}
	s.AddSlotHTML(plugin.SlotStatusEnd, markup.String())
	return nil
}
