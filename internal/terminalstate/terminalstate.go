// Package terminalstate answers one question in one place: what is a terminal
// id right now. A send that could not be delivered and a screen that could not
// be read both have to say whether the terminal is merely stopped and can be
// brought back, and both used to answer with whatever the failing route
// happened to return, which says nothing about that.
//
// The states live here, the sentences do not. The same state reads differently
// depending on who asked: a local caller can be told to run a command, a
// browser must never be, so every surface words the state it gets itself.
package terminalstate

import "github.com/local/dev-cockpit/internal/coder"

// State is what the cockpit knows about a terminal id.
type State int

const (
	// Unknown means neither a running terminal nor a stored coder session
	// carries the id, so there is nothing to offer for it.
	Unknown State = iota
	// Running means a terminal with the id is live, a coder or a shell.
	Running
	// Resumable means nothing is running under the id, but a coder still keeps
	// the session, so `coder-resume` brings it back.
	Resumable
	// ShellGone means the id is a shell's and no shell is running under it.
	// Only a caller that already knows it asked about a shell gets this answer,
	// see ClassifyShell.
	ShellGone
)

// CoderLookup is what the classifier reads from one coder manager: whether a
// session with the id is live, and whether a stopped one is still there.
// *coder.Manager satisfies it.
type CoderLookup interface {
	Resolve(id string) error
	ResolveResumable(id string) (coder.Session, error)
}

// ShellLookup is what it reads from the shells: whether one with the id is
// live. *shell.Shells satisfies it.
type ShellLookup interface {
	Resolve(id string) error
}

// Classify says what a terminal id is, for a caller that knows nothing else
// about it. Running wins over Resumable: a live session is stored as well, and
// what a caller acts on is that it runs.
func Classify(id string, coders []CoderLookup, shells ShellLookup) State {
	for _, co := range coders {
		if co.Resolve(id) == nil {
			return Running
		}
	}
	if shells != nil && shells.Resolve(id) == nil {
		return Running
	}
	for _, co := range coders {
		if _, err := co.ResolveResumable(id); err == nil {
			return Resumable
		}
	}
	return Unknown
}

// ClassifyShell says what the id is for a caller that already knows it asked
// about a shell, which the shell routes do. A shell leaves nothing behind when
// it ends, so from the id alone a shell that is gone looks exactly like an id
// nobody ever used; the caller's own knowledge is what tells the two apart.
func ClassifyShell(id string, coders []CoderLookup, shells ShellLookup) State {
	if state := Classify(id, coders, shells); state != Unknown {
		return state
	}
	return ShellGone
}
