package web

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/web/render"
)

// quicknav collects the live sessions and shells for the quick nav button. The
// current id comes from the route param, so attach pages can mark the entry you
// are already on. Snapshot is cached and shell listing is a cheap pane scan, so
// this is fine to build on every page render.
func (s *Server) quicknav(c *gin.Context) render.QuickNav {
	return s.buildQuickNav(c.Param("id"), c.Param("name"), c.Request.URL.Path)
}

// handleQuickNav renders just the menu items so the quick nav button can pull a
// fresh list every time it opens, instead of showing whatever was live when the
// page was first rendered. The page context (which entry is current, the return
// target for the create links) is reconstructed from the path the client is on.
func (s *Server) handleQuickNav(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		path = "/"
	}
	id, name := quicknavContextFromPath(path)
	c.HTML(http.StatusOK, "quicknav_items.gohtml", render.Page{
		QuickNav:  s.buildQuickNav(id, name, path),
		CSRFToken: s.csrfToken(c),
	})
}

// buildQuickNav assembles the quick nav targets for the given page context.
func (s *Server) buildQuickNav(currentID, nameParam, currentPath string) render.QuickNav {
	qn := render.QuickNav{CurrentID: currentID}
	for _, r := range s.sessions.Snapshot().Running {
		qn.Sessions = append(qn.Sessions, render.QuickNavTarget{
			ID:      r.Identifier,
			Name:    r.Name,
			URL:     "/sessions/" + r.Identifier,
			Project: s.projects.ProjectNameFor(r.CWD),
		})
	}
	for _, sh := range s.shells.List() {
		qn.Shells = append(qn.Shells, render.QuickNavTarget{
			ID:      sh.Identifier,
			Name:    sh.Name,
			URL:     "/shells/" + sh.Identifier,
			Project: s.projects.ProjectNameFor(sh.CWD),
		})
	}
	sortByProject(qn.Sessions)
	sortByProject(qn.Shells)
	qn.CurrentProject = currentProject(nameParam, qn)
	qn.CurrentPath = currentPath
	qn.AllProjects = s.projectBrowser(currentPath)
	for i := range qn.AllProjects {
		if qn.AllProjects[i].Name == qn.CurrentProject {
			qn.CurrentProjectPath = qn.AllProjects[i].Path
			break
		}
	}
	return qn
}

// quicknavContextFromPath recovers the route params buildQuickNav needs from a
// raw request path, matching what Gin would have bound on a full page render.
func quicknavContextFromPath(path string) (id, name string) {
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
func currentProject(nameParam string, qn render.QuickNav) string {
	if nameParam != "" {
		return nameParam
	}
	if qn.CurrentID == "" {
		return ""
	}
	for _, t := range qn.Sessions {
		if t.ID == qn.CurrentID {
			return t.Project
		}
	}
	for _, t := range qn.Shells {
		if t.ID == qn.CurrentID {
			return t.Project
		}
	}
	return ""
}

// sortByProject orders quick nav targets by project name, then by their own
// name, both case-insensitive.
func sortByProject(targets []render.QuickNavTarget) {
	sort.Slice(targets, func(i, j int) bool {
		pi, pj := strings.ToLower(targets[i].Project), strings.ToLower(targets[j].Project)
		if pi != pj {
			return pi < pj
		}
		return strings.ToLower(targets[i].Name) < strings.ToLower(targets[j].Name)
	})
}
