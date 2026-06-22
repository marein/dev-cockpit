package render

import (
	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/provider"
	"github.com/local/dev-cockpit/internal/session"
)

// SessionNewData is the model for the new-session form. Project is chosen from a
// select (preselected to DefaultPath, e.g. the project you came from).
type SessionNewData struct {
	Page
	Projects          []string
	DefaultPath       string
	Agents            []provider.AgentOption
	DefaultAgent      string
	RemoteControl     bool
	AutomaticApproval bool
	Return            string // where Cancel goes back to (the page you came from)
}

// SessionAttachData is the model for the attach page.
type SessionAttachData struct {
	Page
	Session           session.Running
	SessionIdentifier string
	ProjectName       string // owning project, empty when CWD is outside the projects root
	Files             []filesystem.File
	MaxUploadSizeMB   string
	Error             string
	Message           string
	StreamURL         string
	ResizeURL         string
	InputURL          string
}

// SessionFilesData is the model for the session files HTML fragment.
type SessionFilesData struct {
	Page
	SessionIdentifier string
	Files             []filesystem.File
	MaxUploadSizeMB   string
	Error             string
	Message           string
}
