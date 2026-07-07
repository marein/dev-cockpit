package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/web/render"
)

type agentForm struct {
	AgentID      AlphaNumDashString `form:"agent_id" binding:"required"`
	Description  string             `form:"agent_description" binding:"required"`
	Instructions string             `form:"agent_instructions" binding:"required"`
}

func (s *Server) handleAgentsList(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, "agents_list.gohtml", render.AgentsListData{
			Page:     s.page(c, s.coderTitle(co, "Agents"), "coder"),
			CoderNav: s.coderNav("agents", co),
			Base:     s.coderBase(co),
			Agents:   co.Coder().AgentRepository().List(),
		})
	}
}

func (s *Server) handleAgentNew(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		base := s.coderBase(co)
		c.HTML(http.StatusOK, "agents_form.gohtml", render.AgentsFormData{
			Page:        s.page(c, "Create agent", "coder"),
			Base:        base,
			FormAction:  base + "/agents",
			SubmitLabel: "Create agent",
			Heading:     "Create agent",
		})
	}
}

func (s *Server) handleAgentEdit(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		base := s.coderBase(co)
		id := c.Param("id")
		a, err := co.Coder().AgentRepository().Find(id)
		if err != nil {
			s.redirectWithFlash(c, base+"/agents", "", err.Error())
			return
		}
		c.HTML(http.StatusOK, "agents_form.gohtml", render.AgentsFormData{
			Page:         s.page(c, "Edit agent", "coder"),
			Base:         base,
			IsEdit:       true,
			OriginalID:   a.ID,
			ID:           a.ID,
			Description:  a.Description,
			Instructions: a.Instructions,
			FormAction:   base + "/agents/" + a.ID,
			SubmitLabel:  "Save agent",
			Heading:      "Edit agent",
		})
	}
}

func (s *Server) saveAgent(c *gin.Context, co *coder.Manager, originalID, redirectBack string) {
	var form agentForm
	if !s.decodeForm(c, &form, redirectBack) {
		return
	}
	res, err := co.Coder().AgentRepository().Save(originalID, form.AgentID.String(), form.Description, form.Instructions)
	if err != nil {
		s.redirectWithFlash(c, redirectBack, "", err.Error())
		return
	}
	msg := "Agent \"" + res.Saved.ID + "\" saved."
	if res.Created {
		msg = "Agent \"" + res.Saved.ID + "\" created."
	}
	s.redirectWithFlash(c, s.coderBase(co)+"/agents", msg, "")
}

func (s *Server) handleAgentCreate(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		s.saveAgent(c, co, "", s.coderBase(co)+"/agents/new")
	}
}

func (s *Server) handleAgentUpdate(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		s.saveAgent(c, co, id, s.coderBase(co)+"/agents/"+id+"/edit")
	}
}

func (s *Server) handleAgentDelete(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := s.coderBase(co) + "/agents"
		id := c.Param("id")
		a, err := co.Coder().AgentRepository().Delete(id)
		if err != nil {
			s.redirectWithFlash(c, target, "", err.Error())
			return
		}
		s.redirectWithFlash(c, target, "Agent \""+a.ID+"\" deleted.", "")
	}
}
