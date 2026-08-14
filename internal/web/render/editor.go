package render

import "github.com/local/dev-cockpit/internal/project"

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
}

// EditorProjectsData feeds the switcher fragment, the very rows the editor
// page renders inside its dropdown. The client pulls it when the project set
// changes instead of building the markup a second time in the browser.
type EditorProjectsData struct {
	Projects []EditorProject
}

// EditorProject is one project switcher entry in the editor's tree header. The
// entries render with the data-project-* attributes @dc/project-sort reads, so
// the client orders the menu like every other project listing.
type EditorProject struct {
	Name         string
	URL          string
	Current      bool
	Active       bool
	LastUsedUnix int64
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
	Steered       bool
	SteerPrefill  string
	// FilesData feeds a coder's files modal, the same one the attach pages
	// carry; nil for shells and for coders whose files cannot be listed.
	FilesData *CoderFilesData
}
