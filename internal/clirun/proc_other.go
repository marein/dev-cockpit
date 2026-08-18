//go:build !unix

package clirun

import "os/exec"

// KillsWholeGroup has no process groups to work with here; the context keeps
// killing the one process and a wait delay keeps bounding the pipe wait.
func KillsWholeGroup(cmd *exec.Cmd) {}
