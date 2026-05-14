package web

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/web/render"
)

type projectCreateForm struct {
	Name AlphaNumDashString `form:"project_name" binding:"required"`
}

type projectDeleteForm struct {
	Project string `form:"project" binding:"required"`
}

func (s *Server) handleProjectsList(c *gin.Context) {
	projects := s.projects.List()
	s.sessions.Invalidate()
	snap := s.sessions.Snapshot()
	for i := range projects {
		for _, active := range snap.Running {
			if filesystem.IsUnder(active.CWD, projects[i].Path) {
				projects[i].ActiveSessions++
				projects[i].ActiveSessionRefs = append(projects[i].ActiveSessionRefs, project.SessionRef{
					ID:   active.Identifier,
					Name: active.Name,
				})
			}
		}
		for _, inactive := range snap.Inactive {
			if filesystem.IsUnder(inactive.CWD, projects[i].Path) {
				projects[i].InactiveSessions++
				projects[i].InactiveSessionRefs = append(projects[i].InactiveSessionRefs, project.SessionRef{
					ID:   inactive.SessionID,
					Name: inactive.Name,
				})
			}
		}
		sort.Slice(projects[i].ActiveSessionRefs, func(a, b int) bool {
			return strings.ToLower(projects[i].ActiveSessionRefs[a].Name) < strings.ToLower(projects[i].ActiveSessionRefs[b].Name)
		})
		sort.Slice(projects[i].InactiveSessionRefs, func(a, b int) bool {
			return strings.ToLower(projects[i].InactiveSessionRefs[a].Name) < strings.ToLower(projects[i].InactiveSessionRefs[b].Name)
		})
	}
	c.HTML(http.StatusOK, "projects_list.gohtml", render.ProjectsListData{
		Page:     s.page(c, "Projects", "projects"),
		Projects: projects,
	})
}

func (s *Server) handleProjectNew(c *gin.Context) {
	c.HTML(http.StatusOK, "projects_new.gohtml", s.page(c, "New Project", "projects"))
}

func (s *Server) handleProjectCreate(c *gin.Context) {
	var form projectCreateForm
	if !s.decodeForm(c, &form, "/projects/new") {
		return
	}
	path, err := s.projects.Create(form.Name.String())
	if err != nil {
		s.redirectWithFlash(c, "/projects/new", "", err.Error())
		return
	}
	s.redirectWithFlash(c, "/projects", "Project \""+filepath.Base(path)+"\" created.", "")
}

func (s *Server) handleProjectDelete(c *gin.Context) {
	var form projectDeleteForm
	if !s.decodeForm(c, &form, "/projects") {
		return
	}
	p, err := s.projects.FindByName(form.Project)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	if used := s.projectInUse(p.Path); used != "" {
		s.redirectWithFlash(c, "/projects", "", "Cannot delete project: in use by session \""+used+"\".")
		return
	}
	if err := s.projects.Remove(p); err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	s.redirectWithFlash(c, "/projects", "Project \""+p.Name+"\" deleted.", "")
}

// projectInUse returns the name of an active/inactive session whose CWD lives
// under path, or "" if none.
func (s *Server) projectInUse(path string) string {
	snap := s.sessions.Snapshot()
	for _, r := range snap.Running {
		if filesystem.IsUnder(r.CWD, path) {
			return r.Name
		}
	}
	for _, r := range snap.Resumable {
		if filesystem.IsUnder(r.CWD, path) {
			return r.Name
		}
	}
	return ""
}
