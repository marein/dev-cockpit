//go:build !linux && !darwin

package proctree

import "net"

// PeerPID answers 0, this platform does not say who connected.
func PeerPID(net.Conn) int { return 0 }
