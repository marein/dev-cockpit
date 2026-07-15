package render

import "github.com/local/dev-cockpit/internal/project"

// EditorData is the model for the per-project code editor page.
type EditorData struct {
	Page
	Project    project.Project
	MaxEditKiB int64
	// Return is the safe in-app URL the header back button leads to, passed by
	// the linking page as ?return like the create forms' Cancel.
	Return string
	// Projects feeds the project switcher in the file tree header, one entry
	// per selectable project linking to its editor page.
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
