package render

import (
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
)

// CoderChoice is one selectable coder in the new-coder form together with its
// agent choices.
type CoderChoice struct {
	ID           string
	Agents       []coder.AgentOption
	DefaultAgent string
}

// CoderNewData is the model for the new-coder form. Project is chosen from a
// select that stands in the order the projects page is in; DefaultPath holds
// the project the form was opened from and is empty without one, then the first
// project of that order is the preselection.
type CoderNewData struct {
	Page
	Projects          []ProjectOption
	DefaultPath       string
	Coders            []CoderChoice
	SelectedCoder     string
	AutomaticApproval bool
	Return            string // where Cancel goes back to (the page you came from)
	// Panel marks a create opened from the editor terminal panel's + menu, the
	// one caller whose create goes back to the editor instead of the coder's
	// page. It rides the query into the form and back out through the POST,
	// like Return does; Return alone cannot carry this, every quick nav link
	// on an editor page sends the same editor return for its Cancel.
	Panel bool
	// SplitGroup and SplitColumn carry a split view the new coder joins right
	// after it starts: the group id, and a member of the column it stacks into
	// (empty for a column of its own at the right edge). They ride the query
	// into the form and back out through the POST, like Return does.
	SplitGroup  string
	SplitColumn string
}

// CoderAttachData is the model for the attach page.
type CoderAttachData struct {
	Page
	Running         coder.Running
	Identifier      string
	Coder           string // owning coder id
	ProjectName     string // owning project, empty when CWD is outside the projects root
	Files           []filesystem.File
	MaxUploadSizeMB string
	Error           string
	Message         string
	StreamURL       string
	ResizeURL       string
	InputURL        string
}

// CoderFilesData is the model for the coder files HTML fragment.
type CoderFilesData struct {
	Page
	Identifier      string
	Files           []filesystem.File
	MaxUploadSizeMB string
	Error           string
	Message         string
}
