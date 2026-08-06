//go:build !linux

package hostinfo

// Only Linux publishes busy counters the binary can read without cgo, so
// everywhere else the busy number stays the load average against the cores.
// This file and not read_darwin.go, so the darwin readers stay what they are:
// pure command output in, numbers out.
func cpu() (busy int, label string, ok bool) { return loadCPU() }
