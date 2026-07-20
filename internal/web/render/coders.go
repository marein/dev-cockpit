package render

import (
	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/filesystem"
)

// CoderChoice is one selectable coder in the new-coder form together with its
// agent choices.
type CoderChoice struct {
	ID           string
	Agents       []coder.AgentOption
	DefaultAgent string
}

// CoderNewData is the model for the new-coder form. Project is chosen from a
// select (preselected to DefaultPath, e.g. the project you came from).
type CoderNewData struct {
	Page
	Projects          []string
	DefaultPath       string
	Coders            []CoderChoice
	SelectedCoder     string
	AutomaticApproval bool
	Return            string // where Cancel goes back to (the page you came from)
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
