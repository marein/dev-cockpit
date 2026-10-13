//go:build !linux

package sshagent

// protectMemory changes nothing here: darwin has no dumpable flag for one
// process, and a core limit of zero would carry over to every child the
// cockpit starts.
func protectMemory() error { return nil }
