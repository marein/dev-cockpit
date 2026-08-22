package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/detach"
)

// A server of the previous version execs this binary with the argv it knows,
// which is what every turn it starts does while a new binary already sits on
// disk. Both shapes have to reach the hold process, or those turns die with an
// unknown command and the user reads the coder's stderr about it.
func TestBothHoldCommandsAreReachable(t *testing.T) {
	for _, argv := range [][]string{
		{"assistant", "run-turn", "claude", "-p"},
		{detach.HoldArgs[0], "--", "claude", "-p"},
	} {
		found, _, err := newRootCommand().Find(argv)
		if err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		if !strings.HasPrefix(found.Use, "run-turn") && !strings.HasPrefix(found.Use, detach.HoldArgs[0]) {
			t.Fatalf("%v reached %q instead of the hold process", argv, found.Use)
		}
		if !found.DisableFlagParsing {
			t.Fatalf("%v reached a command that parses flags, a provider's own flags would be read as ours", argv)
		}
	}
}

// The old argv carries no separator and no flags of ours, and it has to run the
// program exactly as before, exit code included.
func TestTheOldArgvStillRunsTheProgram(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = file
	code := detach.Hold([]string{"sh", "-c", "echo held; exit 7"})
	os.Stdout = stdout
	file.Close()
	if code != 7 {
		t.Fatalf("the exit code answered %d", code)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "held") {
		t.Fatalf("the program's output is missing: %q", raw)
	}
}
