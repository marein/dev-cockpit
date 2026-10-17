package web

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
)

const (
	settingsSharingPath = "/settings/sharing"
	sharePathPrefix     = "/share/"
)

// shareLocalRefusal is what a local call reads when it tries to change a
// link: a share opens projects to somebody outside the cockpit, which no
// assistant may do on its own.
const shareLocalRefusal = "Only the user shares, in the browser. Ask the user."

// shareMaxAmount bounds the number the form takes, three digits are more than
// the longest lifetime needs in any unit.
const shareMaxAmount = 999

var shareUnits = []render.ShareUnit{
	{Value: "minutes", Label: "minutes"},
	{Value: "hours", Label: "hours"},
	{Value: "days", Label: "days"},
}

// sharePermissions are the permissions a link grants on a project today, in
// the order of share.All. One that share knows and no route keeps yet is
// offered nowhere and refused from a hand built form, so no link holds a
// grant before the cockpit enforces it.
var sharePermissions = []render.SharePermission{
	{Value: share.Comments, Label: "Comment", Hint: "Add line comments, and edit and delete their own.", Icon: "ti-message-circle"},
}

var errShareGrant = errors.New("A link cannot allow that.")

func sharePermission(value string) (render.SharePermission, bool) {
	for _, p := range sharePermissions {
		if string(p.Value) == value {
			return p, true
		}
	}
	return render.SharePermission{}, false
}

// shareGranted reads what a form grants each project it lists, New link's
// and Edit's alike: a field per permission offered, its values the projects
// switched on, at most limit of them. A value naming no project listed
// grants nothing.
func shareGranted(c *gin.Context, limit int) (func(project string) []share.Permission, error) {
	fields := make(map[share.Permission][]string, len(sharePermissions))
	for _, p := range sharePermissions {
		values := c.PostFormArray("grant:" + string(p.Value))
		if len(values) > limit {
			return nil, errShareGrant
		}
		fields[p.Value] = values
	}
	return func(project string) []share.Permission {
		granted := []share.Permission{}
		for _, p := range sharePermissions {
			if slices.Contains(fields[p.Value], project) {
				granted = append(granted, p.Value)
			}
		}
		return granted
	}, nil
}

// shareOffers are the permissions a project can be granted, each on that
// grants or picks it.
func shareOffers(on func(share.Permission) bool) []render.SharePermission {
	out := make([]render.SharePermission, 0, len(sharePermissions))
	for _, p := range sharePermissions {
		p.On = on(p.Value)
		out = append(out, p)
	}
	return out
}

// errShareGone answers a project that does not exist, without quoting the name
// the request sent.
var errShareGone = errors.New("That project does not exist.")

// shareProject resolves a project a link is to open as it stands right now.
func (s *Server) shareProject(name string) (project.Project, error) {
	p, err := s.projects.FindByName(name)
	if err != nil || p.Name != name {
		return project.Project{}, errShareGone
	}
	return p, nil
}

var (
	errSharePick          = errors.New("Pick at least one project or assistant for the link.")
	errShareMany          = errors.New("Too many projects.")
	errShareManyAssistant = errors.New("Too many assistants.")
	errShareNoAssistant   = errors.New("That assistant does not exist.")
)

// shareProjects resolves the projects a link is to open, in the order picked,
// and answers those it resolved before any refusal. A pick can name no more
// projects than there are, which bounds the lookups one request makes.
func (s *Server) shareProjects(names []string) ([]string, error) {
	if len(names) > len(s.projects.List()) {
		return nil, errShareMany
	}
	picked := make([]string, 0, len(names))
	for _, name := range names {
		p, err := s.shareProject(strings.TrimSpace(name))
		if err != nil {
			return picked, err
		}
		picked = append(picked, p.Name)
	}
	return picked, nil
}

// shareAssistants resolves the assistants a link is to open by id, in the
// order picked, the way shareProjects resolves projects.
func (s *Server) shareAssistants(ids []string) ([]string, error) {
	if len(ids) > len(s.assistants.List()) {
		return nil, errShareManyAssistant
	}
	picked := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if !assistant.ValidID(id) {
			return picked, errShareNoAssistant
		}
		if _, err := s.assistants.Get(id); err != nil {
			return picked, errShareNoAssistant
		}
		picked = append(picked, id)
	}
	return picked, nil
}

// sharePicks resolves what a form picks to share, the projects first and the
// assistants after them, and answers what it resolved before any refusal.
// Nothing picked is no refusal here, the form that makes a link asks for one.
func (s *Server) sharePicks(c *gin.Context) (projects, assistants []string, err error) {
	projects, err = s.shareProjects(c.PostFormArray("project"))
	if err != nil {
		return projects, nil, err
	}
	assistants, err = s.shareAssistants(c.PostFormArray("assistant"))
	return projects, assistants, err
}

// shareResources are the resources of the projects and the assistants
// picked, each project granting what grants answers for it, each assistant
// read only.
func shareResources(projects, assistants []string, grants func(string) []share.Permission) []share.Resource {
	resources := make([]share.Resource, 0, len(projects)+len(assistants))
	for _, name := range projects {
		resources = append(resources, share.Resource{Kind: share.KindProject, Target: name, Permissions: grants(name)})
	}
	for _, id := range assistants {
		resources = append(resources, share.Resource{Kind: share.KindAssistant, Target: id})
	}
	return resources
}

// liveShareProject answers a project of a link as it stands right now. A
// project gone from disk leaves every link, however it went, and a link left
// with nothing ends with it. Any other failed read keeps it, it may pass.
func (s *Server) liveShareProject(sh share.Share, name string) (project.Project, bool) {
	if _, ok := sh.Resource(share.KindProject, name); !ok {
		return project.Project{}, false
	}
	p, err := s.projects.FindByName(name)
	if errors.Is(err, project.ErrMissing) {
		s.unshareProject(name)
	}
	if err != nil || p.Name != name {
		return project.Project{}, false
	}
	return p, true
}

// unshareProject takes a project off every link, which ends a link it was the
// last project of.
func (s *Server) unshareProject(name string) {
	s.shares.RemoveTarget(share.KindProject, name)
}

// liveShareAssistant answers an assistant of a link as it stands right now.
// An assistant deleted for certain leaves every link, however it went, and a
// link left with nothing ends with it. A transcript that cannot be read keeps
// it, it may pass.
func (s *Server) liveShareAssistant(sh share.Share, id string) (assistant.Instance, bool) {
	if _, ok := sh.Resource(share.KindAssistant, id); !ok {
		return assistant.Instance{}, false
	}
	inst, err := s.assistants.Get(id)
	if err != nil {
		if s.assistants.Gone(id) {
			s.unshareAssistant(id)
		}
		return assistant.Instance{}, false
	}
	return inst, true
}

// unshareAssistant takes an assistant off every link, which ends a link it
// was the last resource of.
func (s *Server) unshareAssistant(id string) {
	s.shares.RemoveTarget(share.KindAssistant, id)
	s.guestFiles.drop(id)
}

// liveShareAssistants answers the assistants of a link that stand right now,
// in the order they went on it.
func (s *Server) liveShareAssistants(sh share.Share) []assistant.Instance {
	var out []assistant.Instance
	for _, id := range sh.Targets(share.KindAssistant) {
		if inst, ok := s.liveShareAssistant(sh, id); ok {
			out = append(out, inst)
		}
	}
	return out
}

// liveShareProjects answers the projects of a link that stand right now, in
// the order they went on it.
func (s *Server) liveShareProjects(sh share.Share) []project.Project {
	var out []project.Project
	for _, name := range sh.Targets(share.KindProject) {
		if p, ok := s.liveShareProject(sh, name); ok {
			out = append(out, p)
		}
	}
	return out
}

func shareLifetime(rawAmount, unit string) (time.Duration, error) {
	amount, err := strconv.Atoi(strings.TrimSpace(rawAmount))
	if err != nil || amount < 1 || amount > shareMaxAmount {
		return 0, errors.New("The expiry is a whole number from 1 to 999.")
	}
	switch unit {
	case "minutes":
		return time.Duration(amount) * time.Minute, nil
	case "hours":
		return time.Duration(amount) * time.Hour, nil
	case "days":
		return time.Duration(amount) * 24 * time.Hour, nil
	}
	return 0, errors.New("The expiry unit is minutes, hours or days.")
}

// shareURL is the address a guest opens, under the address the request that
// makes the link came in on.
func shareURL(c *gin.Context, token string) string {
	scheme := "http"
	if requestIsSecure(c) {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host + sharePathPrefix + token
}

// shareGrants words what a link grants on one of its resources.
func shareGrants(r share.Resource) string {
	if len(r.Permissions) == 0 {
		return "Read only"
	}
	names := []string{"Read"}
	for _, p := range r.Permissions {
		name := string(p)
		if offered, ok := sharePermission(name); ok {
			name = strings.ToLower(offered.Label)
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}
