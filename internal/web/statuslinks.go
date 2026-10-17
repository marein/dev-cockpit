package web

import (
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// handleStatusLinks answers the status line's links chip as a fragment. The
// page's path comes along like the tab strip's, so the project the chip
// unfolds is the one the page is about, resolved the same way as at render.
// A guest's chip holds the projects of its link alone, its path stands below
// the link's address.
func (s *Server) handleStatusLinks(c *gin.Context) {
	path := c.Query("path")
	if g, ok := guestOf(c); ok {
		id, current, _, _ := quicknavContextFromPath(path)
		if id != "" {
			current = currentProject("", render.QuickNav{CurrentID: id, Active: s.guestTabs(g)}, "")
		}
		c.HTML(http.StatusOK, "status_links.gohtml", render.Page{Links: s.guestStatusLinks(g, current)})
		return
	}
	if path == "" {
		path = "/"
	}
	id, name, cleanPath, focus := quicknavContextFromPath(path)
	qn := s.buildQuickNav(id, name, cleanPath, focus)
	c.HTML(http.StatusOK, "status_links.gohtml", render.Page{
		Links: render.NewStatusLinks(qn.AllProjects, qn.CurrentProject),
	})
}

// guestStatusLinks is the links chip of a guest's status line: the addresses
// of the running stacks of the link's projects alone, current unfolded. The
// owner's reading of the projects stays out, no last use and no activity, and
// a worktree is one only beside its main, like in the guest's switcher.
func (s *Server) guestStatusLinks(g *guest, current string) render.StatusLinks {
	live := s.guestProjects(g)
	on := guestProjectNames(live)
	docker := s.dockerByProject(live)
	navs := make([]render.ProjectNav, 0, len(live))
	for _, p := range live {
		nav := render.ProjectNav{Name: p.Name, Docker: docker[p.Name]}
		if p.GitWorktree && slices.Contains(on, p.GitWorktreeOf) {
			nav.WorktreeOf = p.GitWorktreeOf
		}
		navs = append(navs, nav)
	}
	return render.NewStatusLinks(navs, current)
}
