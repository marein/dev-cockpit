package render

// InstructionsData is the model for the global instructions editor.
type InstructionsData struct {
	Page
	CoderNav     CoderNav
	Base         string // canonical coder URL prefix, "/coders/<id>"
	Instructions string
}
