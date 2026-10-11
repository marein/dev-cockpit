package musicplayer_test

import (
	"io/fs"
	"regexp"
	"testing"

	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/plugin"
	"github.com/marein/dev-cockpit/plugins/musicplayer"
)

// The distribution picks the id, so the markup mounts the elements under the
// names the cockpit answers, with the volume inside the player, and every
// element's module is in the assets. The menu keeps its own selection, app.js
// lets the focus follow the pointer in every other dropdown, which would pull
// the cursor out of the filter while typing.
func TestThePlayerMountsUnderTheWiringID(t *testing.T) {
	serves, err := pluginhost.ConfigureServe([]plugin.Named[plugin.ServePlugin]{{ID: "tunes", Plugin: musicplayer.Plugin{}}}, t.TempDir(), t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("configure serve: %v", err)
	}
	s := serves[0]
	markup := s.SlotHTML(plugin.SlotStatusEnd)
	if !regexp.MustCompile(`(?s)^<tunes-player class="dc-status-item dropup">.*<tunes-volume></tunes-volume>.*</tunes-player>\s*$`).MatchString(markup) {
		t.Fatalf("the markup does not mount the player with its volume:\n%s", markup)
	}
	if !regexp.MustCompile(`<div class="dropdown-menu[^"]*" data-own-selection>`).MatchString(markup) {
		t.Fatal("the song menu does not carry data-own-selection")
	}
	if n := len(s.CustomElements()); n != 2 {
		t.Fatalf("%d elements added", n)
	}
	for _, element := range s.CustomElements() {
		if _, err := fs.Stat(s.Assets(), element.Module); err != nil {
			t.Errorf("%s names a missing module: %v", element.Name, err)
		}
	}
}
