package render

import (
	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/filesystem"
)

// CoderChoice is one selectable coder in the new-coder form together with its
// agent choices. NeedsLogin marks a coder whose CLI is installed but not
// logged in, which the form answers with the login hint.
type CoderChoice struct {
	ID           string
	Agents       []coder.AgentOption
	DefaultAgent string
	NeedsLogin   bool
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
	// SplitGroup and SplitColumn carry a split view the new coder joins right
	// after it starts: the group id, and a member of the column it stacks into
	// (empty for a column of its own at the right edge). They ride the query
	// into the form and back out through the POST, like Return does.
	SplitGroup  string
	SplitColumn string
}

// CoderAccountData is the model for a coder's account section: the login
// state the probe read, and the base the login routes hang under.
type CoderAccountData struct {
	Page
	SettingsNav SettingsNav
	Base        string
	CoderID     string
	LoggedIn    bool
	Account     string
	Detail      string
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
