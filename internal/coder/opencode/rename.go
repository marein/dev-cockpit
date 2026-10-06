package opencode

// RenamesInside implements coder.SessionRenamedInside. opencode's /rename
// takes no name, it opens a dialog of its own.
func (p *Coder) RenamesInside() bool { return true }
