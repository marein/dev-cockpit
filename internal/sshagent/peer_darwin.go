//go:build darwin

package sshagent

import (
	"net"

	"golang.org/x/sys/unix"
)

func peerPID(c net.Conn) int {
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
		if v, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID); err == nil {
			pid = v
		}
	})
	return pid
}
