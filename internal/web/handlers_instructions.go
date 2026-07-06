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
	co, err := s.coderFromRequest(c)
	if err != nil {
		s.redirectWithFlash(c, "/instructions", "", err.Error())
		return
	}
	instructions, err := co.Coder().GlobalInstructions().Read()
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	c.HTML(http.StatusOK, "instructions_form.gohtml", render.InstructionsData{
		Page:          s.page(c, "Instructions", "instructions"),
		CoderTabs:     s.coderTabs("/instructions", co),
		SelectedCoder: co.ID(),
		Instructions:  instructions,
	})
}

func (s *Server) handleInstructionsUpdate(c *gin.Context) {
	co, err := s.coderFromRequest(c)
	if err != nil {
		s.redirectWithFlash(c, "/instructions", "", err.Error())
		return
	}
	var form instructionsForm
	if !s.decodeForm(c, &form, "/instructions"+s.coderQuery(co)) {
		return
	}
	if err := co.Coder().GlobalInstructions().Save(form.Instructions); err != nil {
		s.redirectWithFlash(c, "/instructions"+s.coderQuery(co), "", err.Error())
		return
	}
	s.redirectWithFlash(c, "/instructions"+s.coderQuery(co), "Instructions saved.", "")
}
