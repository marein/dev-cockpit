package editorintelligence

import (
	"os/exec"
	"syscall"
)

// setChildProcAttr makes the kernel deliver SIGKILL to a language server
// when the serve process dies, so an abrupt exit never leaves orphaned
// children. The regular shutdown path still closes servers gracefully.
func setChildProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
