//go:build unix

package git

import (
	"errors"
	"os/exec"
	"syscall"
)

// killsWholeGroup puts the process into a group of its own and makes the
// timeout kill that group, not just git: the helpers git starts — ssh, a
// credential helper, a signer's pinentry — inherit the group, and killing
// git alone would leave them alive, holding the output pipes open and, being
// interactive, waiting forever for an answer nobody can give.
func killsWholeGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
}
