package web

import (
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/web/render"
)

type projectCreateForm struct {
	Name AlphaNumDashString `form:"project_name" binding:"required"`
}

type projectDeleteForm struct {
	Project string `form:"project" binding:"required"`
}

func (s *Server) handleProjectsList(c *gin.Context) {
	for i := range s.coders {
		s.coders[i].Invalidate()
	}
	s.shells.Invalidate()
	c.HTML(http.StatusOK, "projects_list.gohtml", render.ProjectsListData{
		Page:     s.page(c, "Projects", "projects"),
		Projects: s.projectsWithRunners(),
	})
}

// projectsWithRunners returns every project enriched with the running and
// inactive sessions and the shells living under it. It is the single source
// behind both the projects list page and the quick nav project browser. Sessions
// are ordered most-recent first, shells by name. The snapshot is read as-is
// (cached); callers that need a fresh one (the list page) Invalidate beforehand.
func (s *Server) projectsWithRunners() []project.Project {
	projects := s.projects.List()
	news := s.notifier.UnreadTargets()
	for i := range projects {
		for j := range s.coders {
			coderID := s.coders[j].ID()
			snap := s.coders[j].Snapshot()
			for _, active := range snap.Running {
				if filesystem.IsUnder(active.CWD, projects[i].Path) {
					projects[i].ActiveCoders++
					projects[i].ActiveCoderRefs = append(projects[i].ActiveCoderRefs, project.CoderRef{
						ID:       active.Identifier,
						Name:     active.Name,
						Coder:    coderID,
						At:       active.StartedAt,
						TabPos:   active.TabPos,
						Group:    active.TabGroup,
						GroupPos: active.TabGroupPos,
						HasNews:  news[active.Identifier],
					})
					projects[i].HasNews = projects[i].HasNews || news[active.Identifier]
				}
			}
			for _, inactive := range snap.Inactive {
				if filesystem.IsUnder(inactive.CWD, projects[i].Path) {
					projects[i].InactiveCoders++
					projects[i].InactiveCoderRefs = append(projects[i].InactiveCoderRefs, project.CoderRef{
						ID:      inactive.SessionID,
						Name:    inactive.Name,
						Coder:   coderID,
						At:      inactive.UpdatedAt,
						HasNews: news[inactive.SessionID],
					})
					projects[i].HasNews = projects[i].HasNews || news[inactive.SessionID]
				}
			}
		}
		// Active coders follow the tab strip order (@dc_tab_pos), so the start
		// page agrees with the strip and the quick nav. Inactive coders have no
		// tab, they stay most recent first.
		refs := projects[i].ActiveCoderRefs
		sort.Slice(refs, func(a, b int) bool {
			return byTabOrder(refs[a].TabPos, refs[b].TabPos, refs[a].At, refs[b].At, refs[a].ID, refs[b].ID)
		})
		sort.Slice(projects[i].InactiveCoderRefs, func(a, b int) bool {
			return projects[i].InactiveCoderRefs[a].At.After(projects[i].InactiveCoderRefs[b].At)
		})
	}

	shells := s.shells.List()
	for i := range projects {
		for j := range shells {
			if filesystem.IsUnder(shells[j].CWD, projects[i].Path) {
				projects[i].ShellRefs = append(projects[i].ShellRefs, project.ShellRef{
					ID:       shells[j].Identifier,
					Name:     shells[j].Name,
					At:       shells[j].StartedAt,
					TabPos:   shells[j].TabPos,
					Group:    shells[j].TabGroup,
					GroupPos: shells[j].TabGroupPos,
					HasNews:  news[shells[j].Identifier],
				})
				projects[i].HasNews = projects[i].HasNews || news[shells[j].Identifier]
			}
		}
		// Shells follow the same tab strip order as the coders above.
		refs := projects[i].ShellRefs
		sort.Slice(refs, func(a, b int) bool {
			return byTabOrder(refs[a].TabPos, refs[b].TabPos, refs[a].At, refs[b].At, refs[a].ID, refs[b].ID)
		})
		projects[i].ActiveRefs = mergedActiveRefs(&projects[i])
	}
	return projects
}

// mergedActiveRefs interleaves a project's live coders and shells into the tab
// strip order, so the chip row on the projects page reads like the strip. Split
// view members cluster at their group's strip position (the best placed member)
// in @dc_tab_gpos order, mirroring foldStripTabs without folding them into one
// entry. Group scope is this project's list; a group spanning projects clusters
// per row.
func mergedActiveRefs(p *project.Project) []project.TerminalRef {
	type mref struct {
		ref      project.TerminalRef
		at       time.Time
		tabPos   int
		group    string
		groupPos int
	}
	var all []mref
	for _, r := range p.ActiveCoderRefs {
		all = append(all, mref{
			ref:      project.TerminalRef{ID: r.ID, Name: r.Name, Kind: "coder", Coder: r.Coder, HasNews: r.HasNews},
			at:       r.At,
			tabPos:   r.TabPos,
			group:    r.Group,
			groupPos: r.GroupPos,
		})
	}
	for _, r := range p.ShellRefs {
		all = append(all, mref{
			ref:      project.TerminalRef{ID: r.ID, Name: r.Name, Kind: "shell", HasNews: r.HasNews},
			at:       r.At,
			tabPos:   r.TabPos,
			group:    r.Group,
			groupPos: r.GroupPos,
		})
	}
	sort.SliceStable(all, func(a, b int) bool {
		return byTabOrder(all[a].tabPos, all[b].tabPos, all[a].at, all[b].at, all[a].ref.ID, all[b].ref.ID)
	})
	groupCount := map[string]int{}
	for _, r := range all {
		if r.group != "" {
			groupCount[r.group]++
		}
	}
	out := make([]project.TerminalRef, 0, len(all))
	done := map[string]bool{}
	for _, r := range all {
		if r.group == "" || groupCount[r.group] < 2 {
			out = append(out, r.ref)
			continue
		}
		if done[r.group] {
			continue
		}
		done[r.group] = true
		var members []mref
		for _, m := range all {
			if m.group == r.group {
				members = append(members, m)
			}
		}
		sort.SliceStable(members, func(a, b int) bool {
			pa, pb := members[a].groupPos, members[b].groupPos
			if pa != pb {
				if pa == 0 {
					return false
				}
				if pb == 0 {
					return true
				}
				return pa < pb
			}
			return byTabOrder(members[a].tabPos, members[b].tabPos, members[a].at, members[b].at, members[a].ref.ID, members[b].ref.ID)
		})
		for _, m := range members {
			out = append(out, m.ref)
		}
	}
	return out
}

func (s *Server) handleProjectNew(c *gin.Context) {
	c.HTML(http.StatusOK, "projects_new.gohtml", s.page(c, "New Project", "projects"))
}

func (s *Server) handleProjectCreate(c *gin.Context) {
	var form projectCreateForm
	if !s.decodeForm(c, &form, "/projects/new") {
		return
	}
	path, err := s.projects.Create(form.Name.String())
	if err != nil {
		s.redirectWithFlash(c, "/projects/new", "", err.Error())
		return
	}
	name := filepath.Base(path)
	s.redirectWithProjectFlash(c, name, "Project \""+name+"\" created.", "")
}

func (s *Server) handleProjectDelete(c *gin.Context) {
	var form projectDeleteForm
	if !s.decodeForm(c, &form, "/projects") {
		return
	}
	p, err := s.projects.FindByName(form.Project)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	s.purgeProjectRunners(p.Path)
	s.publishTerminals("") // the purge stopped this project's coders/shells, drop them everywhere
	if err := s.projects.Remove(p); err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	s.redirectWithFlash(c, "/projects", "Project \""+p.Name+"\" deleted.", "")
}

// purgeProjectRunners tears down everything a project has running before the
// project directory is removed: live sessions are stopped, every stored
// (resumable) session under the project is deleted, and live shells are killed.
// Best-effort — individual failures don't block project removal.
func (s *Server) purgeProjectRunners(path string) {
	for i := range s.coders {
		sessions := s.coders[i]
		snap := sessions.Snapshot()
		for _, r := range snap.Running {
			if filesystem.IsUnder(r.CWD, path) {
				_, _ = sessions.Stop(r.Identifier)
			}
		}
		for _, r := range snap.Resumable {
			if filesystem.IsUnder(r.CWD, path) {
				_, _ = sessions.DeleteResumable(r.SessionID)
			}
		}
	}
	for _, sh := range s.shells.List() {
		if filesystem.IsUnder(sh.CWD, path) {
			_, _ = s.shells.Delete(sh.Identifier)
		}
	}
}
