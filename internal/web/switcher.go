package web

import (
	"net/http"
	"net/url"
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
	return s.buildSwitcher(c.Param("id"), c.Param("name"), c.Request.URL.Path)
}

// handleSwitcher renders just the menu items so the quick-switch button can pull
// a fresh list every time it opens, instead of showing whatever was live when the
// page was first rendered. The page context (which entry is current, the return
// target for the create links) is reconstructed from the path the client is on.
func (s *Server) handleSwitcher(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		path = "/"
	}
	id, name := switchContextFromPath(path)
	c.HTML(http.StatusOK, "session_switcher_items.gohtml", render.Page{
		Switcher: s.buildSwitcher(id, name, path),
	})
}

// buildSwitcher assembles the quick-switch targets for the given page context.
func (s *Server) buildSwitcher(currentID, nameParam, currentPath string) render.Switcher {
	sw := render.Switcher{CurrentID: currentID}
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
	sw.CurrentProject = currentProject(nameParam, sw)
	sw.CurrentPath = currentPath
	return sw
}

// switchContextFromPath recovers the route params buildSwitcher needs from a raw
// request path, matching what Gin would have bound on a full page render.
func switchContextFromPath(path string) (id, name string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return "", ""
	}
	switch parts[0] {
	case "sessions", "shells":
		if parts[1] != "new" {
			id = parts[1]
		}
	case "projects":
		if len(parts) >= 3 && parts[2] == "editor" {
			if decoded, err := url.PathUnescape(parts[1]); err == nil {
				name = decoded
			}
		}
	}
	return id, name
}

// currentProject derives the project of the page being rendered: the project
// route param on project pages, otherwise the project of the live session/shell
// you are attached to. Empty when there is no project context.
func currentProject(nameParam string, sw render.Switcher) string {
	if nameParam != "" {
		return nameParam
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
