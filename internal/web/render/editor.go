package render

import "github.com/local/dev-cockpit/internal/project"

// EditorData is the model for the per-project code editor page.
type EditorData struct {
	Page
	Project    project.Project
	MaxEditKiB int64
}
