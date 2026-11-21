package cli

import (
	"os"
	"testing"

	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/detach"
)

// TestMain lets the test binary stand in as the hold process: a turn started
// by a test through the real assistant service execs the running binary, and
// in a test that binary is this one, with no command tree in front of it. The
// same holds for the guard of clirun.DiesWithParent.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == detach.HoldArgs[0] {
		os.Exit(detach.Hold(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == clirun.GuardCommand[0] {
		os.Exit(clirun.Guard())
	}
	os.Exit(m.Run())
}
