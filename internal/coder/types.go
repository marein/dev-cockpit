package coder

import "time"

// Running is one live tmux session backed by a recognised coder process.
type Running struct {
	Identifier  string // stable session ID used in URLs/UI
	TmuxSession string // underlying tmux session name
	PID         string
	Name        string
	StartedAt   time.Time
	CWD         string
}

// StartOptions control how a new provider session is launched.
type StartOptions struct {
	AutomaticApproval bool
}

// Snapshot captures the present state of every session category.
type Snapshot struct {
	Running   []Running
	Inactive  []Session
	Resumable []Session // every stored session, including those already running
}
