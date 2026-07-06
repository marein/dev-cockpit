package coder

import (
	"sort"
	"strings"
)

type Registry struct {
	coders []Coder
}

func NewRegistry(ps ...Coder) *Registry { return &Registry{coders: ps} }

// All returns the coders in registration order, which doubles as the
// default preference order when several coders are active.
func (r *Registry) All() []Coder { return r.coders }

func (r *Registry) IDs() []string {
	ids := make([]string, 0, len(r.coders))
	for _, p := range r.coders {
		ids = append(ids, p.ID())
	}
	sort.Strings(ids)
	return ids
}

func (r *Registry) ByID(rawID string) Coder {
	id := strings.TrimSpace(strings.ToLower(rawID))
	for _, p := range r.coders {
		if p.ID() == id {
			return p
		}
	}
	return nil
}
