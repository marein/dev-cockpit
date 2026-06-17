package render

import "github.com/local/dev-cockpit/internal/session"

// ShellsData is the model for the shells list page.
type ShellsData struct {
	Page
	Shells []session.Shell
}

// ShellAttachData is the model for the shell attach page.
type ShellAttachData struct {
	Page
	Shell     session.Shell
	StreamURL string
	ResizeURL string
	InputURL  string
	RenameURL string
}
