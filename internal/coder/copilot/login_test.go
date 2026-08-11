package copilot

import (
	"os"
	"path/filepath"
	"testing"
)

// deviceOutput is a recorded `copilot login --device-code` start.
const deviceOutput = "To authenticate, visit https://github.com/login/device and enter code A2D1-6C79\nWaiting for authorization...\n"

func TestReadDeviceLine(t *testing.T) {
	reading := copilotLogin{}.Read(deviceOutput, "")
	if reading.URL != "https://github.com/login/device" {
		t.Fatalf("url = %q", reading.URL)
	}
	if reading.Code != "A2D1-6C79" {
		t.Fatalf("code = %q", reading.Code)
	}
	if reading.Waiting {
		t.Fatal("the device flow never waits for a pasted code")
	}
}

func TestReadBeforeTheDeviceLine(t *testing.T) {
	reading := copilotLogin{}.Read("", "")
	if reading.URL != "" || reading.Code != "" {
		t.Fatalf("reading = %+v, want empty", reading)
	}
}

func clearTokenVariables(t *testing.T) {
	t.Helper()
	for _, name := range tokenVariables {
		t.Setenv(name, "")
	}
}

// The config shape is recorded from a logged in copilot, comment header and
// all; the probe reads it through the same reader the trust handling uses.
func TestProbeReadsTheStoredLogin(t *testing.T) {
	clearTokenVariables(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	config := `// User settings belong in settings.json.
// This file is managed automatically.
{
  "lastLoggedInUser": {
    "host": "https://github.com",
    "login": "marein"
  },
  "loggedInUsers": [
    {
      "host": "https://github.com",
      "login": "marein"
    }
  ]
}`
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".copilot", "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	state := copilotLogin{}.Probe()
	if !state.LoggedIn {
		t.Fatal("must read as logged in")
	}
	if state.Account != "marein" {
		t.Fatalf("account = %q", state.Account)
	}
	if state.Detail != "" {
		t.Fatalf("detail = %q, want none for github.com", state.Detail)
	}
}

func TestProbeWithoutAnyLogin(t *testing.T) {
	clearTokenVariables(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if state := (copilotLogin{}).Probe(); state.LoggedIn {
		t.Fatalf("state = %+v, want logged out", state)
	}
}

func TestProbeHonorsTheEnvironmentToken(t *testing.T) {
	clearTokenVariables(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GH_TOKEN", "gho_recorded")
	state := copilotLogin{}.Probe()
	if !state.LoggedIn {
		t.Fatal("an environment token counts as logged in")
	}
	if state.Detail != "Uses the GH_TOKEN token from the environment." {
		t.Fatalf("detail = %q", state.Detail)
	}
}

func TestStateFromConfigNamesAnEnterpriseHost(t *testing.T) {
	state := stateFromConfig(map[string]any{
		"loggedInUsers":    []any{map[string]any{"host": "https://example.ghe.com", "login": "marein"}},
		"lastLoggedInUser": map[string]any{"host": "https://example.ghe.com", "login": "marein"},
	})
	if !state.LoggedIn || state.Account != "marein" {
		t.Fatalf("state = %+v", state)
	}
	if state.Detail != "example.ghe.com" {
		t.Fatalf("detail = %q", state.Detail)
	}
}
