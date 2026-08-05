package docker

import (
	"os"
	"testing"

	"github.com/local/dev-cockpit/internal/detach"
)

// TestMain lets the test binary stand in as the hold process: a compose run is
// a real detached process started from the running binary, and in a test that
// binary is this one, with no command tree in front of it.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == detach.HoldArgs[0] {
		os.Exit(detach.Hold(os.Args[2:]))
	}
	os.Exit(m.Run())
}
