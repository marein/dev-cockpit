package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// handleStatusLinks answers the status line's links chip as a fragment. The
// page's path comes along like the tab strip's, so the project the chip
// unfolds is the one the page is about, resolved the same way as at render.
func (s *Server) handleStatusLinks(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		path = "/"
	}
	id, name, cleanPath, focus := quicknavContextFromPath(path)
	qn := s.buildQuickNav(id, name, cleanPath, focus)
	c.HTML(http.StatusOK, "status_links.gohtml", render.Page{
		Links: render.NewStatusLinks(qn.AllProjects, qn.CurrentProject),
	})
}
