//go:build linux && !nopdeathsig

package clirun

import (
	"os/exec"
	"syscall"
)

// DiesWithParent has the kernel SIGKILL the process the moment the process
// that starts it ends, however it ends: a crash, a kill -9 or a restart leaves
// nothing of it behind. The kernel ties the signal to the thread that started
// it, and Go ends a thread only when a goroutine locked to it ends, which no
// caller of this does. The release it answers has nothing to let go here.
func DiesWithParent(cmd *exec.Cmd) (release func()) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	return func() {}
}
