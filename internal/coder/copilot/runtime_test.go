package copilot

import (
	"strings"
	"testing"

	"github.com/local/dev-cockpit/internal/coder"
)

// A task reaches copilot through --interactive, which starts the session and
// runs that prompt. Typing it into the pane afterwards is what used to lose it.
func TestStartCommandCarriesTheTask(t *testing.T) {
	command := runtime{}.StartCommand(coder.SessionStart{
		SessionID: "sid", Name: "name", Workdir: "/work", Task: "Fix the login redirect",
	})
	if !strings.Contains(command, "--interactive 'Fix the login redirect'") {
		t.Errorf("task is not passed to copilot: %s", command)
	}
	plain := runtime{}.StartCommand(coder.SessionStart{SessionID: "sid", Name: "name", Workdir: "/work"})
	if strings.Contains(plain, "--interactive") {
		t.Errorf("a session without a task must not ask for an interactive prompt: %s", plain)
	}
}

// copilot records every session as an event log, so it answers activity from
// that record: the capability is what keeps the manager off the screen, whose
// input line carries the CLI's own draft. The reading itself is tested in
// activity_test.go.
func TestCopilotReportsSessionActivity(t *testing.T) {
	var c any = New()
	if _, ok := c.(coder.ActivityReporter); !ok {
		t.Fatal("copilot keeps an event log, it has to answer from it")
	}
}
