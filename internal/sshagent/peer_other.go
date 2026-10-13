//go:build !linux && !darwin

package sshagent

import "net"

func peerPID(net.Conn) int { return 0 }
