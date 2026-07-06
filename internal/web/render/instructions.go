package render

// InstructionsData is the model for the global instructions editor.
type InstructionsData struct {
	Page
	CoderTabs     CoderTabs
	SelectedCoder string
	Instructions  string
}
