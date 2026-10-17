package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// handleEditorEntry answers the shell's Editor entry, the one address the rail
// and the tab bar link. Which project it opens is decided here and never in a
// rendered link: the project this editor was last opened on, else the one
// before that, else the project used last anywhere in the cockpit, else the
// first one, and with no project at all the projects page says one has to be
// created, through the same flash every other handler sends it. The memory is
// server state (`editorRecent`), so the phone opens what the desktop was on.
// Every step checks the project still exists, a deleted one falls through to
// the next. The answer must not be cached or the browser would keep reopening
// the project of the first click, so it is a See Other with no-store, never a
// permanent redirect.
func (s *Server) handleEditorEntry(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	list := s.projects.List()
	exists := func(name string) bool {
		for i := range list {
			if list[i].Name == name {
				return true
			}
		}
		return false
	}
	for _, name := range s.editorRecent.Names() {
		if exists(name) {
			c.Redirect(http.StatusSeeOther, "/projects/"+url.PathEscape(name)+"/editor")
			return
		}
	}
	pick := -1
	for i := range list {
		if pick < 0 || list[i].LastUsedUnix > list[pick].LastUsedUnix {
			pick = i
		}
	}
	if pick < 0 {
		s.redirectWithInfo(c, "/projects", "No project yet. Create one, then it opens in the editor.")
		return
	}
	c.Redirect(http.StatusSeeOther, "/projects/"+url.PathEscape(list[pick].Name)+"/editor")
}

// handleProjectEditor renders the editor page for one project.
func (s *Server) handleProjectEditor(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", "Project not found.")
		return
	}
	s.projects.Touch(p.Name)
	s.notifier.MarkTargetRead(notify.WorktreeTarget(p.Name))
	// Exactly the project name, nothing of what stands open in it: the entry
	// has to answer "where was I", and a file or a scroll position is the
	// page's own state, not the area's.
	s.editorRecent.Touch(p.Name)
	s.renderProjectEditor(c, p)
}

// handleShareEditor renders the same page for a guest of a project of a
// link, without moving what the owner opened last or read.
func (s *Server) handleShareEditor(c *gin.Context) {
	g, _ := guestOf(c)
	s.renderProjectEditor(c, g.project)
}

func (s *Server) renderProjectEditor(c *gin.Context, p project.Project) {
	// The language servers of the project's languages start with the page,
	// so their indexing runs while the reader still orients, not under the
	// first lookup; the statusbar indicator shows it meanwhile. The editor
	// page itself is editor action.
	s.intel.Touch(p.Name)
	go s.warmLSPServers(p)
	set := s.editorSettings()
	lspExts, lspNames := s.lspSpecs()
	data := render.EditorData{
		Page:         s.page(c, "Editor - "+p.Name, "editor"),
		Project:      p,
		MaxEditKiB:   filesystem.MaxEditableBytes / 1024,
		MaxEditSize:  filesystem.HumanSize(filesystem.MaxEditableBytes),
		DiffMaxLines: set.DiffMaxLines,
		DiffMaxKiB:   set.DiffMaxKiB,
		Autosave:     set.Autosave,
		LSPExts:      lspExts,
		LSPNames:     lspNames,
		Terminal:     strings.TrimSpace(c.Query("terminal")),
		View:         editorView(c.Query("view")),
		File:         strings.TrimSpace(c.Query("file")),
		Line:         positiveQuery(c, "line"),
		Col:          positiveQuery(c, "col"),
	}
	if g, ok := guestOf(c); ok {
		data.Projects = s.guestSwitcher(g, p.Name)
	} else {
		data.Projects = s.editorSwitcher(p.Name)
	}
	c.HTML(http.StatusOK, "project_editor.gohtml", data)
}

// editorView reads the panel a deep link asks the editor to open on, the
// commit view or the revision compare, which is where the projects page's git
// menu leads. Anything else is nothing, so a stale link opens the plain
// editor; the value rides into the page like the terminal id and is never read
// out of the URL there.
func editorView(raw string) string {
	switch view := strings.TrimSpace(raw); view {
	case "commit", "compare":
		return view
	}
	return ""
}

// positiveQuery reads a line or column of a deep link, where anything that is
// not a number above zero means none.
func positiveQuery(c *gin.Context, key string) int {
	n, err := strconv.Atoi(strings.TrimSpace(c.Query(key)))
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// editorSwitcher is the project list behind the tree header's switcher, one
// entry per project linking to its editor. current marks the project the page
// belongs to.
func (s *Server) editorSwitcher(current string) []render.EditorProject {
	list := s.projectsWithRunners()
	entries := make([]render.EditorProject, 0, len(list))
	for _, q := range list {
		entries = append(entries, render.EditorProject{
			Name:         q.Name,
			URL:          "/projects/" + url.PathEscape(q.Name) + "/editor",
			Current:      q.Name == current,
			Active:       q.Active(),
			LastUsedUnix: q.LastUsedUnix,
			Repo:         switcherRepo(q),
			Branch:       q.GitBranch,
			Worktree:     q.GitWorktree,
			WorktreeOf:   q.GitWorktreeOf,
			WorktreeMain: q.GitWorktreeMain,
			Search:       switcherSearch(q),
			Tags:         q.Tags,
		})
	}
	return entries
}

// handleEditorProjects re-renders the switcher's rows alone, which is what an
// open editor pulls when a project is created, renamed or deleted somewhere
// else. It answers the same markup the page carries, so the browser never
// holds a second copy of those rows; the project this editor belongs to comes
// from the route, exactly as the page built them. A project that has just been
// deleted under this page is no error here: the switcher simply loses its row,
// and where that leaves the editor is the page's own business.
func (s *Server) handleEditorProjects(c *gin.Context) {
	c.HTML(http.StatusOK, "editor_projects.gohtml", render.EditorProjectsData{
		Projects: s.editorSwitcher(c.Param("name")),
	})
}

// handleEditorTerminals renders the editor's terminal panel fragment: the
// project's live coders and shells in tab strip order, flat like the quick nav
// (groups are strip UI). The stream, resize and input routes are the same ones
// the attach pages use, so the panel is one more client of the same session.
// A guest's panel carries the link's addresses, no resize, and the input,
// the files and the inactive coders only where the link grants them.
func (s *Server) handleEditorTerminals(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	acc := accessOf(c)
	steered, prefill := s.watcher.Marks()
	page := s.page(c, "", "projects")
	sessions := []render.EditorTerminal{}
	for _, t := range s.terminalTabs() {
		if t.Project != p.Name {
			continue
		}
		base := acc.Base + t.URL
		et := render.EditorTerminal{
			ID:            t.ID,
			Name:          t.Name,
			Kind:          t.Kind,
			Coder:         t.Coder,
			Launcher:      t.Launcher,
			URL:           base,
			StreamURL:     base + "/stream",
			TextURL:       base + "/copy",
			ScrollHistory: t.Kind == "shell",
			Working:       t.Working,
		}
		if !acc.Guest {
			et.ResizeURL = base + "/resize"
			et.HasNews = t.HasNews
			et.Steered = steered[t.ID]
			et.SteerPrefill = prefill[t.ID]
		}
		if acc.Can(share.TerminalInput) {
			et.InputURL = base + "/input"
		}
		if t.Kind == "coder" {
			et.ConversationURL = base + "/conversation"
			if co, running, err := s.resolveRunning(t.ID); err == nil && acc.Can(share.TerminalInput) {
				if files, err := co.Coder().SessionRepository().ListFiles(running.Identifier); err == nil {
					et.FilesData = &render.CoderFilesData{
						Page:            page,
						Identifier:      t.ID,
						Files:           files,
						MaxUploadSizeMB: maxRequestBodyMegabytes(s.cfg.MaxRequestBodySize),
					}
				}
			}
		}
		sessions = append(sessions, et)
	}
	inactive := []render.EditorInactiveCoder{}
	for _, q := range s.projectsWithRunners() {
		if q.Name != p.Name || !acc.Can(share.Terminals) {
			continue
		}
		for _, r := range q.InactiveCoderRefs {
			inactive = append(inactive, render.EditorInactiveCoder{
				ID:       r.ID,
				Name:     r.Name,
				Coder:    r.Coder,
				Launcher: r.Launcher,
				URL:      acc.Base + "/coders/" + r.ID + "/resume",
				HasNews:  r.HasNews && !acc.Guest,
			})
		}
		break
	}
	c.HTML(http.StatusOK, "editor_terminals.gohtml", render.EditorTerminalsData{
		Sessions:  sessions,
		Inactive:  inactive,
		CSRFToken: page.CSRFToken,
		Access:    acc,
	})
}
