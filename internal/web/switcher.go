package web

import (
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/web/render"
)

// switcher collects the live sessions and shells for the quick-switch button.
// The current id comes from the route param, so attach pages can mark the entry
// you are already on. Snapshot is cached and shell listing is a cheap pane scan,
// so this is fine to build on every page render.
func (s *Server) switcher(c *gin.Context) render.Switcher {
	sw := render.Switcher{CurrentID: c.Param("id")}
	for _, r := range s.sessions.Snapshot().Running {
		sw.Sessions = append(sw.Sessions, render.SwitchTarget{
			ID:      r.Identifier,
			Name:    r.Name,
			URL:     "/sessions/" + r.Identifier,
			Project: s.projects.ProjectNameFor(r.CWD),
		})
	}
	for _, sh := range s.shells.List() {
		sw.Shells = append(sw.Shells, render.SwitchTarget{
			ID:      sh.Identifier,
			Name:    sh.Name,
			URL:     "/shells/" + sh.Identifier,
			Project: s.projects.ProjectNameFor(sh.CWD),
		})
	}
	sortByProject(sw.Sessions)
	sortByProject(sw.Shells)
	sw.CurrentProject = s.currentProject(c, sw)
	sw.CurrentPath = c.Request.URL.Path
	return sw
}

// currentProject derives the project of the page being rendered: the :name route
// param on project pages, otherwise the project of the live session/shell you are
// attached to. Empty when there is no project context.
func (s *Server) currentProject(c *gin.Context, sw render.Switcher) string {
	if name := c.Param("name"); name != "" {
		return name
	}
	if sw.CurrentID == "" {
		return ""
	}
	for _, t := range sw.Sessions {
		if t.ID == sw.CurrentID {
			return t.Project
		}
	}
	for _, t := range sw.Shells {
		if t.ID == sw.CurrentID {
			return t.Project
		}
	}
	return ""
}

// sortByProject orders quick-switch targets by project name, then by their own
// name, both case-insensitive.
func sortByProject(targets []render.SwitchTarget) {
	sort.Slice(targets, func(i, j int) bool {
		pi, pj := strings.ToLower(targets[i].Project), strings.ToLower(targets[j].Project)
		if pi != pj {
			return pi < pj
		}
		return strings.ToLower(targets[i].Name) < strings.ToLower(targets[j].Name)
	})
}
