package netguard

import (
	"net/netip"
	"testing"
)

func TestLinkLocalAddressesAreRefusedAndLocalOnesAllowed(t *testing.T) {
	for _, raw := range []string{"169.254.169.254", "fe80::1", "fe80::1%eth0", "::ffff:169.254.1.1", "ff02::1"} {
		if !LinkLocal(netip.MustParseAddr(raw)) {
			t.Fatalf("want %s link local", raw)
		}
		if err := RefuseLinkLocal("tcp", "["+raw+"]:80", nil); err == nil {
			t.Fatalf("want a dial to %s refused", raw)
		}
	}
	for _, raw := range []string{"127.0.0.1", "::1", "10.0.0.5", "192.168.1.20", "172.16.0.1", "8.8.8.8"} {
		if LinkLocal(netip.MustParseAddr(raw)) {
			t.Fatalf("want %s allowed", raw)
		}
		if err := RefuseLinkLocal("tcp", "["+raw+"]:80", nil); err != nil {
			t.Fatalf("want a dial to %s allowed, got %v", raw, err)
		}
	}
}
