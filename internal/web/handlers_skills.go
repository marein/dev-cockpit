package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/web/render"
)

type skillForm struct {
	SkillID      AlphaNumDashString `form:"skill_id" binding:"required"`
	Description  string             `form:"skill_description" binding:"required"`
	Instructions string             `form:"skill_instructions" binding:"required"`
}

func (s *Server) handleSkillsList(c *gin.Context) {
	co, err := s.coderFromRequest(c)
	if err != nil {
		s.redirectWithFlash(c, "/skills", "", err.Error())
		return
	}
	c.HTML(http.StatusOK, "skills_list.gohtml", render.SkillsListData{
		Page:       s.page(c, "Skills", "skills"),
		CoderTabs:  s.coderTabs("/skills", co),
		CoderQuery: s.coderQuery(co),
		Skills:     co.Coder().SkillRepository().List(),
	})
}

func (s *Server) handleSkillNew(c *gin.Context) {
	co, err := s.coderFromRequest(c)
	if err != nil {
		s.redirectWithFlash(c, "/skills", "", err.Error())
		return
	}
	c.HTML(http.StatusOK, "skills_form.gohtml", render.SkillsFormData{
		Page:          s.page(c, "Create skill", "skills"),
		SelectedCoder: co.ID(),
		CoderQuery:    s.coderQuery(co),
		FormAction:    "/skills",
		SubmitLabel:   "Create skill",
		Heading:       "Create skill",
	})
}

func (s *Server) handleSkillEdit(c *gin.Context) {
	co, err := s.coderFromRequest(c)
	if err != nil {
		s.redirectWithFlash(c, "/skills", "", err.Error())
		return
	}
	id := c.Param("id")
	skill, err := co.Coder().SkillRepository().Find(id)
	if err != nil {
		s.redirectWithFlash(c, "/skills"+s.coderQuery(co), "", err.Error())
		return
	}
	c.HTML(http.StatusOK, "skills_form.gohtml", render.SkillsFormData{
		Page:          s.page(c, "Edit skill", "skills"),
		SelectedCoder: co.ID(),
		CoderQuery:    s.coderQuery(co),
		IsEdit:        true,
		OriginalID:    skill.ID,
		ID:            skill.ID,
		Description:   skill.Description,
		Instructions:  skill.Instructions,
		FormAction:    "/skills/" + skill.ID,
		SubmitLabel:   "Save skill",
		Heading:       "Edit skill",
	})
}

func (s *Server) saveSkill(c *gin.Context, originalID, redirectBack string) {
	co, err := s.coderFromRequest(c)
	if err != nil {
		s.redirectWithFlash(c, "/skills", "", err.Error())
		return
	}
	var form skillForm
	if !s.decodeForm(c, &form, redirectBack+s.coderQuery(co)) {
		return
	}
	res, err := co.Coder().SkillRepository().Save(originalID, form.SkillID.String(), form.Description, form.Instructions)
	if err != nil {
		s.redirectWithFlash(c, redirectBack+s.coderQuery(co), "", err.Error())
		return
	}
	msg := "Skill \"" + res.Saved.ID + "\" saved."
	if res.Created {
		msg = "Skill \"" + res.Saved.ID + "\" created."
	}
	s.redirectWithFlash(c, "/skills"+s.coderQuery(co), msg, "")
}

func (s *Server) handleSkillCreate(c *gin.Context) {
	s.saveSkill(c, "", "/skills/new")
}

func (s *Server) handleSkillUpdate(c *gin.Context) {
	id := c.Param("id")
	s.saveSkill(c, id, "/skills/"+id+"/edit")
}

func (s *Server) handleSkillDelete(c *gin.Context) {
	co, err := s.coderFromRequest(c)
	if err != nil {
		s.redirectWithFlash(c, "/skills", "", err.Error())
		return
	}
	id := c.Param("id")
	skill, err := co.Coder().SkillRepository().Delete(id)
	if err != nil {
		s.redirectWithFlash(c, "/skills"+s.coderQuery(co), "", err.Error())
		return
	}
	s.redirectWithFlash(c, "/skills"+s.coderQuery(co), "Skill \""+skill.ID+"\" deleted.", "")
}
