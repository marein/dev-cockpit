package assistant

import (
	"os"
	"testing"
)

// TestMain lets the test binary stand in as the turn process: a turn started
// by these tests execs the running binary, and in a test that binary is this
// one, with no command tree in front of it.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == runTurnArgs[0] && os.Args[2] == runTurnArgs[1] {
		os.Exit(RunTurn(os.Args[3:]))
	}
	os.Exit(m.Run())
}
