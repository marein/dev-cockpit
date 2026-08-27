package web

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
	"github.com/marein/dev-cockpit/plugin"
)

// NewProjectCreator is the one project creation path of the cockpit: the
// repository makes the directory (or adopts an existing empty one, see
// project.Repository.Create), the projects event tells every open page. The
// projects page handler runs it, and so does the plugin package's Projects
// facade, which is what makes a project created by a plugin exactly a
// project created on the page. A name whose directory holds content answers
// plugin.ErrProjectExists, the one sentinel both surfaces word their refusal
// from. It stands apart from the Server because the plugins configure before
// the server is built, on the same bus the server is then given.
func NewProjectCreator(projects *project.Repository, bus *eventbus.Bus) func(ctx context.Context, name string) (string, error) {
	return func(ctx context.Context, name string) (string, error) {
		path, err := projects.Create(name)
		if err != nil {
			if errors.Is(err, project.ErrExists) {
				return "", plugin.ErrProjectExists
			}
			return "", err
		}
		bus.Publish(eventbus.Event{Type: "projects"})
		return path, nil
	}
}

// NewProjectChanged is what a plugin's Project.Changed runs: the same
// projects event NewProjectCreator publishes, so every open list pulls the
// project's fresh state. It cannot fail today, the error is the facade's
// room to grow.
func NewProjectChanged(bus *eventbus.Bus) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		bus.Publish(eventbus.Event{Type: "projects"})
		return nil
	}
}

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
	projects := s.projectsWithRunners()
	c.HTML(http.StatusOK, "projects_list.gohtml", render.ProjectsListData{
		Page:          s.page(c, "Projects", "projects"),
		Projects:      projects,
		Docker:        s.dockerByProject(projects),
		DockerActions: render.DockerButtons(s.composeActions()),
		Deleting:      s.deletes.view(projects),
	})
}

// dockerByProject joins the cached container list and the compose stacks
// onto the projects through the compose working directory. One cache read
// for the whole page, nothing asks the daemon here; only the stack lookup
// stats the project roots for a compose file.
func (s *Server) dockerByProject(projects []project.Project) map[string]render.ProjectDocker {
	state := s.docker.State()
	if !state.Available {
		return nil
	}
	// One matcher for the whole page: the link rules are the install's, not a
	// project's, and compiling them per container would compile them per chip.
	matcher := s.linkMatcher()
	out := map[string]render.ProjectDocker{}
	for i := range projects {
		entry := render.ProjectDocker{}
		for _, container := range state.ForDir(projects[i].Path) {
			entry.Containers = append(entry.Containers, render.DockerContainer{
				Container: container,
				Links:     matcher.Links(container),
			})
		}
		for _, stack := range state.StacksForDir(projects[i].Path) {
			row := render.DockerStack{
				Stack: stack,
				Busy:  s.docker.ComposeBusy(stack.Dir),
			}
			// The newest run of the stack, going or over: its output is what
			// the menu leads to, which is the only way back to what a command
			// wrote once the toast is gone.
			if runs := s.docker.ComposeRunsForDir(stack.Dir); len(runs) > 0 {
				row.RunID = runs[0].ID
				row.RunAction = runs[0].Action
				row.RunGoing = runs[0].Running
			}
			entry.Stacks = append(entry.Stacks, row)
		}
		if len(entry.Containers) > 0 || len(entry.Stacks) > 0 {
			out[projects[i].Name] = entry
		}
	}
	return out
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
	path, err := s.createProject(c.Request.Context(), form.Name.String())
	if err != nil {
		if wantsJSON(c.Request) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		s.redirectWithFlash(c, "/projects/new", "", err.Error())
		return
	}
	name := filepath.Base(path)
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"name": name, "path": path})
		return
	}
	s.redirectWithProjectFlash(c, name, "Project \""+name+"\" created.", "")
}

func (s *Server) handleProjectDelete(c *gin.Context) {
	var form projectDeleteForm
	if !s.decodeForm(c, &form, "/projects") {
		return
	}
	p, err := s.projects.FindByName(form.Project)
	if err != nil {
		if wantsJSON(c.Request) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	// A project that runs containers is deleted off the request: compose down
	// takes as long as it takes and no page may wait on it. A project that runs
	// no containers but has a compose run under way goes the same road, because
	// that run has to be waited out before the directory can go, and waiting is
	// exactly what a request must not do.
	if len(s.composeStacksToStop(p.Path)) > 0 || s.docker.ComposeBusyUnder(p.Path) {
		s.startProjectDelete(c, p)
		return
	}
	s.purgeProjectRunners(p.Path)
	s.closeProjectLSP(p.Name)
	if err := s.projects.Remove(p); err != nil {
		if wantsJSON(c.Request) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	s.commitDrafts.Delete(p.Name)
	s.lineComments.Clear(p.Name)
	s.quickOpen.Forget(p.Path)
	s.publishTerminals("") // the purge removed this project's coders and shells everywhere
	s.publishProjects()
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"name": p.Name, "path": p.Path})
		return
	}
	c.Redirect(http.StatusSeeOther, "/projects")
}

// startProjectDelete answers the request at once and leaves the work to the
// goroutine behind it. The projects event is what puts the working row on every
// open list, this client's own included.
func (s *Server) startProjectDelete(c *gin.Context, p project.Project) {
	if s.deletes.start(p.Name, p.Path) {
		go s.deleteProjectWithCompose(p)
		s.publishProjects()
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusAccepted, gin.H{"name": p.Name, "path": p.Path, "deleting": true})
		return
	}
	c.Redirect(http.StatusSeeOther, "/projects")
}

// purgeProjectRunners tears down everything a project has running before the
// project directory is removed: live sessions are stopped, every stored
// (resumable) session under the project is deleted, and live shells are killed.
// Best-effort — individual failures don't block project removal. Every
// terminal it takes marks its notifications read on the spot, the same call
// the single stop and delete handlers make: their pages are gone with the
// project, so their news has nobody left to ring for.
func (s *Server) purgeProjectRunners(path string) {
	for i := range s.coders {
		sessions := s.coders[i]
		snap := sessions.Snapshot()
		for _, r := range snap.Running {
			if filesystem.IsUnder(r.CWD, path) {
				_, _ = sessions.Stop(r.Identifier)
				s.notifier.MarkTargetRead(r.Identifier)
				// The terminal is gone with the project, so its job is too:
				// nothing will ever report on it again.
				s.jobCalledOff(r.Identifier)
			}
		}
		for _, r := range snap.Resumable {
			if filesystem.IsUnder(r.CWD, path) {
				_, _ = sessions.DeleteResumable(r.SessionID)
				s.notifier.MarkTargetRead(r.SessionID)
				s.jobCalledOff(r.SessionID)
			}
		}
	}
	for _, sh := range s.shells.List() {
		if filesystem.IsUnder(sh.CWD, path) {
			_, _ = s.shells.Delete(sh.Identifier)
			s.notifier.MarkTargetRead(sh.Identifier)
		}
	}
}
