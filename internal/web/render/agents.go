package render

import "github.com/local/dev-cockpit/internal/coder"

// AgentsListData is the model for the agents list.
type AgentsListData struct {
	Page
	CoderTabs  CoderTabs
	CoderQuery string // "?coder=x" suffix for links when several coders run
	Agents     []coder.Agent
}

// AgentsFormData is the model for create/edit forms.
type AgentsFormData struct {
	Page
	SelectedCoder string
	CoderQuery    string
	IsEdit        bool
	OriginalID    string
	ID            string
	Description   string
	Instructions  string
	FormAction    string
	SubmitLabel   string
	Heading       string
}
