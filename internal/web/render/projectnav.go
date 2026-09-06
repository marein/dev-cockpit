package render

// ProjectNav is the per-project quick-access model shared by the subpages
// (editor, session, shell). It lists the project's editor plus its live
// terminals and inactive sessions, so a subpage can offer one-click
// navigation to every sibling resource of the same project. Terminals holds
// the running coders and shells merged in tab strip order, the same order the
// projects page shows its chips.
type ProjectNav struct {
	Name           string
	Path           string
	EditorURL      string
	NewCoderURL    string
	NewShellURL    string
	Terminals      []ProjectNavItem
	InactiveCoders []ProjectNavItem
	// Active mirrors the projects page: a project counts as active when it has a
	// running session, shell or container. LastUsedUnix is its last-opened
	// timestamp. Both feed the project browser's client-side sort (same modes
	// as the list page).
	Active       bool
	LastUsedUnix int64
	HasNews      bool
	// The git facts the search line and the worktree marks are drawn from,
	// the same set the editor's project palette carries and built from the
	// same file reads (internal/gitfacts), no git process runs for them.
	// Repo names the repository a row belongs to, Branch the checked out
	// branch, Worktree marks a linked worktree, WorktreeOf names its main
	// project when that project lies in the cockpit and WorktreeMain the main
	// repository's path on disk when it does not.
	Repo         string
	Branch       string
	Worktree     bool
	WorktreeOf   string
	WorktreeMain string
	// Search is the one line the quick nav's query is matched against: name,
	// repository and branch together, so "dev htmx" lands on the one worktree
	// of dev-cockpit that stands on the htmx branch.
	Search string
	// GitRepo says whether the detail offers the git menu, and Docker carries
	// the compose stacks and the containers it lists below its terminals, both
	// the shapes the projects page already renders from. Docker is empty
	// whenever no daemon answers, and the detail then shows nothing of it.
	GitRepo bool
	Docker  ProjectDocker
}

// ProjectNavItem is one navigable resource. URL points at the attach page for
// active sessions and shells, and at the resume action for inactive sessions.
type ProjectNavItem struct {
	ID      string
	Name    string
	URL     string
	Kind    string // "coder" or "shell", empty on inactive entries
	Coder   string // owning coder id, shown when several coders run
	HasNews bool
}

// ProjectOption is one project in the project select of a create form. The
// select is server rendered alphabetically and put into the user's order in the
// browser, so an option carries everything @dc/project-sort compares: the name,
// whether the project runs something, and when it was last opened.
type ProjectOption struct {
	Name         string
	Path         string
	Active       bool
	LastUsedUnix int64
}

// ProjectOptions turns the quick nav's project browser into that select. Both
// list every project with the same marks, and the quick nav is built for every
// page render anyway, so a create form takes its projects from there instead of
// scanning the coders and shells a second time.
func ProjectOptions(nav []ProjectNav) []ProjectOption {
	out := make([]ProjectOption, 0, len(nav))
	for _, p := range nav {
		out = append(out, ProjectOption{
			Name:         p.Name,
			Path:         p.Path,
			Active:       p.Active,
			LastUsedUnix: p.LastUsedUnix,
		})
	}
	return out
}
