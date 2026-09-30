package push

import (
	"net"
	"net/http"
	"time"

	"github.com/marein/dev-cockpit/internal/netguard"
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
			Control: netguard.RefuseLinkLocal,
		}).DialContext,
	},
}
