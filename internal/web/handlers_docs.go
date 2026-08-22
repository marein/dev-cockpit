package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/web/render"
)

func (s *Server) handleDocs(c *gin.Context) {
	c.HTML(http.StatusOK, "docs.gohtml", render.DocsData{
		Page:   s.page(c, "Documentation", "docs"),
		Lead:   render.DocsLead,
		Topics: render.DocsTopics(),
	})
}
