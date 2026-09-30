package netguard

import (
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

func LinkLocal(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast()
}

func RefuseLinkLocal(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if LinkLocal(addr) {
		return fmt.Errorf("link local address %s refused", addr)
	}
	return nil
}
