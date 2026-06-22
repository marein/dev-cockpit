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
					At:   active.StartedAt,
				})
			}
		}
		for _, inactive := range snap.Inactive {
			if filesystem.IsUnder(inactive.CWD, projects[i].Path) {
				projects[i].InactiveSessions++
				projects[i].InactiveSessionRefs = append(projects[i].InactiveSessionRefs, project.SessionRef{
					ID:   inactive.SessionID,
					Name: inactive.Name,
					At:   inactive.UpdatedAt,
				})
			}
		}
		// Sessions sorted by date, most recent first (like the old session list).
		sort.Slice(projects[i].ActiveSessionRefs, func(a, b int) bool {
			return projects[i].ActiveSessionRefs[a].At.After(projects[i].ActiveSessionRefs[b].At)
		})
		sort.Slice(projects[i].InactiveSessionRefs, func(a, b int) bool {
			return projects[i].InactiveSessionRefs[a].At.After(projects[i].InactiveSessionRefs[b].At)
		})
	}

	shells := s.shells.List()
	for i := range projects {
		for j := range shells {
			if filesystem.IsUnder(shells[j].CWD, projects[i].Path) {
				projects[i].ShellRefs = append(projects[i].ShellRefs, project.ShellRef{
					ID:   shells[j].Identifier,
					Name: shells[j].Name,
				})
			}
		}
		sort.Slice(projects[i].ShellRefs, func(a, b int) bool {
			return strings.ToLower(projects[i].ShellRefs[a].Name) < strings.ToLower(projects[i].ShellRefs[b].Name)
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
	name := filepath.Base(path)
	s.redirectWithFlash(c, "/projects#project-"+name, "Project \""+name+"\" created.", "")
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
	s.purgeProjectRunners(p.Path)
	if err := s.projects.Remove(p); err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	s.redirectWithFlash(c, "/projects", "Project \""+p.Name+"\" deleted.", "")
}

// purgeProjectRunners tears down everything a project has running before the
// project directory is removed: live sessions are stopped, every stored
// (resumable) session under the project is deleted, and live shells are killed.
// Best-effort — individual failures don't block project removal.
func (s *Server) purgeProjectRunners(path string) {
	snap := s.sessions.Snapshot()
	for _, r := range snap.Running {
		if filesystem.IsUnder(r.CWD, path) {
			_, _ = s.sessions.Stop(r.Identifier)
		}
	}
	for _, r := range snap.Resumable {
		if filesystem.IsUnder(r.CWD, path) {
			_, _ = s.sessions.DeleteResumable(r.SessionID)
		}
	}
	for _, sh := range s.shells.List() {
		if filesystem.IsUnder(sh.CWD, path) {
			_, _ = s.shells.Delete(sh.Identifier)
		}
	}
}
