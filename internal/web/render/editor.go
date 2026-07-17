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
	// Intel carries the editor intelligence client configuration, rendered
	// as a JSON data attribute on the dc-editor element.
	Intel string
}

// EditorIntelConfig is the client side intelligence configuration. It is
// marshalled into EditorData.Intel. Exts lists the file extensions with a
// language server profile, so the client never mirrors the registry.
type EditorIntelConfig struct {
	Mode       string   `json:"mode"`
	AutoAI     bool     `json:"autoAi"`
	DebounceMs int      `json:"debounceMs"`
	Exts       []string `json:"exts"`
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
