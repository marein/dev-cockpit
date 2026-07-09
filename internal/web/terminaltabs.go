package web

import (
	"sort"

	"github.com/local/dev-cockpit/internal/web/render"
)

// terminalTabs collects every live coder and shell for the attach page tab
// strip. Entries are ordered by start time, oldest first, so a freshly started
// coder or shell always joins the strip on the right. The client may reorder
// the strip per device afterwards; ids it has never seen keep this order.
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
		})
	}
	sort.SliceStable(tabs, func(i, j int) bool {
		if !tabs[i].StartedAt.Equal(tabs[j].StartedAt) {
			return tabs[i].StartedAt.Before(tabs[j].StartedAt)
		}
		return tabs[i].ID < tabs[j].ID
	})
	return tabs
}
