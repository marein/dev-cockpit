package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// loginOutput is a recorded `claude auth login` start: the URL rides in a
// terminal hyperlink, and the prompt line has no newline behind it.
const loginOutput = "Opening browser to sign in…\n" +
	"If the browser didn't open, visit: \x1b]8;;https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a&state=l-RG58\x07https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a&state=l-RG58\x1b]8;;\x07\n" +
	"Paste code here if prompted > "

func TestReadLoginOutput(t *testing.T) {
	reading := claudeLogin{}.Read(loginOutput, "")
	if reading.URL != "https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a&state=l-RG58" {
		t.Fatalf("url = %q", reading.URL)
	}
	if !reading.Waiting {
		t.Fatal("the prompt line must read as waiting for the code")
	}
	if reading.Note != "" {
		t.Fatalf("note = %q, want none", reading.Note)
	}
}

func TestReadBeforeTheURL(t *testing.T) {
	reading := claudeLogin{}.Read("Opening browser to sign in…\n", "")
	if reading.URL != "" || reading.Waiting {
		t.Fatalf("reading = %+v, want empty", reading)
	}
}

// The complaint about a wrong code arrives on stderr while the prompt keeps
// standing, recorded from a bogus code pasted into a real flow.
func TestReadKeepsTheComplaint(t *testing.T) {
	reading := claudeLogin{}.Read(loginOutput, "Invalid code. Please make sure the full code was copied.\n")
	if !reading.Waiting {
		t.Fatal("a complained-about code keeps the flow waiting")
	}
	if reading.Note != "Invalid code. Please make sure the full code was copied." {
		t.Fatalf("note = %q", reading.Note)
	}
}

func TestParseAuthStatusLoggedIn(t *testing.T) {
	recorded := `{
  "loggedIn": true,
  "authMethod": "claude.ai",
  "apiProvider": "firstParty",
  "email": "user@example.test",
  "orgId": "03fbd40c",
  "orgName": "an org",
  "subscriptionType": "max"
}`
	state := parseAuthStatus([]byte(recorded))
	if !state.LoggedIn {
		t.Fatal("must read as logged in")
	}
	if state.Account != "user@example.test" {
		t.Fatalf("account = %q", state.Account)
	}
	if state.Detail != "claude.ai, max plan" {
		t.Fatalf("detail = %q", state.Detail)
	}
}

func TestParseAuthStatusLoggedOut(t *testing.T) {
	state := parseAuthStatus([]byte(`{"loggedIn": false}`))
	if state.LoggedIn || state.Account != "" {
		t.Fatalf("state = %+v, want logged out", state)
	}
}

func TestParseAuthStatusUnreadable(t *testing.T) {
	state := parseAuthStatus([]byte("no json at all"))
	if state.LoggedIn {
		t.Fatal("an unreadable status must count as not logged in")
	}
}

func readClaudeConfig(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := map[string]any{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return config
}

// A finished login seeds the onboarding flag, so the first terminal session
// starts on its task instead of on the theme wizard.
func TestLoginCompletedSeedsTheOnboardingFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	claudeLogin{}.LoginCompleted()
	config := readClaudeConfig(t, home)
	if done, _ := config[onboardingFlag].(bool); !done {
		t.Fatal("the flag must be set on a fresh config")
	}

	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"theme": "dark", "hasCompletedOnboarding": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	claudeLogin{}.LoginCompleted()
	config = readClaudeConfig(t, home)
	if done, _ := config[onboardingFlag].(bool); !done {
		t.Fatal("the flag must flip on an existing config")
	}
	if theme, _ := config["theme"].(string); theme != "dark" {
		t.Fatalf("theme = %q, the rest of the config must survive", theme)
	}
}
