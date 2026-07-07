package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/web/render"
)

type skillForm struct {
	SkillID      AlphaNumDashString `form:"skill_id" binding:"required"`
	Description  string             `form:"skill_description" binding:"required"`
	Instructions string             `form:"skill_instructions" binding:"required"`
}

func (s *Server) handleSkillsList(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, "skills_list.gohtml", render.SkillsListData{
			Page:     s.page(c, s.coderTitle(co, "Skills"), "coder"),
			CoderNav: s.coderNav("skills", co),
			Base:     s.coderBase(co),
			Skills:   co.Coder().SkillRepository().List(),
		})
	}
}

func (s *Server) handleSkillNew(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		base := s.coderBase(co)
		c.HTML(http.StatusOK, "skills_form.gohtml", render.SkillsFormData{
			Page:        s.page(c, "Create skill", "coder"),
			Base:        base,
			FormAction:  base + "/skills",
			SubmitLabel: "Create skill",
			Heading:     "Create skill",
		})
	}
}

func (s *Server) handleSkillEdit(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		base := s.coderBase(co)
		id := c.Param("id")
		skill, err := co.Coder().SkillRepository().Find(id)
		if err != nil {
			s.redirectWithFlash(c, base+"/skills", "", err.Error())
			return
		}
		c.HTML(http.StatusOK, "skills_form.gohtml", render.SkillsFormData{
			Page:         s.page(c, "Edit skill", "coder"),
			Base:         base,
			IsEdit:       true,
			OriginalID:   skill.ID,
			ID:           skill.ID,
			Description:  skill.Description,
			Instructions: skill.Instructions,
			FormAction:   base + "/skills/" + skill.ID,
			SubmitLabel:  "Save skill",
			Heading:      "Edit skill",
		})
	}
}

func (s *Server) saveSkill(c *gin.Context, co *coder.Manager, originalID, redirectBack string) {
	var form skillForm
	if !s.decodeForm(c, &form, redirectBack) {
		return
	}
	res, err := co.Coder().SkillRepository().Save(originalID, form.SkillID.String(), form.Description, form.Instructions)
	if err != nil {
		s.redirectWithFlash(c, redirectBack, "", err.Error())
		return
	}
	msg := "Skill \"" + res.Saved.ID + "\" saved."
	if res.Created {
		msg = "Skill \"" + res.Saved.ID + "\" created."
	}
	s.redirectWithFlash(c, s.coderBase(co)+"/skills", msg, "")
}

func (s *Server) handleSkillCreate(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		s.saveSkill(c, co, "", s.coderBase(co)+"/skills/new")
	}
}

func (s *Server) handleSkillUpdate(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		s.saveSkill(c, co, id, s.coderBase(co)+"/skills/"+id+"/edit")
	}
}

func (s *Server) handleSkillDelete(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := s.coderBase(co) + "/skills"
		id := c.Param("id")
		skill, err := co.Coder().SkillRepository().Delete(id)
		if err != nil {
			s.redirectWithFlash(c, target, "", err.Error())
			return
		}
		s.redirectWithFlash(c, target, "Skill \""+skill.ID+"\" deleted.", "")
	}
}
