package render

import "github.com/local/dev-cockpit/internal/coder"

// SkillsListData is the model for the skills list.
type SkillsListData struct {
	Page
	CoderNav CoderNav
	Base     string // canonical coder URL prefix, "/coders/<id>"
	Skills   []coder.Skill
}

// SkillsFormData is the model for create/edit skill forms.
type SkillsFormData struct {
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
