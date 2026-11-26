package tmux

import (
	"sync"

	"github.com/marein/dev-cockpit/internal/clirun"
)

// controlGate keeps a control client's attach apart from what tmux tells
// every control client about. tmux 3.4 counts a client as a control client
// from its first identify message on, but sets up what writing to it needs
// only at its last, and it writes %sessions-changed, %session-renamed,
// %client-detached and %paste-buffer-* to every control client it counts.
// One of them in between crashes the server, and every session with it.
// Attaches share the gate until they are attached, the commands that cause
// one of these lines and a control client's detach take it alone.
var controlGate sync.RWMutex

// announced runs tmux commands that every control client hears of.
func announced(cmds ...[]string) error {
	controlGate.Lock()
	defer controlGate.Unlock()
	for _, args := range cmds {
		if err := clirun.Check("tmux", args...); err != nil {
			return err
		}
	}
	return nil
}
