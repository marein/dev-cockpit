//go:build !linux

package editorintelligence

import "os/exec"

// setChildProcAttr is a no op where parent death signals are unavailable.
// Language servers still exit on their own when stdin closes.
func setChildProcAttr(_ *exec.Cmd) {}
