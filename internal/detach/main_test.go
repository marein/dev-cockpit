package detach

import (
	"os"
	"testing"
)

// TestMain lets the test binary stand in as the hold process: a run started by
// these tests execs the running binary, and in a test that binary is this one,
// with no command tree in front of it.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == HoldArgs[0] {
		os.Exit(Hold(os.Args[2:]))
	}
	os.Exit(m.Run())
}
