package render

import "github.com/local/dev-cockpit/internal/shell"

// ShellNewData is the model for the new-shell form. Project is chosen from a
// select (preselected to DefaultPath, e.g. the project you came from).
type ShellNewData struct {
	Page
	Projects    []string
	DefaultPath string
	Return      string // where Cancel goes back to (the page you came from)
}

// ShellAttachData is the model for the shell attach page.
type ShellAttachData struct {
	Page
	Shell       shell.Shell
	ProjectName string // owning project, empty for home/ungrouped shells
	Tabs        []TerminalTab
	StreamURL   string
	ResizeURL   string
	InputURL    string
	RenameURL   string
}
