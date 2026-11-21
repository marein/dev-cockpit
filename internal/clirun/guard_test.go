//go:build unix

package clirun

import (
	"os"
	"testing"
)

// TestMain lets the test binary stand in as the guard: DiesWithParent runs
// this binary where the kernel has no parent death signal, and in a test that
// binary is this one, with no command tree in front of it. The build tag
// nopdeathsig takes that path on Linux too, so the tests here, in opencode
// and in editorintelligence prove it on Linux.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == GuardCommand[0] {
		os.Exit(Guard())
	}
	os.Exit(m.Run())
}
