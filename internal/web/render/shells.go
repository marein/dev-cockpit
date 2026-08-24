package render

import "github.com/marein/dev-cockpit/internal/shell"

// ShellNewData is the model for the new-shell form. Project is chosen from a
// select that stands in the order the projects page is in; DefaultPath holds
// the project the form was opened from and is empty without one, then the first
// project of that order is the preselection.
type ShellNewData struct {
	Page
	Projects    []ProjectOption
	DefaultPath string
	Return      string // where Cancel goes back to (the page you came from)
	// SplitGroup and SplitColumn carry a split view the new shell joins right
	// after it starts: the group id, and a member of the column it stacks into
	// (empty for a column of its own at the right edge). They ride the query
	// into the form and back out through the POST, like Return does.
	SplitGroup  string
	SplitColumn string
}

// ShellAttachData is the model for the shell attach page.
type ShellAttachData struct {
	Page
	Shell       shell.Shell
	ProjectName string // owning project, empty for home/ungrouped shells
	StreamURL   string
	ResizeURL   string
	InputURL    string
	RenameURL   string
}
