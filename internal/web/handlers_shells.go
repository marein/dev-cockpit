package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/session"
	"github.com/local/dev-cockpit/internal/web/render"
)

func (s *Server) handleShellNew(c *gin.Context) {
	defaultPath := s.projects.DefaultPath()
	if name := strings.TrimSpace(c.Query("project")); name != "" {
		if p, err := s.projects.FindByName(name); err == nil {
			defaultPath = p.Path
		}
	}
	c.HTML(http.StatusOK, "shells_new.gohtml", render.ShellNewData{
		Page:        s.page(c, "New Shell", "projects"),
		Projects:    s.projects.SelectablePaths(),
		DefaultPath: defaultPath,
		Return:      s.formReturn(c),
	})
}

func (s *Server) handleShellCreate(c *gin.Context) {
	p, err := s.projects.Find(strings.TrimSpace(c.PostForm("project")))
	if err != nil {
		s.redirectWithFlash(c, "/shells/new", "", err.Error())
		return
	}
	id, err := s.shells.Start(p.Path, "shell")
	if err != nil {
		s.redirectWithFlash(c, "/shells/new", "", err.Error())
		return
	}
	c.Redirect(http.StatusSeeOther, "/shells/"+id)
}

func (s *Server) handleShellAttach(c *gin.Context) {
	id := c.Param("id")
	shell, err := s.shells.ResolveRunning(id)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	projectName := s.projects.ProjectNameFor(shell.CWD)
	c.HTML(http.StatusOK, "shell_attach.gohtml", render.ShellAttachData{
		Page:        s.page(c, shell.Name, "projects"),
		Shell:       shell,
		ProjectName: projectName,
		StreamURL:   "/shells/" + shell.Identifier + "/stream",
		ResizeURL:   "/shells/" + shell.Identifier + "/resize",
		InputURL:    "/shells/" + shell.Identifier + "/input",
		RenameURL:   "/shells/" + shell.Identifier + "/rename",
	})
}

func (s *Server) handleShellRename(c *gin.Context) {
	shell, err := s.shells.Rename(c.Param("id"), c.PostForm("name"))
	if err != nil {
		if errors.Is(err, session.ErrNoActiveSession) {
			c.String(http.StatusGone, err.Error())
			return
		}
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, map[string]string{"name": shell.Name})
}

func (s *Server) handleShellDelete(c *gin.Context) {
	id := c.Param("id")
	project := ""
	if sh, err := s.shells.ResolveRunning(id); err == nil {
		project = s.projects.ProjectNameFor(sh.CWD)
	}
	name, err := s.shells.Delete(id)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	s.redirectWithProjectFlash(c, project, "Shell \""+name+"\" deleted.", "")
}

func (s *Server) handleShellInput(c *gin.Context) {
	id := c.Param("id")
	var batch sessionInputBatch
	if err := c.ShouldBindJSON(&batch); err != nil {
		c.String(http.StatusBadRequest, "Invalid input.")
		return
	}
	if len(batch.Items) > maxSessionInputItems {
		c.String(http.StatusRequestEntityTooLarge, "Too many input items.")
		return
	}
	items := make([]session.Input, len(batch.Items))
	for i, item := range batch.Items {
		items[i] = session.Input(item)
	}
	if err := s.shells.Send(id, items); err != nil {
		if errors.Is(err, session.ErrNoActiveSession) {
			c.String(http.StatusGone, err.Error())
			return
		}
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	c.String(http.StatusOK, "OK")
}

func (s *Server) handleShellResize(c *gin.Context) {
	id := c.Param("id")
	var form sessionResizeForm
	if err := c.Bind(&form); err != nil {
		return
	}
	if err := s.shells.Resize(id, form.Cols, form.Rows); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": userFacingError(c, err)})
		return
	}
	c.Status(http.StatusNoContent)
}
