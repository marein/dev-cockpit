//go:build !unix

package git

import "os/exec"

// killsWholeGroup has no process groups to work with here; the timeout keeps
// killing the one process and the wait delay keeps bounding the pipe wait.
func killsWholeGroup(cmd *exec.Cmd) {}
