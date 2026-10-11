package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// handleProjectTags renders a main project's tags form, as a page and alone
// for the dialog. A worktree gets no form, it carries no tags.
func (s *Server) handleProjectTags(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	if err := project.TagsRefused(p); err != nil {
		s.redirectWithProjectFlash(c, p.Name, "", err.Error())
		return
	}
	data := s.projectTagsData(c, p)
	if data.Modal {
		c.HTML(http.StatusOK, "project_tags_form.gohtml", data)
		return
	}
	c.HTML(http.StatusOK, "project_tags.gohtml", data)
}

func (s *Server) handleProjectTagsSave(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		s.formRefused(c, "/projects", err.Error())
		return
	}
	if err := project.TagsRefused(p); err != nil {
		if inFormModal(c) || wantsJSON(c.Request) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		s.redirectWithProjectFlash(c, p.Name, "", err.Error())
		return
	}
	if err := readForm(c); err != nil {
		s.refuseProjectTags(c, p, nil, err.Error())
		return
	}
	if err := s.projects.SetTags(p, sentPick(c.Request.PostForm)); err != nil {
		s.refuseProjectTags(c, p, c.Request.PostForm, err.Error())
		return
	}
	s.publishProjects()
	message := fmt.Sprintf("Tags of %q saved.", p.Name)
	if s.formStayed(c, message) {
		return
	}
	s.redirectWithProjectFlash(c, p.Name, message, "")
}

func (s *Server) projectTagsData(c *gin.Context, p project.Project) render.ProjectTagsData {
	known := s.projects.KnownTags(s.projects.List())
	return render.ProjectTagsData{
		Page:       s.page(c, "Tags of "+p.Name, "projects"),
		Project:    p.Name,
		Tags:       p.Tags,
		InUse:      known,
		Colors:     project.TagColors,
		NextColors: project.NextTagColors(known),
		Return:     s.formReturn(c),
		Modal:      inFormModal(c),
	}
}

// refuseProjectTags answers a refused save: the dialog and a local caller
// hear why as JSON, the page shows the form again under the reason with what
// was sent, a form it could not read with the tags as they are. Like the
// tags settings it renders rather than redirects, so nothing a request sent
// rides a URL or the session.
func (s *Server) refuseProjectTags(c *gin.Context, p project.Project, sent url.Values, message string) {
	if inFormModal(c) || wantsJSON(c.Request) {
		c.JSON(http.StatusBadRequest, gin.H{"error": message})
		return
	}
	data := s.projectTagsData(c, p)
	if sent != nil {
		data.Tags = project.SentTags(sentPick(sent), data.InUse)
	}
	data.Page.Flash = render.Flash{Message: message, Level: "error"}
	c.HTML(http.StatusOK, "project_tags.gohtml", data)
}

// sentPick reads a project's tags form: a tag in use by its id (tag), a new
// one by its name (new) with its color in new-color:<name>, so a tag of any
// spelling names its own field.
func sentPick(form url.Values) project.TagPick {
	pick := project.TagPick{IDs: form["tag"], Names: form["new"], NewColors: map[string]string{}}
	for field, values := range form {
		if name, ok := strings.CutPrefix(field, "new-color:"); ok && len(values) > 0 {
			pick.NewColors[name] = values[0]
		}
	}
	return pick
}
