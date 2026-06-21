package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/session"
	"github.com/local/dev-cockpit/internal/web/render"
)

func (s *Server) handleShellsList(c *gin.Context) {
	c.HTML(http.StatusOK, "shells_list.gohtml", render.ShellsData{
		Page:   s.page(c, "Shells", "shells"),
		Shells: s.shells.List(),
	})
}

func (s *Server) handleShellCreate(c *gin.Context) {
	workdir, name, err := s.shellTarget(c.PostForm("project"))
	if err != nil {
		s.redirectWithFlash(c, "/shells", "", err.Error())
		return
	}
	id, err := s.shells.Start(workdir, name)
	if err != nil {
		s.redirectWithFlash(c, "/shells", "", err.Error())
		return
	}
	c.Redirect(http.StatusSeeOther, "/shells/"+id)
}

// shellTarget resolves the working directory and label for a new shell. An
// empty project means a home-directory shell.
func (s *Server) shellTarget(rawProject string) (workdir, name string, err error) {
	if project := strings.TrimSpace(rawProject); project != "" {
		p, err := s.projects.FindByName(project)
		if err != nil {
			return "", "", err
		}
		return p.Path, p.Name, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", "", errors.New("Home directory is not available.")
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		resolved = home
	}
	return resolved, "home", nil
}

func (s *Server) handleShellAttach(c *gin.Context) {
	id := c.Param("id")
	shell, err := s.shells.ResolveRunning(id)
	if err != nil {
		s.redirectWithFlash(c, "/shells", "", err.Error())
		return
	}
	c.HTML(http.StatusOK, "shell_attach.gohtml", render.ShellAttachData{
		Page:      s.page(c, shell.Name, "shells"),
		Shell:     shell,
		StreamURL: "/shells/" + shell.Identifier + "/stream",
		ResizeURL: "/shells/" + shell.Identifier + "/resize",
		InputURL:  "/shells/" + shell.Identifier + "/input",
		RenameURL: "/shells/" + shell.Identifier + "/rename",
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
	name, err := s.shells.Delete(c.Param("id"))
	if err != nil {
		s.redirectWithFlash(c, "/shells", "", err.Error())
		return
	}
	s.redirectWithFlash(c, "/shells", "Shell \""+name+"\" deleted.", "")
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
