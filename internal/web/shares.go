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
const shareLocalRefusal = "Sharing is the user's to do, in the browser. Ask the user."

// shareMaxAmount bounds the number the form takes, three digits are more than
// the longest lifetime needs in any unit.
const shareMaxAmount = 999

var shareUnits = []render.ShareUnit{
	{Value: "minutes", Label: "Minutes"},
	{Value: "hours", Label: "Hours"},
	{Value: "days", Label: "Days"},
}

// errShareGone answers a project that does not exist, without quoting the name
// the request sent.
var errShareGone = errors.New("This project does not exist, so it cannot be shared.")

// shareProject resolves a project a link is to open as it stands right now.
func (s *Server) shareProject(name string) (project.Project, error) {
	p, err := s.projects.FindByName(name)
	if err != nil || p.Name != name {
		return project.Project{}, errShareGone
	}
	return p, nil
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
	return 0, errors.New("The expiry is counted in minutes, hours or days.")
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
	names := make([]string, 0, len(r.Permissions))
	for _, p := range r.Permissions {
		names = append(names, string(p))
	}
	return "Read, " + strings.Join(names, ", ")
}

func (s *Server) handleSettingsSharing(c *gin.Context) {
	s.renderSettingsSharing(c, nil)
}

// renderSettingsSharing lists the links that still open a project, each with
// the projects that could still go on it. A project the page was opened for,
// from a project's Share, stands preselected wherever a project is picked. A
// project of a link that cannot be read right now is left out, and so is a
// link with none left.
func (s *Server) renderSettingsSharing(c *gin.Context, created *render.ShareCreated) {
	names := []string{}
	for _, p := range s.projects.List() {
		names = append(names, p.Name)
	}
	pick := strings.TrimSpace(c.Query("project"))
	if !slices.Contains(names, pick) {
		pick = ""
	}
	rows := []render.ShareRow{}
	for _, sh := range s.shares.List() {
		live := s.liveShareProjects(sh)
		if len(live) == 0 {
			continue
		}
		row := render.ShareRow{
			ID:        sh.ID,
			Name:      sh.Name,
			CreatedAt: machineTime(sh.CreatedAt),
			ExpiresAt: machineTime(sh.ExpiresAt),
		}
		for _, p := range live {
			r, _ := sh.Resource(share.KindProject, p.Name)
			row.Projects = append(row.Projects, render.ShareProject{Name: p.Name, Grants: shareGrants(r)})
		}
		for _, name := range names {
			if _, on := sh.Resource(share.KindProject, name); !on {
				row.Addable = append(row.Addable, name)
			}
		}
		if slices.Contains(row.Addable, pick) {
			row.Pick = pick
		}
		rows = append(rows, row)
	}
	c.HTML(http.StatusOK, "settings_sharing.gohtml", render.SettingsSharingData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("sharing"),
		Projects:    names,
		Pick:        pick,
		Units:       shareUnits,
		NameMax:     share.MaxNameLength,
		Shares:      rows,
		Created:     created,
	})
}

func (s *Server) handleSettingsSharingSave(c *gin.Context) {
	if s.localCall(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": shareLocalRefusal})
		return
	}
	switch c.PostForm("form") {
	case "create":
		s.createShare(c)
	case "update", "add", "remove", "revoke":
		s.changeShare(c)
	default:
		s.renderError(c, http.StatusBadRequest, "Unknown action", "That action isn't available on this page.")
	}
}

// createShare makes a link and shows it, this once: only its hash is stored,
// so the answer is the one place the token ever stands.
func (s *Server) createShare(c *gin.Context) {
	formPath := settingsSharingPath
	p, err := s.shareProject(strings.TrimSpace(c.PostForm("project")))
	if err != nil {
		s.redirectWithAnchoredFlash(c, formPath, "share-new", "", err.Error())
		return
	}
	formPath += "?" + url.Values{"project": {p.Name}}.Encode()
	lifetime, err := shareLifetime(c.PostForm("amount"), c.PostForm("unit"))
	if err != nil {
		s.redirectWithAnchoredFlash(c, formPath, "share-new", "", err.Error())
		return
	}
	created, token, err := s.shares.Create(c.PostForm("name"), []share.Resource{{Kind: share.KindProject, Target: p.Name}}, lifetime)
	if err != nil {
		s.redirectWithAnchoredFlash(c, formPath, "share-new", "", err.Error())
		return
	}
	c.Header("Cache-Control", "no-store")
	s.renderSettingsSharing(c, &render.ShareCreated{
		URL:       shareURL(c, token),
		Projects:  created.Targets(share.KindProject),
		ExpiresAt: machineTime(created.ExpiresAt),
	})
}

// changeShare runs one change of a standing link: its name and lifetime, a
// project on or off it, its revoke. The answer stands at the link's card,
// which only an id of a standing link names.
func (s *Server) changeShare(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))
	anchor := ""
	for _, sh := range s.shares.List() {
		if sh.ID == id {
			anchor = "share-" + id
		}
	}
	done := func(message string, err error) {
		errMsg := ""
		switch {
		case errors.Is(err, share.ErrNotFound):
			errMsg, anchor = "That link no longer stands.", ""
		case err != nil:
			errMsg = err.Error()
		}
		if anchor == "" {
			s.redirectWithFlash(c, settingsSharingPath, message, errMsg)
			return
		}
		s.redirectWithAnchoredFlash(c, settingsSharingPath, anchor, message, errMsg)
	}
	switch c.PostForm("form") {
	case "update":
		var lifetime time.Duration
		if strings.TrimSpace(c.PostForm("amount")) != "" {
			var err error
			if lifetime, err = shareLifetime(c.PostForm("amount"), c.PostForm("unit")); err != nil {
				done("", err)
				return
			}
		}
		if err := s.shares.Update(id, c.PostForm("name"), lifetime); err != nil {
			done("", err)
			return
		}
		done("Link saved.", nil)
	case "add":
		p, err := s.shareProject(strings.TrimSpace(c.PostForm("project")))
		if err == nil {
			err = s.shares.Add(id, share.Resource{Kind: share.KindProject, Target: p.Name})
		}
		if err != nil {
			done("", err)
			return
		}
		done("Project added, the link opens it now.", nil)
	case "remove":
		ended, err := s.shares.Remove(id, share.KindProject, strings.TrimSpace(c.PostForm("project")))
		if err != nil {
			done("", err)
			return
		}
		if ended {
			anchor = ""
			done("The link ended with its last project.", nil)
			return
		}
		done("Project removed, the link no longer opens it.", nil)
	case "revoke":
		if err := s.shares.Revoke(id); err != nil {
			done("", err)
			return
		}
		anchor = ""
		done("Link revoked.", nil)
	}
}
