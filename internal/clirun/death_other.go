//go:build !linux || nopdeathsig

package clirun

import (
	"os"
	"os/exec"
	"syscall"
)

// DiesWithParent has no parent death signal here, so a guard of this binary
// stands in: the process joins the guard's process group, which the guard
// kills once its stdin ends, and only the starter holds the other end. The
// process stays the starter's own child, its signals, output and exit code
// untouched. Call release once the process was waited for.
func DiesWithParent(cmd *exec.Cmd) (release func()) {
	self := Executable()
	if self == "" {
		return func() {}
	}
	r, w, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	defer r.Close()
	guard := exec.Command(self, GuardCommand...)
	guard.Stdin = r
	guard.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := guard.Start(); err != nil {
		_ = w.Close()
		return func() {}
	}
	go func() {
		_ = guard.Wait()
		_ = w.Close()
	}()
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = guard.Process.Pid
	return func() { _, _ = w.Write([]byte{0}) }
}
