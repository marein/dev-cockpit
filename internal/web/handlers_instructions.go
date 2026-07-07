package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/web/render"
)

type instructionsForm struct {
	Instructions string `form:"instructions"`
}

func (s *Server) handleInstructionsEdit(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		instructions, err := co.Coder().GlobalInstructions().Read()
		if err != nil {
			s.redirectWithFlash(c, "/projects", "", err.Error())
			return
		}
		c.HTML(http.StatusOK, "instructions_form.gohtml", render.InstructionsData{
			Page:         s.page(c, s.coderTitle(co, "Instructions"), "coder"),
			CoderNav:     s.coderNav("instructions", co),
			Base:         s.coderBase(co),
			Instructions: instructions,
		})
	}
}

func (s *Server) handleInstructionsUpdate(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := s.coderBase(co) + "/instructions"
		var form instructionsForm
		if !s.decodeForm(c, &form, target) {
			return
		}
		if err := co.Coder().GlobalInstructions().Save(form.Instructions); err != nil {
			s.redirectWithFlash(c, target, "", err.Error())
			return
		}
		s.redirectWithFlash(c, target, "Instructions saved.", "")
	}
}
