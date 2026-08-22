package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/terminal"
	"github.com/marein/dev-cockpit/internal/web/render"
)

func (s *Server) handleShellNew(c *gin.Context) {
	defaultPath := s.projects.DefaultPath()
	if name := strings.TrimSpace(c.Query("project")); name != "" {
		if p, err := s.projects.FindByName(name); err == nil {
			defaultPath = p.Path
		}
	}
	// A split scoped create opens this form prefilled and carries the group
	// target through as hidden fields, like the coder create does.
	target := splitTargetFromRequest(c)
	c.HTML(http.StatusOK, "shells_new.gohtml", render.ShellNewData{
		Page:        s.page(c, "New Shell", "projects"),
		Projects:    s.projects.SelectablePaths(),
		DefaultPath: defaultPath,
		Return:      s.formReturn(c),
		SplitGroup:  target.Group,
		SplitColumn: target.Column,
	})
}

func (s *Server) handleShellCreate(c *gin.Context) {
	// A split scoped create carries its target through the form's hidden
	// fields, like the coder create does; the project is the form's own field
	// either way.
	target := splitTargetFromRequest(c)
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
	s.styleSessionPane(id)
	joinErr := s.joinSplit(id, target)
	s.invalidateTerminals()
	s.publishTerminals(s.projects.ProjectNameFor(p.Path))
	if joinErr != nil {
		// The shell runs either way; only the split view did not happen.
		s.redirectWithFlash(c, "/shells/"+id, "", "The shell was started but could not join the split view.")
		return
	}
	c.Redirect(http.StatusSeeOther, "/shells/"+id)
}

func (s *Server) handleShellAttach(c *gin.Context) {
	id := c.Param("id")
	sh, err := s.shells.ResolveRunning(id)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	// A grouped shell lives on the split page; every link to its own page
	// lands there with its pane focused.
	if url, ok := s.splitPageURL(sh.TabGroup, sh.Identifier); ok {
		c.Redirect(http.StatusSeeOther, url)
		return
	}
	projectName := s.projects.ProjectNameFor(sh.CWD)
	s.projects.Touch(projectName)
	s.notifier.MarkTargetRead(sh.Identifier)
	page := s.page(c, pageTitle(sh.Name, projectName), "projects")
	page.HasTabStrip = true
	c.HTML(http.StatusOK, "shell_attach.gohtml", render.ShellAttachData{
		Page:        page,
		Shell:       sh,
		ProjectName: projectName,
		StreamURL:   "/shells/" + sh.Identifier + "/stream",
		ResizeURL:   "/shells/" + sh.Identifier + "/resize",
		InputURL:    "/shells/" + sh.Identifier + "/input",
		RenameURL:   "/shells/" + sh.Identifier + "/rename",
	})
}

func (s *Server) handleShellRename(c *gin.Context) {
	sh, err := s.shells.Rename(c.Param("id"), c.PostForm("name"))
	if err != nil {
		if errors.Is(err, shell.ErrNotRunning) {
			c.String(http.StatusGone, err.Error())
			return
		}
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	s.publishTerminals(s.projects.ProjectNameFor(sh.CWD))
	c.JSON(http.StatusOK, map[string]string{"name": sh.Name})
}

// handleShellName returns just the current shell name as plain text. The attach
// header pulls it on a terminals event to track a rename made elsewhere, instead of
// digging its name out of the whole tab strip fragment.
func (s *Server) handleShellName(c *gin.Context) {
	sh, err := s.shells.ResolveRunning(c.Param("id"))
	if err != nil {
		c.String(http.StatusGone, err.Error())
		return
	}
	c.String(http.StatusOK, sh.Name)
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
	s.notifier.MarkTargetRead(id)
	s.publishTerminals(project)
	s.redirectWithProjectFlash(c, project, "Shell \""+name+"\" deleted.", "")
}

func (s *Server) handleShellInput(c *gin.Context) {
	id := c.Param("id")
	var batch terminalInputBatch
	if err := c.ShouldBindJSON(&batch); err != nil {
		c.String(http.StatusBadRequest, "Invalid input.")
		return
	}
	if len(batch.Items) > maxTerminalInputItems {
		c.String(http.StatusRequestEntityTooLarge, "Too many input items.")
		return
	}
	items := make([]terminal.Input, len(batch.Items))
	for i, item := range batch.Items {
		items[i] = terminal.Input(item)
	}
	if err := s.shells.Send(id, items); err != nil {
		if errors.Is(err, shell.ErrNotRunning) {
			s.shellNotRunning(c, id)
			return
		}
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	c.String(http.StatusOK, "OK")
}

func (s *Server) handleShellResize(c *gin.Context) {
	id := c.Param("id")
	var form terminalResizeForm
	if err := c.Bind(&form); err != nil {
		return
	}
	s.updateTerminalTheme(form.Background, form.Foreground)
	if err := s.shells.Resize(id, form.Cols, form.Rows); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": userFacingError(c, err)})
		return
	}
	c.Status(http.StatusNoContent)
}
