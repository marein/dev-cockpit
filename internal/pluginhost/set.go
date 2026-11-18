package pluginhost

import (
	"slices"
	"sort"
)

// Set is every plugin of the serving cockpit: the compiled ones always on,
// the optional ones switched on and the loaded external ones. The last two
// come and go, so every surface asks the Set when it renders or routes and
// keeps no list of its own. A nil Set has no plugins.
type Set struct {
	compiled []*Serve
	host     *Host
	events   events
}

// NewSet joins the compiled Serves always on with the host's live ones, host
// nil for none.
func NewSet(compiled []*Serve, host *Host) *Set {
	return &Set{compiled: compiled, host: host}
}

// Serves answers the compiled Serves in wiring order, the switched on
// optional ones among them, then the loaded external ones by id.
func (s *Set) Serves() []*Serve {
	if s == nil {
		return nil
	}
	if s.host == nil {
		return s.compiled
	}
	serves := append(append([]*Serve(nil), s.compiled...), s.host.Optional()...)
	slices.SortStableFunc(serves, func(a, b *Serve) int { return a.order - b.order })
	return append(serves, s.host.Serves()...)
}

// Compiled answers the compiled Serves always on, the ones the asset
// manifest bakes in at start.
func (s *Set) Compiled() []*Serve {
	if s == nil {
		return nil
	}
	return s.compiled
}

// Lookup answers the Serve of one plugin id, nil for none.
func (s *Set) Lookup(id string) *Serve {
	for _, serve := range s.Serves() {
		if serve.ID() == id {
			return serve
		}
	}
	return nil
}

// Version names the state of the live plugins. It changes whenever one
// loads, unloads or changes state, a live region showing this version skips
// its pull.
func (s *Set) Version() string {
	if s == nil || s.host == nil {
		return "0"
	}
	return s.host.Version()
}

func sortServes(serves []*Serve) {
	sort.Slice(serves, func(i, j int) bool { return serves[i].ID() < serves[j].ID() })
}

// Host answers the host of the live plugins, nil for none.
func (s *Set) Host() *Host {
	if s == nil {
		return nil
	}
	return s.host
}
