//go:build !unix

package assistant

import (
	"errors"
	"os"
	"os/exec"
)

// A turn that cannot be detached would die with the server, which is the one
// thing this feature exists to prevent. Refusing to start is honest; the
// cockpit is a unix program.
func detach(*exec.Cmd) error {
	return errors.New("The coder could not be started.")
}

func lockTurn(string) (*os.File, error) {
	return nil, errors.New("The coder could not be started.")
}

func processAlive(int, string) bool { return false }

func killProcess(int, string) {}
