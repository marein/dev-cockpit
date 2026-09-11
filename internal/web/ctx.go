package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// handleCtx renders the list column of one area on its own, for the phone's
// sheet: the same partials the pages render, with the same data. The path
// query names the page the sheet opens over, so the terminals column marks
// the current terminal, the create links carry its project and the settings
// column marks the section on screen.
func (s *Server) handleCtx(c *gin.Context) {
	area := c.Param("area")
	path := c.Query("path")
	if path == "" {
		path = "/"
	}
	page := s.page(c, "", area)
	id, name, cleanPath, focus := quicknavContextFromPath(path)
	page.QuickNav = s.buildQuickNav(id, name, cleanPath, focus)
	switch area {
	case "projects":
		s.shells.Invalidate()
		// The docker presence comes along because the count badge wears a
		// compose action of the project, the same way it wears a working
		// session. It is the page's own join, one cache read, no daemon call.
		projects := s.projectsWithRunners()
		c.HTML(http.StatusOK, "ctx_projects.gohtml", render.ProjectsListData{Page: page, Projects: projects, Docker: s.dockerByProject(projects)})
	case "terminals":
		c.HTML(http.StatusOK, "ctx_terminals.gohtml", page)
	case "settings":
		c.HTML(http.StatusOK, "ctx_settings.gohtml", render.SettingsGeneralData{Page: page, SettingsNav: s.settingsNav(settingsSectionOf(cleanPath))})
	case "docs":
		c.HTML(http.StatusOK, "ctx_docs.gohtml", render.DocsData{Page: page, Topics: render.DocsTopics()})
	case "assistant":
		c.HTML(http.StatusOK, "ctx_assistant.gohtml", s.assistantCtxData(c, cleanPath, "assistant-sheet-new"))
	default:
		c.Status(http.StatusNotFound)
	}
}

// settingsSectionOf names the settings section a path shows, the way the
// column marks its active row: the first segment after /settings, coders
// folded onto the coder row.
func settingsSectionOf(path string) string {
	rest := strings.TrimPrefix(path, "/settings/")
	if rest == path {
		return ""
	}
	section := strings.SplitN(rest, "/", 2)[0]
	if section == "coders" {
		return "coder"
	}
	return section
}
