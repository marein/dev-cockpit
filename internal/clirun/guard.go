//go:build unix

package clirun

import (
	"os"
	"syscall"
)

// GuardCommand is the argv of the guard behind the binary. Its names stand in
// the command line of a running guard, so they never change.
var GuardCommand = []string{"child-guard"}

// Guard is the body of GuardCommand, see DiesWithParent: a byte on stdin is
// the release, the end of stdin is the starter's end. A guard that does not
// lead its process group was not started by DiesWithParent and kills nothing.
func Guard() int {
	if syscall.Getpgrp() != os.Getpid() {
		return 2
	}
	if n, _ := os.Stdin.Read(make([]byte, 1)); n == 0 {
		_ = syscall.Kill(0, syscall.SIGKILL)
	}
	return 0
}
