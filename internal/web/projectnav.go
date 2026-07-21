package web

import (
	"net/url"

	"github.com/local/dev-cockpit/internal/web/render"
)

// projectBrowser maps the shared projectsWithRunners model into the quick nav's
// two-level browser model: each project plus its editor, sessions and shells with
// the URLs to reach them. It adds nothing the projects list doesn't already
// compute, it just reshapes a subset and derives the links. currentPath is the
// page the quick nav was opened from, so the create links return there on Cancel,
// matching the Active tab.
func (s *Server) projectBrowser(currentPath string) []render.ProjectNav {
	projects := s.projectsWithRunners()
	out := make([]render.ProjectNav, 0, len(projects))
	ret := url.QueryEscape(currentPath)
	for _, p := range projects {
		nav := render.ProjectNav{
			Name:         p.Name,
			Path:         p.Path,
			EditorURL:    "/projects/" + url.PathEscape(p.Name) + "/editor?return=" + ret,
			NewCoderURL:  "/coders/new?project=" + url.QueryEscape(p.Name) + "&return=" + ret,
			NewShellURL:  "/shells/new?project=" + url.QueryEscape(p.Name) + "&return=" + ret,
			LastUsedUnix: p.LastUsedUnix,
			Active:       len(p.ActiveRefs) > 0,
			HasNews:      p.HasNews,
		}
		for _, r := range p.ActiveRefs {
			url := "/shells/" + r.ID
			if r.Kind == "coder" {
				url = "/coders/" + r.ID
			}
			nav.Terminals = append(nav.Terminals, render.ProjectNavItem{ID: r.ID, Name: r.Name, URL: url, Kind: r.Kind, Coder: r.Coder, HasNews: r.HasNews})
		}
		for _, r := range p.InactiveCoderRefs {
			nav.InactiveCoders = append(nav.InactiveCoders, render.ProjectNavItem{ID: r.ID, Name: r.Name, URL: "/coders/" + r.ID + "/resume", Coder: r.Coder, HasNews: r.HasNews})
		}
		out = append(out, nav)
	}
	return out
}
