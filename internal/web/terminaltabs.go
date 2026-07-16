package web

import (
	"net/http"
	"sort"
	"strings"
	"time"

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
				Group:     r.TabGroup,
				GroupPos:  r.TabGroupPos,
				GroupName: r.TabGroupName,
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
			Group:     sh.TabGroup,
			GroupPos:  sh.TabGroupPos,
			GroupName: sh.TabGroupName,
		})
	}
	sort.SliceStable(tabs, func(i, j int) bool {
		return byTabOrder(tabs[i].TabPos, tabs[j].TabPos, tabs[i].StartedAt, tabs[j].StartedAt, tabs[i].ID, tabs[j].ID)
	})
	return tabs
}

// stripTabs folds the flat session list into the strip entries: sessions
// sharing a @dc_tab_group become one group tab at the position of their best
// placed member, everything else stays a single tab. A group with fewer than
// two live members renders as plain tabs, its stale options are ignored. The
// quick nav keeps using the flat list on purpose, groups are strip UI only.
func (s *Server) stripTabs() []render.StripTab {
	return foldStripTabs(s.terminalTabs())
}

func foldStripTabs(tabs []render.TerminalTab) []render.StripTab {
	memberCount := map[string]int{}
	for _, t := range tabs {
		if t.Group != "" {
			memberCount[t.Group]++
		}
	}
	var out []render.StripTab
	grouped := map[string]int{} // group id -> index in out
	for _, t := range tabs {
		if t.Group == "" || memberCount[t.Group] < 2 {
			out = append(out, render.StripTab{TerminalTab: t})
			continue
		}
		idx, ok := grouped[t.Group]
		if !ok {
			idx = len(out)
			grouped[t.Group] = idx
			out = append(out, render.StripTab{TerminalTab: render.TerminalTab{
				ID:   t.Group,
				URL:  "/splits/" + t.Group,
				Kind: "split",
			}})
		}
		entry := &out[idx]
		entry.Members = append(entry.Members, t)
		entry.HasNews = entry.HasNews || t.HasNews
	}
	for _, idx := range grouped {
		entry := &out[idx]
		sortGroupMembers(entry.Members)
		entry.Name = groupLabel(entry.Members)
		entry.Project = commonProject(entry.Members)
	}
	return out
}

// sortGroupMembers orders a group's members by their @dc_tab_gpos, falling
// back to the strip order for members without one.
func sortGroupMembers(members []render.TerminalTab) {
	sort.SliceStable(members, func(i, j int) bool {
		pi, pj := members[i].GroupPos, members[j].GroupPos
		if pi != pj {
			if pi == 0 {
				return false
			}
			if pj == 0 {
				return true
			}
			return pi < pj
		}
		return byTabOrder(members[i].TabPos, members[j].TabPos, members[i].StartedAt, members[j].StartedAt, members[i].ID, members[j].ID)
	})
}

// groupLabel is the display name of a group tab: the stored @dc_tab_gname when
// one is set (last write wins across members), else the joined member names.
func groupLabel(members []render.TerminalTab) string {
	for _, m := range members {
		if strings.TrimSpace(m.GroupName) != "" {
			return strings.TrimSpace(m.GroupName)
		}
	}
	names := make([]string, len(members))
	for i := range members {
		names[i] = members[i].Name
	}
	return strings.Join(names, " · ")
}

// commonProject is the members' shared project name, empty when they differ.
func commonProject(members []render.TerminalTab) string {
	project := ""
	for i, m := range members {
		if i == 0 {
			project = m.Project
			continue
		}
		if m.Project != project {
			return ""
		}
	}
	return project
}

// byTabOrder ranks live sessions the way the tab strip does, so the projects
// list and the quick nav agree with it: positioned sessions (@dc_tab_pos) first
// in ascending position, unpositioned ones after, oldest first, id as the final
// tiebreak.
func byTabOrder(posI, posJ int, atI, atJ time.Time, idI, idJ string) bool {
	if posI != posJ {
		if posI == 0 {
			return false
		}
		if posJ == 0 {
			return true
		}
		return posI < posJ
	}
	if !atI.Equal(atJ) {
		return atI.Before(atJ)
	}
	return idI < idJ
}

// handleTerminalTabsFragment re-renders the tab strip partial for the live
// refresh on a terminals event, mirroring the quick nav fragment endpoint:
// the page context is recovered from ?path.
func (s *Server) handleTerminalTabsFragment(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		path = "/"
	}
	id, name, cleanPath, focus := quicknavContextFromPath(path)
	c.HTML(http.StatusOK, "terminal_tabs.gohtml", render.TerminalTabsData{
		Page: render.Page{
			QuickNav:  s.buildQuickNav(id, name, cleanPath, focus),
			CSRFToken: s.csrfToken(c),
		},
		Tabs: s.stripTabs(),
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
	s.invalidateTerminals()
	s.publishTerminals("") // order changed everywhere, refresh all
	c.Status(http.StatusNoContent)
}
