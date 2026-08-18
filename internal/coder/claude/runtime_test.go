package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// The status line rides the same blob, and only while the generated script is
// really there: an install that never put one together must not have its own
// statusLine replaced by a command that points at nothing.
func TestSessionSettingsCarryTheStatusLineOnlyWithTheScript(t *testing.T) {
	script := filepath.Join(t.TempDir(), "claude-statusline.sh")
	r := runtime{statusLine: script}
	var values map[string]any
	if err := json.Unmarshal([]byte(r.sessionSettings()), &values); err != nil {
		t.Fatalf("settings are not valid JSON: %v", err)
	}
	if _, ok := values["statusLine"]; ok {
		t.Fatal("a status line is injected without the script")
	}
	if err := os.WriteFile(script, []byte("#!/bin/bash\n"), 0o700); err != nil {
		t.Fatalf("write the script: %v", err)
	}
	if err := json.Unmarshal([]byte(r.sessionSettings()), &values); err != nil {
		t.Fatalf("settings are not valid JSON: %v", err)
	}
	line, ok := values["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine = %v, want the command object", values["statusLine"])
	}
	if line["type"] != "command" || line["command"] != script {
		t.Fatalf("statusLine = %v, want the script as a command", line)
	}
	// Without a refresh interval claude draws the line on its own state changes
	// alone, and the clock, the age of the last commit and the time left on a
	// limit stand still between two answers.
	if line["refreshInterval"] != float64(statusLineRefreshSeconds) {
		t.Fatalf("statusLine refreshInterval = %v, want %d seconds", line["refreshInterval"], statusLineRefreshSeconds)
	}
	if (runtime{}).sessionSettings() == r.sessionSettings() {
		t.Fatal("a coder without a script carries the same settings as one with it")
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
