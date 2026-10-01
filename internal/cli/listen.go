package cli

import (
	"log"
	"net"
	"net/http"
)

// serveHTTP names the listener's own address, never the configured one, so
// a port of 0 shows the port the system picked.
func serveHTTP(server *http.Server, listener net.Listener, certFile, keyFile string) error {
	addr := listener.Addr().String()
	if certFile != "" {
		log.Printf("listening on https://%s", addr)
		return server.ServeTLS(listener, certFile, keyFile)
	}
	log.Printf("listening on http://%s", addr)
	return server.Serve(listener)
}
