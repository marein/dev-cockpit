package cli

import (
	"errors"
	"slices"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/project"
)

// keepForgeOverrides answers an import of the Forges section. An override of
// a project this host does not have would bind a later project of the same
// name, so only the projects there are keep theirs, those the same import
// brought along included. A project that cannot be looked up may be there.
func keepForgeOverrides(forges *forge.Service, projects *project.Repository) func(sections []string) {
	return func(sections []string) {
		if !slices.Contains(sections, "forges") {
			return
		}
		forges.DropOverrides(func(name string) bool {
			_, err := projects.FindByName(name)
			return errors.Is(err, project.ErrMissing)
		})
	}
}
