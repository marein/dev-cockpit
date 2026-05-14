package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/web/render"
)

type instructionsForm struct {
	Instructions string `form:"instructions"`
}

func (s *Server) handleInstructionsEdit(c *gin.Context) {
	instructions, err := s.provider.GlobalInstructions().Read()
	if err != nil {
		s.redirectWithFlash(c, "/sessions", "", err.Error())
		return
	}
	c.HTML(http.StatusOK, "instructions_form.gohtml", render.InstructionsData{
		Page:         s.page(c, "Instructions", "instructions"),
		Provider:     s.provider.ID(),
		Instructions: instructions,
	})
}

func (s *Server) handleInstructionsUpdate(c *gin.Context) {
	var form instructionsForm
	if !s.decodeForm(c, &form, "/instructions") {
		return
	}
	if err := s.provider.GlobalInstructions().Save(form.Instructions); err != nil {
		s.redirectWithFlash(c, "/instructions", "", err.Error())
		return
	}
	s.redirectWithFlash(c, "/instructions", "Instructions saved.", "")
}
