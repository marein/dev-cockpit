package copilot

import (
	"strings"
	"testing"

	"github.com/local/dev-cockpit/internal/clirun"
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

// A task is text, whatever it starts with. It rides as the value of
// --interactive, which copilot requires an argument for and therefore takes
// whatever the next word is (measured on GitHub Copilot CLI 1.0.78), so the task
// reaches the session as text and an end of options separator would only become
// the task itself.
func TestStartCommandCarriesADashLeadingTaskAsText(t *testing.T) {
	tasks := map[string]string{
		"a php option somebody pasted": "-dxdebug.idekey=PHPSTORM",
		"a long flag":                  "--help",
		"a bare dash":                  "-",
		"an ordinary task":             "Fix the login redirect",
	}
	for name, task := range tasks {
		command := runtime{}.StartCommand(coder.SessionStart{
			SessionID: "sid", Name: "name", Workdir: "/work", Task: task,
		})
		want := "--interactive " + clirun.ShellQuote(task)
		if !strings.HasSuffix(command, want) {
			t.Errorf("%s: want %q at the end, got %s", name, want, command)
		}
		if strings.Contains(command, " -- ") {
			t.Errorf("%s: a separator here would be the task, got %s", name, command)
		}
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
