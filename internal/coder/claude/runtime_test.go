package claude

import (
	"encoding/json"
	"strings"
	"testing"

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
	if !strings.HasSuffix(command, "'Fix the login redirect'") {
		t.Errorf("task is not the positional prompt: %s", command)
	}
	plain := r.StartCommand(coder.SessionStart{SessionID: "sid", Name: "name", Workdir: "/work"})
	if strings.Contains(plain, "''") {
		t.Errorf("a session without a task must not carry an empty prompt: %s", plain)
	}
}
