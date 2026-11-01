package web

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// A project's forge shows where the project is bound and binds a main
// project by its origin, by hand where the origin cannot tell, or not at all.
// Like a project's tags it is a dialog over the page it was opened on and a
// page of its own.

// projectForgeLocalRefusal turns a change of a binding over the local socket
// away: the binding decides which repository a host's token acts on, which no
// assistant decides on its own.
const projectForgeLocalRefusal = "A project's forge is set on its page, not from a command."

// forgeRefused answers why p's binding cannot be set, "" when it can.
func forgeRefused(p project.Project) string {
	switch {
	case !p.GitRepo:
		return fmt.Sprintf("%q is no git repository, it binds to no forge.", p.Name)
	case p.GitWorktree && p.GitWorktreeOf != "":
		return fmt.Sprintf("A worktree binds like its main project %q.", p.GitWorktreeOf)
	case p.GitWorktree:
		return "A worktree binds like its main repository, by its origin."
	}
	return ""
}

// handleProjectForge renders a project's forge, as a page and alone for the
// dialog. A worktree gets its main project's binding without the form.
func (s *Server) handleProjectForge(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		s.renderError(c, http.StatusNotFound, "Project not found", "There is no such project.")
		return
	}
	if !p.GitRepo {
		s.renderError(c, http.StatusConflict, "No git repository", forgeRefused(p))
		return
	}
	data := s.projectForgeData(c, p, nil)
	if data.Modal {
		c.HTML(http.StatusOK, "project_forge_form.gohtml", data)
		return
	}
	c.HTML(http.StatusOK, "project_forge.gohtml", data)
}

func (s *Server) handleProjectForgeSave(c *gin.Context) {
	if s.forgeRefuseLocal(c, projectForgeLocalRefusal) {
		return
	}
	p, err := s.projects.FindByName(c.Param("name"))
	switch {
	case err != nil && (inFormModal(c) || wantsJSON(c.Request)):
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown project."})
		return
	case err != nil:
		s.renderError(c, http.StatusNotFound, "Project not found", "There is no such project.")
		return
	case !p.GitRepo && (inFormModal(c) || wantsJSON(c.Request)):
		c.JSON(http.StatusConflict, gin.H{"error": forgeRefused(p)})
		return
	case !p.GitRepo:
		s.renderError(c, http.StatusConflict, "No git repository", forgeRefused(p))
		return
	}
	if refusal := forgeRefused(p); refusal != "" {
		s.refuseProjectForge(c, p, nil, refusal)
		return
	}
	if err := readForm(c); err != nil {
		s.refuseProjectForge(c, p, nil, err.Error())
		return
	}
	sent := forge.Override{How: forge.How(c.PostForm("how")), Host: c.PostForm("host"), Path: c.PostForm("path")}
	if err := s.forge.SetOverride(p.Name, sent); err != nil {
		s.refuseProjectForge(c, p, &sent, err.Error())
		return
	}
	s.publishProjects()
	message := "Forge saved."
	if s.formStayed(c, message) {
		return
	}
	s.redirectWithProjectFlash(c, p.Name, message, "")
}

// projectForgeData reads where p is bound and fills the binding by hand with
// what was sent, else the one set, else the one its origin gives.
func (s *Server) projectForgeData(c *gin.Context, p project.Project, sent *forge.Override) render.ProjectForgeData {
	page := s.page(c, "Forge of "+p.Name, "projects")
	bindings, err := s.forge.Bindings()
	if err != nil {
		page.Flash = render.Flash{Message: err.Error(), Level: "error"}
	}
	owner := forgeOwner(p)
	set := bindings.Override(owner)
	data := render.ProjectForgeData{
		Page:     page,
		Modal:    inFormModal(c),
		Project:  p.Name,
		Return:   s.formReturn(c),
		Worktree: p.GitWorktree,
		Main:     p.GitWorktreeOf,
		How:      set.How,
		Origin:   p.GitOrigin,
		PathMax:  forge.MaxPathLength,
	}
	if b, ok := bindings.Bind(owner, p.GitOriginRaw); ok {
		data.Bound = &b
	}
	if b, ok := bindings.Hosts.Resolve(p.GitOriginRaw); ok {
		data.OriginBound = &b
	}
	host, path := set.Host, set.Path
	if set.How != forge.ByHand && data.OriginBound != nil {
		host, path = data.OriginBound.Host.ID, data.OriginBound.Path
	}
	if sent != nil {
		data.How, host, path = sent.How, sent.Host, sent.Path
	}
	data.Path = path
	for _, h := range bindings.Hosts {
		data.Hosts = append(data.Hosts, render.ForgeChoice{ID: h.ID, Label: h.Kind.Label + " at " + h.WebURL, Picked: h.ID == host})
	}
	return data
}

// refuseProjectForge answers a refused save: the dialog and a local caller
// hear why as JSON, the page shows the form again under the reason with what
// was sent, so nothing a request sent rides a URL or the session.
func (s *Server) refuseProjectForge(c *gin.Context, p project.Project, sent *forge.Override, message string) {
	if inFormModal(c) || wantsJSON(c.Request) {
		c.JSON(http.StatusBadRequest, gin.H{"error": message})
		return
	}
	data := s.projectForgeData(c, p, sent)
	data.Page.Flash = render.Flash{Message: message, Level: "error"}
	c.HTML(http.StatusBadRequest, "project_forge.gohtml", data)
}
