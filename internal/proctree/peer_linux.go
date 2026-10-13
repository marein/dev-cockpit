//go:build linux

package proctree

import (
	"net"

	"golang.org/x/sys/unix"
)

// PeerPID answers the process on the other end of a unix socket, as the
// kernel recorded it when that process connected, 0 where it cannot be read.
func PeerPID(c net.Conn) int {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0
	}
	pid := 0
	_ = raw.Control(func(fd uintptr) {
		if cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
			pid = int(cred.Pid)
		}
	})
	return pid
}
