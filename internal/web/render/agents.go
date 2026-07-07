package render

import "github.com/local/dev-cockpit/internal/coder"

// AgentsListData is the model for the agents list.
type AgentsListData struct {
	Page
	CoderNav CoderNav
	Base     string // canonical coder URL prefix, "/coders/<id>"
	Agents   []coder.Agent
}

// AgentsFormData is the model for create/edit forms.
type AgentsFormData struct {
	Page
	Base         string // canonical coder URL prefix, "/coders/<id>"
	IsEdit       bool
	OriginalID   string
	ID           string
	Description  string
	Instructions string
	FormAction   string
	SubmitLabel  string
	Heading      string
}
