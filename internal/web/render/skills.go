package render

import "github.com/local/dev-cockpit/internal/coder"

// SkillsListData is the model for the skills list.
type SkillsListData struct {
	Page
	CoderTabs  CoderTabs
	CoderQuery string // "?coder=x" suffix for links when several coders run
	Skills     []coder.Skill
}

// SkillsFormData is the model for create/edit skill forms.
type SkillsFormData struct {
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
