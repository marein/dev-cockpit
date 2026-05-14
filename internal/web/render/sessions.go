package render

import (
	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/provider"
	"github.com/local/dev-cockpit/internal/session"
)

// SessionsData is the model for the sessions list page.
type SessionsData struct {
	Page
	Snapshot session.Snapshot
}

// SessionNewData is the model for the new-session form.
type SessionNewData struct {
	Page
	Projects          []string
	DefaultPath       string
	Agents            []provider.AgentOption
	DefaultAgent      string
	RemoteControl     bool
	AutomaticApproval bool
}

// SessionAttachData is the model for the attach page.
type SessionAttachData struct {
	Page
	Session           session.Running
	SessionIdentifier string
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
