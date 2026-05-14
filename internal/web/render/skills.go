package render

import "github.com/local/dev-cockpit/internal/provider"

// SkillsListData is the model for the skills list.
type SkillsListData struct {
	Page
	Skills []provider.Skill
}

// SkillsFormData is the model for create/edit skill forms.
type SkillsFormData struct {
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
