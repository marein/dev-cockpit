package render

import (
	"slices"
	"strings"

	"github.com/marein/dev-cockpit/internal/share"
)

// Access is who a page is rendered for and what that one may do. The zero
// value is the signed in owner, who may do everything. A guest of a link reads
// the link's projects and assistants under the link's address and does in the
// project its request is bound to what the link grants there, which a template
// asks with Can and a script reads from Grants.
type Access struct {
	Guest bool
	// Base stands in front of every address of the cockpit a page links or
	// calls: empty for the owner, the link's own address for a guest.
	Base string
	// Project is the project of the link a guest's request is bound to, empty
	// on a page that belongs to none of them.
	Project string
	// Projects and Assistants say which kinds the guest's link carries, the
	// areas its rail offers.
	Projects   bool
	Assistants bool
	grants     []share.Permission
}

// GuestAccess is a guest's on the link at base, in project.
func GuestAccess(base string, sh share.Share, project string) Access {
	r, _ := sh.Resource(share.KindProject, project)
	return Access{
		Guest:      true,
		Base:       base,
		Project:    project,
		Projects:   len(sh.Targets(share.KindProject)) > 0,
		Assistants: len(sh.Targets(share.KindAssistant)) > 0,
		grants:     r.Permissions,
	}
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

// SharePermission is one permission a link can grant on a project, the way a
// form offers it. On marks it switched on for the project.
type SharePermission struct {
	Value share.Permission
	Label string
	Hint  string
	Icon  string
	On    bool
}

// ShareResource is one project or assistant on a link, Target what a form
// names it by, Name what the list shows. Permissions are what a project can
// be granted, each on or off.
type ShareResource struct {
	Kind        string
	Target      string
	Name        string
	Permissions []SharePermission
}

// Key is how a form names the resource, its kind and its target.
func (r ShareResource) Key() string {
	return r.Kind + ":" + r.Target
}

// ShareRow is one standing link of the list, Soon when it ends within a day.
type ShareRow struct {
	ID        string
	Name      string
	EditURL   string
	Resources []ShareResource
	CreatedAt string
	ExpiresAt string
	Soon      bool
}

// ShareCreated is a link just made, shown this once: the token is in no file
// and nowhere else after this answer. Opens are its projects and assistants.
type ShareCreated struct {
	URL       string
	Name      string
	Opens     []ShareOpened
	ExpiresAt string
}

// ShareOpened is a project or an assistant of a link just made, Grants what a
// project grants beyond reading, empty when nothing.
type ShareOpened struct {
	Name   string
	Grants string
}

// Granted reports whether any of the projects grants more than reading.
func (c ShareCreated) Granted() bool {
	return slices.ContainsFunc(c.Opens, func(o ShareOpened) bool { return o.Grants != "" })
}

// ReadOnly reports whether any project or assistant grants reading alone.
func (c ShareCreated) ReadOnly() bool {
	return slices.ContainsFunc(c.Opens, func(o ShareOpened) bool { return o.Grants == "" })
}

// SettingsSharingData feeds the list of links. Open is the New link the page
// opens at once, from a project's or an assistant's Share, empty otherwise.
type SettingsSharingData struct {
	Page
	SettingsNav SettingsNav
	Open        string
	Shares      []ShareRow
}

// ShareNewData feeds the form that makes a link, in the dialog (Modal) and as
// a page, with what the address named picked, and the link it made (Created).
// Entries are the picked ones as the form lists them, Offers every choice as
// the entry a pick adds. ExpiresAt is what the expiry the form stands on gives
// from now.
type ShareNewData struct {
	Page
	SettingsNav SettingsNav
	Modal       bool
	Opens       Picker
	Entries     []ShareResource
	Offers      []ShareResource
	Units       []ShareUnit
	Amount      int
	Unit        string
	ExpiresAt   string
	NameMax     int
	Created     *ShareCreated
}

// ShareEditData feeds a link's Edit, in the dialog (Modal) and as a page: what
// it opens, what could still go on it (Add) with the entry each pick adds
// (Offers), its expiry and its revoke. Unit is what the unit select stands
// on, the number starts empty and keeps the expiry.
type ShareEditData struct {
	Page
	SettingsNav SettingsNav
	Modal       bool
	ID          string
	Name        string
	URL         string
	CreatedAt   string
	ExpiresAt   string
	Resources   []ShareResource
	Add         Picker
	Offers      []ShareResource
	Units       []ShareUnit
	Unit        string
}

// GuestAssistantData feeds a guest's page of an assistant on a link: the
// whole conversation, the stream it follows and the page it pulls again, the
// owner's list column of the link's assistants, the turn running since when,
// and the aside of the owner's page under the names AssistantData gives it,
// read only.
type GuestAssistantData struct {
	Page
	Name            string
	StreamURL       string
	PageURL         string
	Messages        []AssistantMessageView
	Ctx             *AssistantCtxData
	Running         int64
	Jobs            []AssistantJobView
	JobsOpen        int
	JobsOlder       int
	JobsListURL     string
	Triggers        AssistantTriggersData
	TriggersListURL string
	WatchingOpen    int
}
