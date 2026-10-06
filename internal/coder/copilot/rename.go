package copilot

import "github.com/marein/dev-cockpit/internal/terminal"

// RenameInput implements coder.SessionRenamer.
func (p *Coder) RenameInput(name string) []terminal.Input {
	return []terminal.Input{{Prompt: "/rename " + name}}
}
