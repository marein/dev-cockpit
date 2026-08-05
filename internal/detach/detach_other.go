//go:build !unix

package detach

import (
	"errors"
	"os"
	"os/exec"
)

// A run that cannot be detached would die with the server, which is the one
// thing this package exists to prevent. Refusing to start is honest; the
// cockpit is a unix program.
func detach(*exec.Cmd) error {
	return errors.New("detached runs need a unix system")
}

func takeLock(string) (*os.File, error) {
	return nil, errors.New("detached runs need a unix system")
}

func lockHeld(string) bool { return false }

// Alive reports whether a run this server did not start is still going.
func Alive(int, string) bool { return false }

// Kill ends the whole process group of a run.
func Kill(int, string) {}

// killOwnGroup is a no-op without process groups; a timeout then ends the
// program alone.
func killOwnGroup() {}
