package web

import (
	"os"
	"testing"

	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/detach"
)

// TestMain lets the test binary stand in as the hold process: an owned
// compose run is a real detached process started from the running binary, and
// in a test that binary is this one, with no command tree in front of it. The
// askpass helper the ssh agent's processes call is the running binary too.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == detach.HoldArgs[0] {
		os.Exit(detach.Hold(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "askpass" {
		if askpass.Helper(os.Args[2:], os.Stdout) {
			os.Exit(0)
		}
		os.Exit(1)
	}
	os.Exit(m.Run())
}
