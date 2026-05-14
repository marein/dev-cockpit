package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/web/render"
)

type agentForm struct {
	AgentID      AlphaNumDashString `form:"agent_id" binding:"required"`
	Description  string             `form:"agent_description" binding:"required"`
	Instructions string             `form:"agent_instructions" binding:"required"`
}

func (s *Server) handleAgentsList(c *gin.Context) {
	c.HTML(http.StatusOK, "agents_list.gohtml", render.AgentsListData{
		Page:   s.page(c, "Agents", "agents"),
		Agents: s.provider.AgentRepository().List(),
	})
}

func (s *Server) handleAgentNew(c *gin.Context) {
	c.HTML(http.StatusOK, "agents_form.gohtml", render.AgentsFormData{
		Page:        s.page(c, "Create agent", "agents"),
		FormAction:  "/agents",
		SubmitLabel: "Create agent",
		Heading:     "Create agent",
	})
}

func (s *Server) handleAgentEdit(c *gin.Context) {
	id := c.Param("id")
	a, err := s.provider.AgentRepository().Find(id)
	if err != nil {
		s.redirectWithFlash(c, "/agents", "", err.Error())
		return
	}
	c.HTML(http.StatusOK, "agents_form.gohtml", render.AgentsFormData{
		Page:         s.page(c, "Edit agent", "agents"),
		IsEdit:       true,
		OriginalID:   a.ID,
		ID:           a.ID,
		Description:  a.Description,
		Instructions: a.Instructions,
		FormAction:   "/agents/" + a.ID,
		SubmitLabel:  "Save agent",
		Heading:      "Edit agent",
	})
}

func (s *Server) saveAgent(c *gin.Context, originalID, redirectBack string) {
	var form agentForm
	if !s.decodeForm(c, &form, redirectBack) {
		return
	}
	res, err := s.provider.AgentRepository().Save(originalID, form.AgentID.String(), form.Description, form.Instructions)
	if err != nil {
		s.redirectWithFlash(c, redirectBack, "", err.Error())
		return
	}
	msg := "Agent \"" + res.Saved.ID + "\" saved."
	if res.Created {
		msg = "Agent \"" + res.Saved.ID + "\" created."
	}
	s.redirectWithFlash(c, "/agents", msg, "")
}

func (s *Server) handleAgentCreate(c *gin.Context) {
	s.saveAgent(c, "", "/agents/new")
}

func (s *Server) handleAgentUpdate(c *gin.Context) {
	id := c.Param("id")
	s.saveAgent(c, id, "/agents/"+id+"/edit")
}

func (s *Server) handleAgentDelete(c *gin.Context) {
	id := c.Param("id")
	a, err := s.provider.AgentRepository().Delete(id)
	if err != nil {
		s.redirectWithFlash(c, "/agents", "", err.Error())
		return
	}
	s.redirectWithFlash(c, "/agents", "Agent \""+a.ID+"\" deleted.", "")
}
