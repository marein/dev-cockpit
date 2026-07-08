package push

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// pushHTTPClient is the outbound client for every push channel: bounded by
// a timeout, never following redirects, and refusing link local
// destinations at dial time, after DNS resolution, so a registered URL or a
// DNS rebind cannot point the server at metadata style endpoints. Loopback
// and private ranges stay allowed on purpose, local webhook receivers are a
// normal setup for this cockpit.
var pushHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
			Control: refuseLinkLocal,
		}).DialContext,
	},
}

func refuseLinkLocal(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return fmt.Errorf("link local address %s refused", addr)
	}
	return nil
}
