//go:build unix

package clirun

import (
	"errors"
	"os/exec"
	"syscall"
)

// KillsWholeGroup puts the process into a group of its own and makes the
// context's end kill that group, not just the process: whatever it starts,
// ssh or a credential helper behind git, a script's own children, inherits the
// group, and killing the process alone would leave them alive, holding the
// output pipes open and, if they are interactive, waiting forever for an
// answer nobody can give.
func KillsWholeGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
}
