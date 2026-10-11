package web

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// projectBrowser maps the shared projectsWithRunners model into the quick nav's
// two-level browser model: each project plus its editor, sessions and shells with
// the URLs to reach them. It adds nothing the projects list doesn't already
// compute, it just reshapes a subset and derives the links. currentPath is the
// page the quick nav was opened from, so the create links return there on Cancel,
// matching the Active tab. The row's repository name and its search line come
// from switcherRepo and switcherSearch, the very functions the editor's project
// palette uses, so both surfaces name a repository the same and answer the same
// query.
func (s *Server) projectBrowser(currentPath string) []render.ProjectNav {
	projects := s.projectsWithRunners()
	out := make([]render.ProjectNav, 0, len(projects))
	ret := url.QueryEscape(currentPath)
	// The same map the projects page builds, off the same cached daemon
	// snapshot: nothing calls docker here. A host without a reachable daemon
	// gets a nil map, and every project's Docker stays empty.
	containers := s.dockerByProject(projects)
	for _, p := range projects {
		nav := render.ProjectNav{
			Name:         p.Name,
			Path:         p.Path,
			EditorURL:    "/projects/" + url.PathEscape(p.Name) + "/editor",
			NewCoderURL:  "/coders/new?project=" + url.QueryEscape(p.Name) + "&return=" + ret,
			NewShellURL:  "/shells/new?project=" + url.QueryEscape(p.Name) + "&return=" + ret,
			LastUsedUnix: p.LastUsedUnix,
			Active:       p.Active(),
			HasNews:      p.HasNews,
			Repo:         switcherRepo(p),
			Branch:       p.GitBranch,
			Worktree:     p.GitWorktree,
			WorktreeOf:   p.GitWorktreeOf,
			WorktreeMain: p.GitWorktreeMain,
			Search:       switcherSearch(p),
			Tags:         p.Tags,
			SearchTags:   p.SearchTags,
			GitRepo:      p.GitRepo,
			Docker:       containers[p.Name],
		}
		for _, r := range p.ActiveRefs {
			url := "/shells/" + r.ID
			if r.Kind == "coder" {
				url = "/coders/" + r.ID
			}
			nav.Terminals = append(nav.Terminals, render.ProjectNavItem{ID: r.ID, Name: r.Name, URL: url, Kind: r.Kind, Coder: r.Coder, Launcher: r.Launcher, HasNews: r.HasNews})
		}
		for _, r := range p.InactiveCoderRefs {
			nav.InactiveCoders = append(nav.InactiveCoders, render.ProjectNavItem{ID: r.ID, Name: r.Name, URL: "/coders/" + r.ID + "/resume", Coder: r.Coder, Launcher: r.Launcher, HasNews: r.HasNews})
		}
		out = append(out, nav)
	}
	return out
}

// switcherRepo names the repository a palette row belongs to. A worktree is
// named after its main project, or after the main repository's directory when
// that repository is no project of the cockpit; a repository is named after
// itself; a plain directory has no repository to be named after.
func switcherRepo(q project.Project) string {
	switch {
	case q.GitWorktree && q.GitWorktreeOf != "":
		return q.GitWorktreeOf
	case q.GitWorktree && q.GitWorktreeMain != "":
		return filepath.Base(q.GitWorktreeMain)
	case q.GitRepo:
		return q.Name
	}
	return ""
}

// switcherSearch is the one line the palette's token search runs over for a
// row: the project's name, the repository it belongs to and its branch, for a
// worktree without a main project in the cockpit the main repository's path,
// so that path is findable by the pieces somebody remembers of it, and the
// names of the tags that find it, a worktree's main project's.
func switcherSearch(q project.Project) string {
	parts := []string{q.Name}
	if repo := switcherRepo(q); repo != "" && repo != q.Name {
		parts = append(parts, repo)
	}
	if q.GitBranch != "" {
		parts = append(parts, q.GitBranch)
	}
	if q.GitWorktree && q.GitWorktreeOf == "" && q.GitWorktreeMain != "" {
		parts = append(parts, q.GitWorktreeMain)
	}
	return strings.Join(append(parts, q.SearchTags...), " ")
}
