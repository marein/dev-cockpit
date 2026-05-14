package render

import "github.com/local/dev-cockpit/internal/provider"

// AgentsListData is the model for the agents list.
type AgentsListData struct {
	Page
	Agents []provider.Agent
}

// AgentsFormData is the model for create/edit forms.
type AgentsFormData struct {
	Page
	IsEdit       bool
	OriginalID   string
	ID           string
	Description  string
	Instructions string
	FormAction   string
	SubmitLabel  string
	Heading      string
}
