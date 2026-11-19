//go:build linux

package sshagent

import "golang.org/x/sys/unix"

// protectMemory does what ssh-agent does for its keys: a process that is not
// dumpable refuses ptrace, /proc/<pid>/mem and the rest of its /proc entry to
// every process without CAP_SYS_PTRACE, and the kernel writes no core of it,
// whatever core_pattern or suid_dumpable say. A child gets the flag back with its exec, so tmux, git and the coders
// run as before.
func protectMemory() error {
	return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}
