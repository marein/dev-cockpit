package opencode

import (
	"net/http"
	"os"
	"testing"

	"github.com/marein/dev-cockpit/internal/clirun"
)

// TestMain lets the test binary stand in as the guard DiesWithParent starts
// where the kernel has no parent death signal, and as `opencode serve` for
// tests that boot a server where no opencode is installed.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == clirun.GuardCommand[0] {
		os.Exit(clirun.Guard())
	}
	if os.Getenv("OPENCODE_FAKE_SERVE") == "1" {
		standInServe()
	}
	os.Exit(m.Run())
}

// standInServe answers every request on the port it was started with, which is
// all startServe waits for.
func standInServe() {
	port := ""
	for i, arg := range os.Args[:len(os.Args)-1] {
		if arg == "--port" {
			port = os.Args[i+1]
		}
	}
	_ = http.ListenAndServe("127.0.0.1:"+port, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	os.Exit(1)
}
