package render

import "github.com/marein/dev-cockpit/internal/project"

// EditorData is the model for the per-project code editor page.
type EditorData struct {
	Page
	Project    project.Project
	MaxEditKiB int64
	// MaxEditSize is the same limit as the file tree writes its sizes, so the
	// sentence in the empty editor reads like the rows next to it rather than
	// as a five digit KiB number.
	MaxEditSize string
	// Return is the safe in-app URL the header back button leads to, passed by
	// the linking page as ?return like the create forms' Cancel.
	Return string
	// Projects feeds the project switcher in the file tree header, one entry
	// per selectable project linking to its editor page.
	Projects []EditorProject
	// The diff's limits ride along as page data: the diff itself is computed in
	// the browser, so this is where the values have to be. How it looks, the
	// view and the folding, is not here, that is per device in the editor's own
	// settings.
	DiffMaxLines int
	DiffMaxKiB   int
	// LSPExts is the code navigation surface as comma joined `ext:Label`
	// pairs of the enabled language server profiles, so the client never
	// mirrors the registry and a disabled profile leaves no surface.
	LSPExts string
	// Terminal is the session id a panel-marked coder create handed back via
	// ?terminal=, rendered into the page because the client must not read it
	// from the URL: a boosted navigation swaps the body before it pushes the
	// URL, so the editor's init still sees the previous address. The terminal
	// panel activates that session's tab.
	Terminal string
	// View is the panel the page opens on, "commit" or "compare", rendered
	// for the same reason the terminal id is: the projects page's git menu
	// links into a view, and the client reads it off the page, never the URL.
	// Empty is the plain editor.
	View string
}

// EditorProjectsData feeds the switcher fragment, the very rows the editor
// page renders for its project palette. The client pulls it when the project
// set changes instead of building the markup a second time in the browser.
type EditorProjectsData struct {
	Projects []EditorProject
}

// EditorProject is one row of the editor's project palette. The rows render
// with the data-project-* attributes @dc/project-sort reads, so the client
// orders them like every other project listing, and they carry the git facts a
// worktree is told apart by. Every fact here comes from the file reads the
// project list already did (internal/gitfacts), no git process runs for the
// palette.
type EditorProject struct {
	Name         string
	URL          string
	Current      bool
	Active       bool
	LastUsedUnix int64
	// Repo is the repository the row belongs to as a reader names it: the main
	// project's name for a worktree that has one in the cockpit, the last path
	// element of the main repository otherwise, the project's own name for a
	// repository, empty for a directory that is no repository at all.
	Repo string
	// Branch is the checked out branch, or a short hash on a detached HEAD.
	Branch string
	// Worktree marks a linked worktree, WorktreeOf names its main project when
	// that project lies in the cockpit, and WorktreeMain is the main
	// repository's path on disk for the worktree whose main does not.
	Worktree     bool
	WorktreeOf   string
	WorktreeMain string
	// Search is what the palette's query is matched against: the project's
	// name, its repository and its branch in one line, so "dev htmx" lands on
	// the one worktree of dev-cockpit that stands on the htmx branch.
	Search string
}

// EditorTerminalsData feeds the editor's terminal panel fragment: the live
// coders and shells of one project in tab strip order, plus the project's
// inactive coders for the panel's + menu.
type EditorTerminalsData struct {
	Sessions  []EditorTerminal
	Inactive  []EditorInactiveCoder
	CSRFToken string
}

// EditorInactiveCoder is one resumable coder in the panel's + menu.
type EditorInactiveCoder struct {
	ID      string
	Name    string
	Coder   string
	URL     string
	HasNews bool
}

// EditorTerminal is one session in the editor's terminal panel: the tab entry
// and the pane the client mounts a terminal island into.
type EditorTerminal struct {
	ID            string
	Name          string
	Kind          string // "coder" or "shell"
	Coder         string
	URL           string
	StreamURL     string
	ResizeURL     string
	InputURL      string
	ScrollHistory bool
	HasNews       bool
	Working       bool
	Steered       bool
	SteerPrefill  string
	// FilesData feeds a coder's files modal, the same one the attach pages
	// carry; nil for shells and for coders whose files cannot be listed.
	FilesData *CoderFilesData
}
