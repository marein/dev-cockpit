package web

import (
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// guestQuickNav is the terminal list of a guest's pages: the coders and
// shells of the link's projects in strip order, also those started after the
// link was made and those of a project added to it later, each a tab of its
// own under the link's address. The owner's news and split views stay out,
// they are the owner's reading and the owner's layout.
func (s *Server) guestQuickNav(g *guest, currentID string) render.QuickNav {
	qn := render.QuickNav{CurrentID: currentID, CurrentProject: g.project.Name}
	on := g.share.Targets(share.KindProject)
	for _, t := range s.terminalTabs() {
		if !slices.Contains(on, t.Project) {
			continue
		}
		t.URL = g.base + t.URL
		t.HasNews = false
		t.Group, t.GroupPos, t.GroupName, t.GroupCol = "", 0, "", 0
		qn.Active = append(qn.Active, t)
	}
	qn.Strip = foldStripTabs(qn.Active)
	return qn
}

// guestSwitcher is the editor's project switcher of a guest: the link's
// projects that stand, each linking to its editor under the link's address.
// A row names nothing outside the link and nothing of the owner's reading,
// no tag and no last use, and a worktree is one only beside its main.
func (s *Server) guestSwitcher(g *guest, current string) []render.EditorProject {
	live := s.guestProjects(g)
	on := guestProjectNames(live)
	rows := make([]render.EditorProject, 0, len(live))
	for _, q := range live {
		row := render.EditorProject{Name: q.Name, URL: guestEditorURL(g, q.Name), Current: q.Name == current, Branch: q.GitBranch}
		switch {
		case q.GitWorktree && slices.Contains(on, q.GitWorktreeOf):
			row.Worktree, row.WorktreeOf, row.Repo = true, q.GitWorktreeOf, q.GitWorktreeOf
		case q.GitRepo || q.GitWorktree:
			row.Repo = q.Name
		}
		search := []string{q.Name}
		if row.Repo != q.Name && row.Repo != "" {
			search = append(search, row.Repo)
		}
		if q.GitBranch != "" {
			search = append(search, q.GitBranch)
		}
		row.Search = strings.Join(search, " ")
		rows = append(rows, row)
	}
	return rows
}

// handleShareEditorProjects re-renders a guest's switcher rows, which an open
// editor pulls when a project went on the link or off it.
func (s *Server) handleShareEditorProjects(c *gin.Context) {
	g, _ := guestOf(c)
	c.HTML(http.StatusOK, "editor_projects.gohtml", render.EditorProjectsData{
		Projects: s.guestSwitcher(g, g.project.Name),
	})
}

// guestPageQuickNav reads the terminal a guest's page shows from the path a
// fragment request names, the way the owner's fragments do.
func (s *Server) guestPageQuickNav(c *gin.Context, g *guest) render.QuickNav {
	id, _, _, _ := quicknavContextFromPath(strings.TrimPrefix(c.Query("path"), g.base))
	return s.guestQuickNav(g, id)
}

// handleShareTerminals answers the guest's Terminals entry: the first
// terminal of the link's projects in strip order, or the area empty.
func (s *Server) handleShareTerminals(c *gin.Context) {
	g, _ := guestOf(c)
	qn := s.guestQuickNav(g, "")
	if len(qn.Strip) > 0 {
		c.Redirect(http.StatusSeeOther, qn.Strip[0].URL)
		return
	}
	page := s.page(c, "Terminals", "terminals")
	page.HasTabStrip = true
	c.HTML(http.StatusOK, "terminals_empty.gohtml", page)
}

// handleShareTerminalTabs re-renders a guest's strip on a terminals event.
func (s *Server) handleShareTerminalTabs(c *gin.Context) {
	g, _ := guestOf(c)
	page := s.page(c, "", "terminals")
	page.QuickNav = s.guestPageQuickNav(c, g)
	c.HTML(http.StatusOK, "terminal_tabs.gohtml", page)
}

// handleShareCtx renders a guest's list column for the phone's sheet. The
// terminals are the one area a guest has a column of.
func (s *Server) handleShareCtx(c *gin.Context) {
	g, _ := guestOf(c)
	if c.Param("area") != "terminals" {
		s.handleNotFound(c)
		return
	}
	page := s.page(c, "", "terminals")
	page.QuickNav = s.guestPageQuickNav(c, g)
	c.HTML(http.StatusOK, "ctx_terminals.gohtml", page)
}
