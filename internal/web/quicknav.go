package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/web/render"
)

// quicknav collects the live sessions and shells for the quick nav button. The
// current id comes from the route param, so attach pages can mark the entry you
// are already on. Snapshot is cached and shell listing is a cheap pane scan, so
// this is fine to build on every page render.
func (s *Server) quicknav(c *gin.Context) render.QuickNav {
	return s.buildQuickNav(c.Param("id"), c.Param("name"), c.Request.URL.Path, c.Query("focus"))
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
	id, name, cleanPath, focus := quicknavContextFromPath(path)
	c.HTML(http.StatusOK, "quicknav_items.gohtml", render.Page{
		QuickNav:   s.buildQuickNav(id, name, cleanPath, focus),
		CSRFToken:  s.csrfToken(c),
		MultiCoder: s.multiCoder(),
	})
}

// buildQuickNav assembles the quick nav targets for the given page context. The
// active list shares terminalTabs, so the quick nav and the attach page tab
// strip list the same coders and shells in the same @dc_tab_pos order.
func (s *Server) buildQuickNav(currentID, nameParam, currentPath, focus string) render.QuickNav {
	qn := render.QuickNav{CurrentID: currentID, Focus: focus}
	qn.Active = s.terminalTabs()
	qn.Strip = foldStripTabs(qn.Active)
	qn.UnreadCount = len(s.notifier.UnreadTargets())
	qn.CurrentProject = currentProject(nameParam, qn, focus)
	qn.CurrentPath = currentPath
	// The create forms return here on Cancel; on a split page that must lead
	// back to the pane the user was on, not to the group's first member.
	if focus != "" && strings.HasPrefix(currentPath, "/splits/") {
		qn.CurrentPath = currentPath + "?focus=" + url.QueryEscape(focus)
	}
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
// The path may carry a query (the quick nav client appends ?focus with the
// active split pane); it is split off and returned separately.
func quicknavContextFromPath(rawPath string) (id, name, cleanPath, focus string) {
	cleanPath = rawPath
	if u, err := url.Parse(rawPath); err == nil {
		cleanPath = u.Path
		focus = u.Query().Get("focus")
	}
	parts := strings.Split(strings.Trim(cleanPath, "/"), "/")
	if len(parts) < 2 {
		return "", "", cleanPath, focus
	}
	switch parts[0] {
	case "coders", "shells", "splits":
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
	return id, name, cleanPath, focus
}

// currentProject derives the project of the page being rendered: the project
// route param on project pages, otherwise the project of the live session/shell
// you are attached to. Empty when there is no project context.
func currentProject(nameParam string, qn render.QuickNav, focus string) string {
	if nameParam != "" {
		return nameParam
	}
	if qn.CurrentID == "" {
		return ""
	}
	for _, t := range qn.Active {
		if t.ID == qn.CurrentID {
			return t.Project
		}
	}
	// A split view page: its id is the members' group id. The focused (active)
	// pane's project wins; without one the members' shared project counts.
	if focus != "" {
		for _, t := range qn.Active {
			if t.ID == focus && t.Group == qn.CurrentID {
				return t.Project
			}
		}
	}
	project := ""
	for _, t := range qn.Active {
		if t.Group != qn.CurrentID {
			continue
		}
		if project == "" {
			project = t.Project
		} else if project != t.Project {
			return ""
		}
	}
	return project
}
