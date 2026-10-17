package render

import (
	"slices"
	"strings"

	"github.com/marein/dev-cockpit/internal/share"
)

// Access is who a page is rendered for and what that one may do. The zero
// value is the signed in owner, who may do everything. A guest of a link reads
// the link's projects under the link's address and does in the project its
// request is bound to what the link grants there, which a template asks with
// Can and a script reads from Grants.
type Access struct {
	Guest bool
	// Base stands in front of every address of the cockpit a page links or
	// calls: empty for the owner, the link's own address for a guest.
	Base string
	// Project is the project of the link a guest's request is bound to, empty
	// on a page that belongs to none of them.
	Project string
	grants  []share.Permission
}

// GuestAccess is a guest's on the link at base, in project.
func GuestAccess(base string, sh share.Share, project string) Access {
	r, _ := sh.Resource(share.KindProject, project)
	return Access{Guest: true, Base: base, Project: project, grants: r.Permissions}
}

// Can reports whether p is granted.
func (a Access) Can(p share.Permission) bool {
	return !a.Guest || slices.Contains(a.grants, p)
}

// Grants lists the permissions in effect, space separated, the way a data
// attribute carries them to a script.
func (a Access) Grants() string {
	var names []string
	for _, p := range share.All {
		if a.Can(p) {
			names = append(names, string(p))
		}
	}
	return strings.Join(names, " ")
}

// ShareUnit is one choice of the unit the lifetime is counted in.
type ShareUnit struct {
	Value string
	Label string
}

// ShareProject is one project on a link, with what the link grants there.
type ShareProject struct {
	Name   string
	Grants string
}

// ShareRow is one standing link on the sharing settings, with the projects
// that could still go on it and the one the page was opened for preselected.
type ShareRow struct {
	ID        string
	Name      string
	Projects  []ShareProject
	Addable   []string
	Pick      string
	CreatedAt string
	ExpiresAt string
}

// ShareCreated is a link just made, shown this once: the token is in no file
// and nowhere else after this answer.
type ShareCreated struct {
	URL       string
	Projects  []string
	ExpiresAt string
}

// SettingsSharingData feeds the sharing settings: the form that makes a link,
// with the project the page was opened for preselected, and every link that
// still opens a project, each with its projects, its name, its lifetime and
// its revoke.
type SettingsSharingData struct {
	Page
	SettingsNav SettingsNav
	Projects    []string
	Pick        string
	Units       []ShareUnit
	NameMax     int
	Shares      []ShareRow
	Created     *ShareCreated
}
