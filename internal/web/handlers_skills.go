package web

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/web/render"
)

type skillForm struct {
	SkillID      AlphaNumDashString `form:"skill_id" binding:"required"`
	Description  string             `form:"skill_description" binding:"required"`
	Instructions string             `form:"skill_instructions" binding:"required"`
}

// managedSkillNote is the one sentence every refusal around a managed skill
// carries: the page shows it as the badge's title, the handlers flash it when
// a stale form or a hand-typed URL still tries.
const managedSkillNote = "This skill is managed by the cockpit: it is written at start, kept current, and cannot be edited or deleted here."

func (s *Server) handleSkillsList(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		skills := co.Coder().SkillRepository().List()
		rows := make([]render.SkillRow, 0, len(skills))
		for _, skill := range skills {
			rows = append(rows, render.SkillRow{Skill: skill, Managed: coder.IsManagedSkill(skill.ID)})
		}
		// The cockpit's own skills lead the list, everybody else's follows. The
		// repository already hands them over by name, so a stable sort keeps
		// that order inside both groups.
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Managed && !rows[j].Managed })
		c.HTML(http.StatusOK, "skills_list.gohtml", render.SkillsListData{
			Page:        s.page(c, s.coderTitle(co, "Skills"), "settings"),
			SettingsNav: s.coderSettingsNav("coder", co, "skills"),
			Base:        s.coderBase(co),
			Skills:      rows,
		})
	}
}

func (s *Server) handleSkillNew(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		base := s.coderBase(co)
		c.HTML(http.StatusOK, "skills_form.gohtml", render.SkillsFormData{
			Page:        s.page(c, "Create skill", "settings"),
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
		if coder.IsManagedSkill(id) {
			s.redirectWithFlash(c, base+"/skills", "", managedSkillNote)
			return
		}
		skill, err := co.Coder().SkillRepository().Find(id)
		if err != nil {
			s.redirectWithFlash(c, base+"/skills", "", err.Error())
			return
		}
		c.HTML(http.StatusOK, "skills_form.gohtml", render.SkillsFormData{
			Page:         s.page(c, "Edit skill", "settings"),
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
	// Neither of the managed skill's names may be touched from here: not as
	// the skill being edited, not as the name another one is saved under.
	if coder.IsManagedSkill(originalID) || coder.IsManagedSkill(form.SkillID.String()) {
		s.redirectWithFlash(c, s.coderBase(co)+"/skills", "", managedSkillNote)
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
		if coder.IsManagedSkill(id) {
			s.redirectWithFlash(c, target, "", managedSkillNote)
			return
		}
		skill, err := co.Coder().SkillRepository().Delete(id)
		if err != nil {
			s.redirectWithFlash(c, target, "", err.Error())
			return
		}
		s.redirectWithFlash(c, target, "Skill \""+skill.ID+"\" deleted.", "")
	}
}
