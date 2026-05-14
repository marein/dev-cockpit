// Package proctree inspects process trees across supported platforms.
package proctree

type strategy interface {
	Descendants(rootPID int) []int
	Cmdline(pid int) []string
	CWD(pid int) string
	Environ(pid int) map[string]string
}

var processTree strategy = newStrategy()

// Descendants returns root and all its transitive child PIDs.
func Descendants(rootPID int) []int { return processTree.Descendants(rootPID) }

// Cmdline returns the argv of the given process.
func Cmdline(pid int) []string { return processTree.Cmdline(pid) }

// CWD returns the working directory of the given process with symlinks resolved.
func CWD(pid int) string { return processTree.CWD(pid) }

func Environ(pid int) map[string]string { return processTree.Environ(pid) }
