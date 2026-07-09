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
}
