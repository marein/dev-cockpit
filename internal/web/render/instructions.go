package render

// InstructionsData is the model for the global instructions editor.
type InstructionsData struct {
	Page
	SettingsNav  SettingsNav
	Base         string // canonical coder URL prefix, "/settings/coders/<id>"
	Instructions string
}
