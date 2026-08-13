package render

import "github.com/local/dev-cockpit/internal/coder"

// SkillRow is one skill of the list plus what the cockpit knows about it:
// a managed skill is the cockpit's own, written at start and kept current,
// so the page renders it locked instead of editable.
type SkillRow struct {
	coder.Skill
	Managed bool
}

// SkillsListData is the model for the skills list.
type SkillsListData struct {
	Page
	SettingsNav SettingsNav
	Base        string // canonical coder URL prefix, "/settings/coders/<id>"
	Skills      []SkillRow
}

// SkillsFormData is the model for create/edit skill forms.
type SkillsFormData struct {
	Page
	Base         string // canonical coder URL prefix, "/settings/coders/<id>"
	IsEdit       bool
	OriginalID   string
	ID           string
	Description  string
	Instructions string
	FormAction   string
	SubmitLabel  string
	Heading      string
}
