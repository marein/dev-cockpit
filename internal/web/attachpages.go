package web

import (
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// The attach pages of a coder and of a shell, the owner's and a guest's of a
// shared project alike. A guest's page is the same page under the link's
// address: it never moves what the owner looked at last, never reads the
// owner's news and never sizes the pane, so it carries no resize address, and
// it types only where the link grants it.

func (s *Server) handleCoderAttach(c *gin.Context) {
	acc := accessOf(c)
	id := c.Param("id")
	co, running, err := s.resolveRunning(id)
	if err != nil {
		s.attachGone(c, err)
		return
	}
	// A grouped session lives on the split page; every link to its own page
	// lands there with its pane focused. A guest sees no split pages.
	if url, ok := s.splitPageURL(running.TabGroup, running.Identifier); ok && !acc.Guest {
		c.Redirect(http.StatusSeeOther, url)
		return
	}
	var files []filesystem.File
	if acc.Can(share.TerminalInput) {
		files, err = co.Coder().SessionRepository().ListFiles(running.Identifier)
		if err != nil {
			s.attachGone(c, err)
			return
		}
	}
	projectName := s.projects.ProjectNameFor(running.CWD)
	if !acc.Guest {
		s.terminalFocused(running.Identifier, projectName)
	}
	page := s.page(c, pageTitle(running.Name, projectName), "terminals")
	page.HasTabStrip = true
	base := acc.Base + "/coders/" + running.Identifier
	data := render.CoderAttachData{
		Page:            page,
		Running:         running,
		Identifier:      running.Identifier,
		Coder:           co.ID(),
		Launcher:        coder.LauncherForSession(co.Coder(), running.Identifier),
		ProjectName:     projectName,
		ProjectURL:      s.attachProjectURL(acc, projectName),
		Files:           files,
		MaxUploadSizeMB: maxRequestBodyMegabytes(s.cfg.MaxRequestBodySize),
		StreamURL:       base + "/stream",
		TextURL:         base + "/copy",
		ConversationURL: base + "/conversation",
	}
	if !acc.Guest {
		data.ResizeURL = base + "/resize"
	}
	if acc.Can(share.TerminalInput) {
		data.InputURL = base + "/input"
	}
	c.HTML(http.StatusOK, "coder_attach.gohtml", data)
}

func (s *Server) handleShellAttach(c *gin.Context) {
	acc := accessOf(c)
	id := c.Param("id")
	sh, err := s.shells.ResolveRunning(id)
	if err != nil {
		s.attachGone(c, err)
		return
	}
	// A grouped shell lives on the split page; every link to its own page
	// lands there with its pane focused. A guest sees no split pages.
	if url, ok := s.splitPageURL(sh.TabGroup, sh.Identifier); ok && !acc.Guest {
		c.Redirect(http.StatusSeeOther, url)
		return
	}
	projectName := s.projects.ProjectNameFor(sh.CWD)
	if !acc.Guest {
		s.terminalFocused(sh.Identifier, projectName)
	}
	page := s.page(c, pageTitle(sh.Name, projectName), "terminals")
	page.HasTabStrip = true
	base := acc.Base + "/shells/" + sh.Identifier
	data := render.ShellAttachData{
		Page:        page,
		Shell:       sh,
		ProjectName: projectName,
		ProjectURL:  s.attachProjectURL(acc, projectName),
		StreamURL:   base + "/stream",
		TextURL:     base + "/copy",
	}
	if !acc.Guest {
		data.ResizeURL = base + "/resize"
	}
	if acc.Can(share.TerminalInput) {
		data.InputURL = base + "/input"
	}
	if acc.Can(share.Terminals) {
		data.RenameURL = base + "/rename"
	}
	c.HTML(http.StatusOK, "shell_attach.gohtml", data)
}

// attachProjectURL is where the project name in an attach page's head leads:
// the owner's row on the projects page, a guest's editor of the project, and
// nowhere for a guest whose link does not open the project, the coder is an
// assistant's of the link.
func (s *Server) attachProjectURL(acc render.Access, project string) string {
	if project == "" || (acc.Guest && acc.Project != project) {
		return ""
	}
	if acc.Guest {
		return acc.Base + "/projects/" + url.PathEscape(project) + "/editor"
	}
	return "/projects#project-" + project
}

// attachGone answers an attach page whose terminal is not there: the owner
// lands on the projects page with the reason, a guest, who has no session to
// carry a flash, reads the page that does not exist.
func (s *Server) attachGone(c *gin.Context, err error) {
	if accessOf(c).Guest {
		s.handleNotFound(c)
		return
	}
	s.redirectWithFlash(c, "/projects", "", err.Error())
}
