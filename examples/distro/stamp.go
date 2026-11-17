package main

import (
	"net/http"

	"github.com/marein/dev-cockpit/plugin"
)

// stamp is always on: one route that answers the instance's state directory.
type stamp struct{}

func (stamp) ConfigureServe(s plugin.Serve) error {
	stateDir := s.StateDir()
	s.AddRoutes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("stamp " + stateDir))
	}))
	return nil
}
