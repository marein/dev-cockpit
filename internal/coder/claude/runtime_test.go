package claude

import (
	"encoding/json"
	"strings"
	"testing"
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
	command := r.StartCommand("sid", "name", "/work", "", false)
	if !strings.Contains(command, "--settings") || !strings.Contains(command, "disableAgentView") {
		t.Errorf("start command misses settings injection: %s", command)
	}
}
