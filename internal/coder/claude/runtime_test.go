package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/local/dev-cockpit/internal/clirun"
	"github.com/local/dev-cockpit/internal/coder"
)

func TestSessionSettings(t *testing.T) {
	r := runtime{notifyInbox: "/tmp/inbox"}
	var values map[string]any
	if err := json.Unmarshal([]byte(r.sessionSettings()), &values); err != nil {
		t.Fatalf("settings are not valid JSON: %v", err)
	}
	if values["theme"] != "auto" {
		t.Errorf("theme = %v, want auto", values["theme"])
	}
	if values["disableAgentView"] != true {
		t.Errorf("disableAgentView = %v, want true", values["disableAgentView"])
	}
	if _, ok := values["hooks"]; !ok {
		t.Error("hooks missing with notify inbox set")
	}
}

func TestStartCommandCarriesSettings(t *testing.T) {
	r := runtime{}
	command := r.StartCommand(coder.SessionStart{SessionID: "sid", Name: "name", Workdir: "/work"})
	if !strings.Contains(command, "--settings") || !strings.Contains(command, "disableAgentView") {
		t.Errorf("start command misses settings injection: %s", command)
	}
}

// A task reaches the CLI in its argv, as claude's positional prompt. Typing it
// into the pane afterwards is what used to lose it.
func TestStartCommandCarriesTheTask(t *testing.T) {
	r := runtime{}
	command := r.StartCommand(coder.SessionStart{
		SessionID: "sid", Name: "name", Workdir: "/work", Task: "Fix the login redirect",
	})
	if !strings.HasSuffix(command, "-- 'Fix the login redirect'") {
		t.Errorf("task is not the positional prompt: %s", command)
	}
	plain := r.StartCommand(coder.SessionStart{SessionID: "sid", Name: "name", Workdir: "/work"})
	if strings.Contains(plain, "''") {
		t.Errorf("a session without a task must not carry an empty prompt: %s", plain)
	}
	if strings.Contains(plain, " -- ") {
		t.Errorf("a session without a task must not carry the separator: %s", plain)
	}
}

// A task is text, whatever it starts with. The shell quoting protects the shell
// alone, so the end of options separator is what keeps claude's own parser from
// reading the task as an option: measured on claude 2.1.226,
// -dxdebug.idekey=PHPSTORM is taken as the short flag -d with a filter and the
// task never reaches the session.
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
		want := "-- " + clirun.ShellQuote(task)
		if !strings.HasSuffix(command, want) {
			t.Errorf("%s: want %q at the end, got %s", name, want, command)
		}
		// The separator is the last thing before the task, so every flag of the
		// session stands in front of it.
		if strings.Count(command, " -- ") != 1 {
			t.Errorf("%s: want one separator, got %s", name, command)
		}
	}
}
