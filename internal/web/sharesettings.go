package web

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// Settings › Sharing is the list of links. Making a link and changing one are
// forms of their own, each a dialog over the list and a page without scripts:
// New link at settingsSharingNewPath, a link's Edit at its id below the list.

const settingsSharingNewPath = settingsSharingPath + "/new"

// shareSoon is how close a link's expiry stands before the list marks it: the
// next day, which is before the owner may look at the list again.
const shareSoon = 24 * time.Hour

// shareDefaultAmount and shareDefaultUnit are what a new link's expiry stands
// on.
const (
	shareDefaultAmount = 1
	shareDefaultUnit   = "hours"
)

var errShareGoneLink = errors.New("That link no longer exists.")

// shareUnitFor is the unit the time a link has left reads in, which is the
// one a new expiry is most likely typed in.
func shareUnitFor(left time.Duration) string {
	switch {
	case left >= 24*time.Hour:
		return "days"
	case left >= time.Hour:
		return "hours"
	}
	return "minutes"
}

// shareEditPath is where a link's Edit stands.
func shareEditPath(id string) string {
	return settingsSharingPath + "/" + url.PathEscape(id)
}

// sharePicker is the one dropdown that picks what a link opens, every kind
// in it: the projects and the assistants offers allows, those named in picks
// picked.
func (s *Server) sharePicker(id string, offers func(share.Kind, string) bool, picks []string) render.Picker {
	projects := render.PickerKind{Field: "project", Label: "Projects", Projects: true}
	for _, p := range s.editorSwitcher("") {
		if offers(share.KindProject, p.Name) {
			projects.Choices = append(projects.Choices, render.PickerChoice{Value: p.Name, Label: p.Name, Project: p, Picked: slices.Contains(picks, p.Name)})
		}
	}
	assistants := render.PickerKind{Field: "assistant", Label: "Assistants"}
	for _, a := range s.assistants.List() {
		if offers(share.KindAssistant, a.ID) {
			assistants.Choices = append(assistants.Choices, render.PickerChoice{
				Value:  a.ID,
				Label:  a.Title,
				Sub:    withShortID(render.CoderLabel(a.CoderID), a.ID),
				Picked: slices.Contains(picks, a.ID),
			})
		}
	}
	return render.Picker{
		ID:      id,
		Label:   "Projects and assistants",
		None:    "Pick projects or assistants",
		Search:  "Search…",
		NoMatch: "Nothing matches.",
		Kinds:   []render.PickerKind{projects, assistants},
	}
}

// shareEntries are the choices of a picker kept as the dialog lists them, each
// with its kind and full name, a project with its permissions, those grants
// answers for it on.
func shareEntries(picker render.Picker, keep func(render.PickerChoice) bool, grants func(string) []share.Permission) []render.ShareResource {
	var entries []render.ShareResource
	for _, kind := range picker.Kinds {
		for _, choice := range kind.Choices {
			if !keep(choice) {
				continue
			}
			entry := render.ShareResource{Kind: kind.Field, Target: choice.Value, Name: choice.Label}
			if share.Kind(kind.Field) == share.KindProject {
				granted := grants(choice.Value)
				entry.Permissions = shareOffers(func(p share.Permission) bool { return slices.Contains(granted, p) })
			}
			entries = append(entries, entry)
		}
	}
	return entries
}

// shareOffered lists every choice of a picker as the entry it becomes once
// picked, nothing granted yet.
func shareOffered(picker render.Picker) []render.ShareResource {
	return shareEntries(picker, func(render.PickerChoice) bool { return true }, func(string) []share.Permission { return nil })
}

// shareRows lists the links that still open a project or an assistant, each
// with what it opens as it stands now. A resource that cannot be read right
// now is left out, and so is a link with none left.
func (s *Server) shareRows() []render.ShareRow {
	now := time.Now()
	rows := []render.ShareRow{}
	for _, sh := range s.shares.List() {
		row := render.ShareRow{
			ID:        sh.ID,
			Name:      sh.Name,
			EditURL:   shareEditPath(sh.ID),
			CreatedAt: machineTime(sh.CreatedAt),
			ExpiresAt: machineTime(sh.ExpiresAt),
			Soon:      sh.ExpiresAt.Sub(now) < shareSoon,
			Resources: s.shareResourceViews(sh),
		}
		if len(row.Resources) > 0 {
			rows = append(rows, row)
		}
	}
	return rows
}

// shareResourceViews are the projects and assistants of a link that stand
// right now, each with what the link grants there.
func (s *Server) shareResourceViews(sh share.Share) []render.ShareResource {
	var out []render.ShareResource
	for _, p := range s.liveShareProjects(sh) {
		r, _ := sh.Resource(share.KindProject, p.Name)
		out = append(out, render.ShareResource{
			Kind: string(share.KindProject), Target: p.Name, Name: p.Name,
			Permissions: shareOffers(func(q share.Permission) bool { return slices.Contains(r.Permissions, q) }),
		})
	}
	for _, inst := range s.liveShareAssistants(sh) {
		out = append(out, render.ShareResource{Kind: string(share.KindAssistant), Target: inst.ID, Name: inst.Title})
	}
	return out
}

func (s *Server) handleSettingsSharing(c *gin.Context) {
	open := ""
	if picks := (url.Values{"project": c.QueryArray("project"), "assistant": c.QueryArray("assistant")}).Encode(); picks != "" {
		open = settingsSharingNewPath + "?" + picks
	}
	c.HTML(http.StatusOK, "settings_sharing.gohtml", render.SettingsSharingData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("sharing"),
		Open:        open,
		Shares:      s.shareRows(),
	})
}

// shareRefuseLocal turns a change of a link over the local socket away: a
// share opens the cockpit to somebody outside it, which no assistant may do
// on its own.
func (s *Server) shareRefuseLocal(c *gin.Context) bool {
	if !s.localCall(c) {
		return false
	}
	c.JSON(http.StatusForbidden, gin.H{"error": shareLocalRefusal})
	return true
}

// handleShareNew renders the form that makes a link, with the projects and
// assistants the address names picked, each project granting what the
// address names for it.
func (s *Server) handleShareNew(c *gin.Context) {
	s.renderShareNew(c, nil)
}

func (s *Server) renderShareNew(c *gin.Context, created *render.ShareCreated) {
	picks := append(c.QueryArray("project"), c.QueryArray("assistant")...)
	lifetime, _ := shareLifetime(strconv.Itoa(shareDefaultAmount), shareDefaultUnit)
	opens := s.sharePicker("share-opens", func(share.Kind, string) bool { return true }, picks)
	granted := func(project string) []share.Permission {
		var on []share.Permission
		for _, p := range sharePermissions {
			if slices.Contains(c.QueryArray("grant:"+string(p.Value)), project) {
				on = append(on, p.Value)
			}
		}
		return on
	}
	data := render.ShareNewData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("sharing"),
		Modal:       inFormModal(c),
		Opens:       opens,
		Entries:     shareEntries(opens, func(choice render.PickerChoice) bool { return choice.Picked }, granted),
		Offers:      shareOffered(opens),
		Units:       shareUnits,
		Amount:      shareDefaultAmount,
		Unit:        shareDefaultUnit,
		ExpiresAt:   machineTime(time.Now().Add(lifetime)),
		NameMax:     share.MaxNameLength,
		Created:     created,
	}
	template := "share_new.gohtml"
	if data.Modal {
		template = "share_new_form.gohtml"
		if created != nil {
			template = "share_created.gohtml"
		}
	}
	c.HTML(http.StatusOK, template, data)
}

// handleShareCreate makes a link to the projects and assistants picked, each
// project granting what its entry switched on, and shows it, this once: only
// its hash is stored, so the answer is the one place the token ever stands. A
// refusal keeps the dialog over what was typed, or leads the page back to the
// form with what it resolved picked and switched on again.
func (s *Server) handleShareCreate(c *gin.Context) {
	if s.shareRefuseLocal(c) {
		return
	}
	projects, assistants, err := s.sharePicks(c)
	if err == nil && len(projects)+len(assistants) == 0 {
		err = errSharePick
	}
	grants, grantErr := shareGranted(c, len(projects))
	if err == nil {
		err = grantErr
	}
	picked := url.Values{"project": projects, "assistant": assistants}
	if grantErr == nil {
		for _, name := range projects {
			for _, p := range grants(name) {
				picked.Add("grant:"+string(p), name)
			}
		}
	}
	formPath := settingsSharingNewPath
	if len(picked) > 0 {
		formPath += "?" + picked.Encode()
	}
	var lifetime time.Duration
	if err == nil {
		lifetime, err = shareLifetime(c.PostForm("amount"), c.PostForm("unit"))
	}
	var created share.Share
	var token string
	if err == nil {
		created, token, err = s.shares.Create(c.PostForm("name"), shareResources(projects, assistants, grants), lifetime)
	}
	if err != nil {
		s.formRefused(c, formPath, err.Error())
		return
	}
	var opens []render.ShareOpened
	for _, r := range created.Resources {
		opened := render.ShareOpened{Name: r.Target}
		if r.Kind == share.KindAssistant {
			inst, _ := s.assistants.Get(r.Target)
			opened.Name = inst.Title
		} else if len(r.Permissions) > 0 {
			opened.Grants = shareGrants(r)
		}
		opens = append(opens, opened)
	}
	c.Header("Cache-Control", "no-store")
	s.renderShareNew(c, &render.ShareCreated{
		URL:       shareURL(c, token),
		Name:      created.Name,
		Opens:     opens,
		ExpiresAt: machineTime(created.ExpiresAt),
	})
}

// standingShare answers the standing link an address names.
func (s *Server) standingShare(c *gin.Context) (share.Share, bool) {
	id := c.Param("id")
	for _, sh := range s.shares.List() {
		if sh.ID == id {
			return sh, true
		}
	}
	return share.Share{}, false
}

// handleShareEdit renders a link's Edit: what it opens with what each project
// grants, what could still go on it and its expiry. A link that no longer
// stands leads back to the list.
func (s *Server) handleShareEdit(c *gin.Context) {
	sh, ok := s.standingShare(c)
	if !ok {
		s.redirectWithFlash(c, settingsSharingPath, "", errShareGoneLink.Error())
		return
	}
	lacks := func(kind share.Kind, target string) bool {
		_, on := sh.Resource(kind, target)
		return !on
	}
	data := render.ShareEditData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("sharing"),
		Modal:       inFormModal(c),
		ID:          sh.ID,
		Name:        sh.Name,
		URL:         shareEditPath(sh.ID),
		CreatedAt:   machineTime(sh.CreatedAt),
		ExpiresAt:   machineTime(sh.ExpiresAt),
		Resources:   s.shareResourceViews(sh),
		Add:         s.sharePicker("share-add", lacks, nil),
		Units:       shareUnits,
		Unit:        shareUnitFor(time.Until(sh.ExpiresAt)),
	}
	data.Offers = shareOffered(data.Add)
	if data.Modal {
		c.HTML(http.StatusOK, "share_edit_form.gohtml", data)
		return
	}
	c.HTML(http.StatusOK, "share_edit.gohtml", data)
}

var (
	errShareShown  = errors.New("The form lists more than the link opens.")
	errShareRemove = errors.New("That cannot be removed from the link.")
)

// shareKey reads a resource as a form names it, its kind and its target.
func shareKey(key string) (share.Resource, bool) {
	kind, target, ok := strings.Cut(key, ":")
	if !ok || target == "" || !slices.Contains([]share.Kind{share.KindProject, share.KindAssistant}, share.Kind(kind)) {
		return share.Resource{}, false
	}
	return share.Resource{Kind: share.Kind(kind), Target: target}, true
}

// shareChange reads a save of a link's Edit: the resources it showed with
// what each project grants now, those taken off, those picked to go on with
// what each of those projects grants, and, with a number, a new expiry. A
// resource that left the link since the form was rendered is passed over.
// The name is never read, a link keeps it.
func (s *Server) shareChange(c *gin.Context, sh share.Share) (share.Change, error) {
	var change share.Change
	shown, removed := c.PostFormArray("resource"), c.PostFormArray("drop")
	if len(shown) > len(sh.Resources) || len(removed) > len(sh.Resources) {
		return change, errShareShown
	}
	for _, key := range removed {
		r, ok := shareKey(key)
		if !ok {
			return change, errShareRemove
		}
		change.Remove = append(change.Remove, r)
	}
	var kept []share.Resource
	for _, key := range shown {
		r, ok := shareKey(key)
		if !ok {
			return change, errShareShown
		}
		if _, on := sh.Resource(r.Kind, r.Target); on && !slices.Contains(removed, key) && r.Kind == share.KindProject {
			kept = append(kept, r)
		}
	}
	projects, assistants, err := s.sharePicks(c)
	if err != nil {
		return change, err
	}
	grants, err := shareGranted(c, len(shown)+len(projects))
	if err != nil {
		return change, err
	}
	for _, r := range kept {
		r.Permissions = grants(r.Target)
		change.Grants = append(change.Grants, r)
	}
	change.Add = shareResources(projects, assistants, grants)
	if strings.TrimSpace(c.PostForm("amount")) != "" {
		if change.Lifetime, err = shareLifetime(c.PostForm("amount"), c.PostForm("unit")); err != nil {
			return change, err
		}
	}
	return change, nil
}

// handleShareSave saves a link's Edit, or revokes the link. A refusal keeps
// the dialog over what was changed, or leads the page back to the Edit.
func (s *Server) handleShareSave(c *gin.Context) {
	if s.shareRefuseLocal(c) {
		return
	}
	sh, ok := s.standingShare(c)
	if !ok {
		s.formRefused(c, settingsSharingPath, errShareGoneLink.Error())
		return
	}
	done := func(message string) {
		if !s.formStayed(c, message) {
			s.redirectWithFlash(c, settingsSharingPath, message, "")
		}
	}
	switch c.PostForm("form") {
	case "save":
		change, err := s.shareChange(c, sh)
		if err == nil {
			err = s.shares.Apply(sh.ID, change)
		}
		if errors.Is(err, share.ErrNotFound) {
			err = errShareGoneLink
		}
		if err != nil {
			s.formRefused(c, shareEditPath(sh.ID), err.Error())
			return
		}
		done("Link saved.")
	case "revoke":
		if err := s.shares.Revoke(sh.ID); err != nil {
			s.formRefused(c, shareEditPath(sh.ID), err.Error())
			return
		}
		done("Link revoked.")
	default:
		s.renderError(c, http.StatusBadRequest, "Unknown action", "That action isn't available on this page.")
	}
}
