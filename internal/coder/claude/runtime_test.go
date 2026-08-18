package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/coder/claude/statusline"
	"github.com/marein/dev-cockpit/internal/settings"
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

// A session without a name gets no --name at all: claude then titles it
// itself, while an empty value would be one it has to refuse.
func TestStartCommandLeavesTheNameOutWhenThereIsNone(t *testing.T) {
	command := runtime{}.StartCommand(coder.SessionStart{SessionID: "sid", Workdir: "/work", Name: "  "})
	if strings.Contains(command, "--name") {
		t.Errorf("a session without a name must not carry the flag: %s", command)
	}
	named := runtime{}.StartCommand(coder.SessionStart{SessionID: "sid", Workdir: "/work", Name: "a name"})
	if !strings.Contains(named, "--name 'a name'") {
		t.Errorf("a named session must carry its name: %s", named)
	}
}

// A picked model rides behind --model on a start alone. A start without one
// carries no flag, the CLI's own default is what such a session runs on, and
// a resume carries none whatever was picked: a resumed session keeps the
// model it has, /model inside it included.
func TestStartCommandCarriesTheModelAndAResumeNever(t *testing.T) {
	r := runtime{}
	command := r.StartCommand(coder.SessionStart{SessionID: "sid", Workdir: "/work", Model: " haiku "})
	if !strings.Contains(command, " --model 'haiku'") {
		t.Errorf("a picked model must ride behind --model: %s", command)
	}
	plain := r.StartCommand(coder.SessionStart{SessionID: "sid", Workdir: "/work"})
	if strings.Contains(plain, "--model") {
		t.Errorf("a session without a model must not carry the flag: %s", plain)
	}
	resume := r.ResumeCommand("sid", "/work", true)
	if strings.Contains(resume, "--model") {
		t.Errorf("a resume must never carry a model: %s", resume)
	}
}

// The status line rides the same blob, and only while the rendered line is
// really there: an install that never put one together must not have its own
// statusLine replaced by a command that draws nothing.
func TestSessionSettingsCarryTheStatusLineOnlyWithTheLine(t *testing.T) {
	line := filepath.Join(t.TempDir(), "claude-statusline.json")
	r := runtime{statusLine: line, statusLineCommand: "'/bin/dev-cockpit' claude status-line --state-dir '/state'"}
	var values map[string]any
	if err := json.Unmarshal([]byte(r.sessionSettings()), &values); err != nil {
		t.Fatalf("settings are not valid JSON: %v", err)
	}
	if _, ok := values["statusLine"]; ok {
		t.Fatal("a status line is injected without the line")
	}
	if err := os.WriteFile(line, []byte(`{"entries":[]}`), 0o600); err != nil {
		t.Fatalf("write the line: %v", err)
	}
	if err := json.Unmarshal([]byte(r.sessionSettings()), &values); err != nil {
		t.Fatalf("settings are not valid JSON: %v", err)
	}
	object, ok := values["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine = %v, want the command object", values["statusLine"])
	}
	if object["type"] != "command" || object["command"] != r.statusLineCommand {
		t.Fatalf("statusLine = %v, want the renderer as the command", object)
	}
	// Without a refresh interval claude draws the line on its own state changes
	// alone, and the clock, the age of the last commit and the time left on a
	// limit stand still between two answers.
	if object["refreshInterval"] != float64(statusLineRefreshSeconds) {
		t.Fatalf("statusLine refreshInterval = %v, want %d seconds", object["refreshInterval"], statusLineRefreshSeconds)
	}
	// A binary whose path could not be read has no command to hand over.
	r.statusLineCommand = ""
	if strings.Contains(r.sessionSettings(), "statusLine") {
		t.Fatal("a status line is injected without a command to draw it")
	}
}

// The fallback steps back from a line the global settings set, and from one it
// cannot read; always does not.
func TestSessionSettingsStatusLineFollowsTheMode(t *testing.T) {
	dir := t.TempDir()
	line := filepath.Join(dir, "claude-statusline.json")
	if err := os.WriteFile(line, []byte(`{"entries":[]}`), 0o600); err != nil {
		t.Fatalf("write the line: %v", err)
	}
	own := filepath.Join(dir, "settings.json")
	store := settings.New(filepath.Join(dir, "cockpit.json"))
	r := runtime{statusLine: line, statusLineCommand: "draw", userSettings: own, store: store}
	carries := func() bool {
		var values map[string]any
		if err := json.Unmarshal([]byte(r.sessionSettings()), &values); err != nil {
			t.Fatalf("settings are not valid JSON: %v", err)
		}
		_, ok := values["statusLine"]
		return ok
	}
	cases := []struct {
		name string
		mode statusline.Mode
		file string
		want bool
	}{
		{"fallback without a settings file", statusline.ModeFallback, "", true},
		{"fallback with settings that set no line", statusline.ModeFallback, `{"theme":"dark","statusLine":null}`, true},
		{"fallback with a line of the user's own", statusline.ModeFallback, `{"statusLine":{"type":"command","command":"x"}}`, false},
		{"fallback with settings it cannot read", statusline.ModeFallback, `{`, false},
		{"always over a line of the user's own", statusline.ModeAlways, `{"statusLine":{"type":"command","command":"x"}}`, true},
	}
	for _, c := range cases {
		_ = os.Remove(own)
		if c.file != "" {
			if err := os.WriteFile(own, []byte(c.file), 0o600); err != nil {
				t.Fatalf("write the user settings: %v", err)
			}
		}
		store.Set(statusline.SettingKey, statusline.Encode(statusline.Config{Mode: c.mode}))
		if got := carries(); got != c.want {
			t.Errorf("%s: statusLine carried = %v, want %v", c.name, got, c.want)
		}
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
