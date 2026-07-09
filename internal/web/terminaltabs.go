package web

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/tmux"
	"github.com/local/dev-cockpit/internal/web/render"
)

// terminalTabs collects every live coder and shell for the attach page tab
// strip. The order is cross-device state held in tmux itself: sessions carry a
// @dc_tab_pos user option written on reorder, so the order lives and dies with
// the sessions and needs no state file. Positioned sessions come first,
// everything without a position (freshly started, resumed) joins on the right,
// oldest first.
func (s *Server) terminalTabs() []render.TerminalTab {
	news := s.notifier.UnreadTargets()
	var tabs []render.TerminalTab
	for i := range s.coders {
		coderID := s.coders[i].ID()
		for _, r := range s.coders[i].Snapshot().Running {
			tabs = append(tabs, render.TerminalTab{
				ID:        r.Identifier,
				Name:      r.Name,
				URL:       "/coders/" + r.Identifier,
				Project:   s.projects.ProjectNameFor(r.CWD),
				Coder:     coderID,
				Kind:      "coder",
				HasNews:   news[r.Identifier],
				StartedAt: r.StartedAt,
				TabPos:    r.TabPos,
			})
		}
	}
	for _, sh := range s.shells.List() {
		tabs = append(tabs, render.TerminalTab{
			ID:        sh.Identifier,
			Name:      sh.Name,
			URL:       "/shells/" + sh.Identifier,
			Project:   s.projects.ProjectNameFor(sh.CWD),
			Kind:      "shell",
			HasNews:   news[sh.Identifier],
			StartedAt: sh.StartedAt,
			TabPos:    sh.TabPos,
		})
	}
	sort.SliceStable(tabs, func(i, j int) bool {
		pi, pj := tabs[i].TabPos, tabs[j].TabPos
		if pi != pj {
			if pi == 0 {
				return false
			}
			if pj == 0 {
				return true
			}
			return pi < pj
		}
		if !tabs[i].StartedAt.Equal(tabs[j].StartedAt) {
			return tabs[i].StartedAt.Before(tabs[j].StartedAt)
		}
		return tabs[i].ID < tabs[j].ID
	})
	return tabs
}

// handleTerminalTabsFragment re-renders the tab strip partial for the
// background refresh when the + menu or the switcher opens, mirroring the
// quick nav fragment endpoint: the page context is recovered from ?path.
func (s *Server) handleTerminalTabsFragment(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		path = "/"
	}
	id, name := quicknavContextFromPath(path)
	c.HTML(http.StatusOK, "terminal_tabs.gohtml", render.TerminalTabsData{
		Page: render.Page{
			QuickNav:  s.buildQuickNav(id, name, path),
			CSRFToken: s.csrfToken(c),
		},
		Tabs: s.terminalTabs(),
	})
}

type tabOrderRequest struct {
	IDs []string `json:"ids"`
}

// maxTabOrderIDs bounds one reorder write so a rogue request cannot build an
// arbitrarily long tmux command line.
const maxTabOrderIDs = 512

// handleTerminalTabsOrder persists the strip order as @dc_tab_pos options on
// the live tmux sessions, first id is the leftmost tab. Ids that do not
// resolve to a live coder or shell are dropped, so a client posting a strip
// that contains a session which ended in the meantime still succeeds.
func (s *Server) handleTerminalTabsOrder(c *gin.Context) {
	var req tabOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "Invalid order.")
		return
	}
	if len(req.IDs) > maxTabOrderIDs {
		c.String(http.StatusRequestEntityTooLarge, "Too many entries.")
		return
	}
	sessions := map[string]string{}
	for i := range s.coders {
		for _, r := range s.coders[i].Snapshot().Running {
			sessions[r.Identifier] = r.TmuxSession
		}
	}
	for _, sh := range s.shells.List() {
		sessions[sh.Identifier] = sh.TmuxSession
	}
	names := make([]string, 0, len(req.IDs))
	for _, id := range req.IDs {
		if name, ok := sessions[id]; ok {
			names = append(names, name)
			delete(sessions, id)
		}
	}
	if err := tmux.New().SetTabPositions(names); err != nil {
		c.String(http.StatusInternalServerError, "Could not save the tab order.")
		return
	}
	for i := range s.coders {
		s.coders[i].Invalidate()
	}
	s.shells.Invalidate()
	c.Status(http.StatusNoContent)
}
