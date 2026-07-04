package render

// ProjectNav is the per-project quick-access model shared by the subpages
// (editor, session, shell). It lists the project's editor plus its live
// sessions, inactive sessions and shells, so a subpage can offer one-click
// navigation to every sibling resource of the same project.
type ProjectNav struct {
	Name           string
	Path           string
	EditorURL      string
	NewCoderURL    string
	NewShellURL    string
	ActiveCoders   []ProjectNavItem
	InactiveCoders []ProjectNavItem
	Shells         []ProjectNavItem
	// Active mirrors the projects page: a project counts as active when it has a
	// running session or shell. LastUsedUnix is its last-opened timestamp. Both
	// feed the project browser's client-side sort (same modes as the list page).
	Active       bool
	LastUsedUnix int64
	HasNews      bool
}

// ProjectNavItem is one navigable resource. URL points at the attach page for
// active sessions and shells, and at the resume action for inactive sessions.
type ProjectNavItem struct {
	ID      string
	Name    string
	URL     string
	Coder   string // owning coder id, shown when several coders run
	HasNews bool
}
