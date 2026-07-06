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
			EditorURL:    "/projects/" + url.PathEscape(p.Name) + "/editor",
			NewCoderURL:  "/coders/new?project=" + url.QueryEscape(p.Name) + "&return=" + ret,
			NewShellURL:  "/shells/new?project=" + url.QueryEscape(p.Name) + "&return=" + ret,
			LastUsedUnix: p.LastUsedUnix,
			Active:       len(p.ActiveCoderRefs) > 0 || len(p.ShellRefs) > 0,
		}
		for _, r := range p.ActiveCoderRefs {
			nav.ActiveCoders = append(nav.ActiveCoders, render.ProjectNavItem{ID: r.ID, Name: r.Name, URL: "/coders/" + r.ID, Coder: r.Coder})
		}
		for _, r := range p.InactiveCoderRefs {
			nav.InactiveCoders = append(nav.InactiveCoders, render.ProjectNavItem{ID: r.ID, Name: r.Name, URL: "/coders/" + r.ID + "/resume", Coder: r.Coder})
		}
		for _, sh := range p.ShellRefs {
			nav.Shells = append(nav.Shells, render.ProjectNavItem{ID: sh.ID, Name: sh.Name, URL: "/shells/" + sh.ID})
		}
		out = append(out, nav)
	}
	return out
}
