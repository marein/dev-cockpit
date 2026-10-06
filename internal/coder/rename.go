package coder

import (
	"errors"
	"strings"
	"unicode"

	"github.com/marein/dev-cockpit/internal/terminal"
)

// SessionRenamer is the optional capability to rename a running session. The
// name lives in the CLI's own session record, so the cockpit cannot set it,
// only ask the CLI to: RenameInput answers the CLI's own rename command, sent
// as is. The cockpit cannot know what stands in the input line or which
// dialog is open, so it touches neither, the user decides whether it fits.
type SessionRenamer interface {
	RenameInput(name string) []terminal.Input
}

// ErrRenameUnsupported marks a coder whose CLI cannot be renamed from outside.
var ErrRenameUnsupported = errors.New("This coder cannot be renamed from the cockpit.")

// SessionRenamedInside is the optional answer of a coder whose CLI renames a
// session only in a dialog of its own, with no command that takes the name.
// The cockpit cannot know what that dialog holds, so it types nothing and
// points the user to the command inside the coder.
type SessionRenamedInside interface {
	RenamesInside() bool
}

// The ways a running session can be renamed, see RenameMode.
const (
	RenameSend   = "send"
	RenameManual = "manual"
)

// RenameMode answers how a coder's running sessions are renamed: RenameSend
// when the cockpit types the command, RenameManual when only the user can, in
// the coder, and empty when there is no way at all.
func RenameMode(c Coder) string {
	if _, ok := c.(SessionRenamer); ok {
		return RenameSend
	}
	if inside, ok := c.(SessionRenamedInside); ok && inside.RenamesInside() {
		return RenameManual
	}
	return ""
}

// Rename sends the coder's rename command into a running session and answers
// the name it sent. The record changes when the CLI has run the command, and
// the turn watch announces it like any rename made inside the CLI.
func (s *Manager) Rename(rawID, rawName string) (string, error) {
	renamer, ok := s.coder.(SessionRenamer)
	if !ok {
		return "", ErrRenameUnsupported
	}
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return "", err
	}
	name := cleanSessionName(rawName)
	if name == "" {
		return "", errors.New("Coder name is required.")
	}
	if err := s.Send(r.Identifier, renamer.RenameInput(name)); err != nil {
		return "", err
	}
	return name, nil
}

// cleanSessionName keeps a name to one line of printable text. A line break
// would submit the command early, the rest would reach the model as a prompt.
func cleanSessionName(raw string) string {
	printable := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, raw)
	return strings.Join(strings.Fields(printable), " ")
}
