package provider

import (
	"sort"
	"strings"
)

type Registry struct {
	providers []Provider
}

func NewRegistry(ps ...Provider) *Registry { return &Registry{providers: ps} }

func (r *Registry) IDs() []string {
	ids := make([]string, 0, len(r.providers))
	for _, p := range r.providers {
		ids = append(ids, p.ID())
	}
	sort.Strings(ids)
	return ids
}

func (r *Registry) ByID(rawID string) Provider {
	id := strings.TrimSpace(strings.ToLower(rawID))
	for _, p := range r.providers {
		if p.ID() == id {
			return p
		}
	}
	return nil
}
