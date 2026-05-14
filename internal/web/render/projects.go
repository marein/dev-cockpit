package render

import "github.com/local/dev-cockpit/internal/project"

// ProjectsListData is the model for the projects list page.
type ProjectsListData struct {
	Page
	Projects []project.Project
}
