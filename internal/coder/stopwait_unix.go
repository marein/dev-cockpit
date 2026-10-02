//go:build unix

package coder

import (
	"errors"
	"syscall"
)

// processAlive asks the kernel with the null signal. A process that is not
// ours to signal still exists.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
