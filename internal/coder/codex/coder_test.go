package codex

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
)

func TestCodexKeepsEverythingUnderItsHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "/somewhere/codex")
	if got := codexHome(); got != "/somewhere/codex" {
		t.Fatalf("home = %s", got)
	}
	t.Setenv("CODEX_HOME", "")
	t.Setenv("HOME", "/home/someone")
	if got := codexHome(); got != filepath.Join("/home/someone", ".codex") {
		t.Fatalf("home = %s", got)
	}
}

func TestCodexIsRenamedWithItsOwnCommand(t *testing.T) {
	var c coder.Coder = New(nil)
	if c.ID() != "codex" || len(c.RequiredTools()) != 1 || c.RequiredTools()[0] != "codex" {
		t.Fatalf("id %q, tools %v", c.ID(), c.RequiredTools())
	}
	if mode := coder.RenameMode(c); mode != coder.RenameSend {
		t.Fatalf("mode = %q, want %q", mode, coder.RenameSend)
	}
	input := c.(coder.SessionRenamer).RenameInput("-n new name")
	if len(input) != 1 || input[0].Prompt != "/rename -n new name" {
		t.Fatalf("input = %+v", input)
	}
}

// codex starts no session with an agent: the form says so with the reason
// instead of a pick, a named agent is refused with a sentence saying why, and
// its agents stay listed for the Agents section.
func TestCodexStartsNoSessionWithAnAgent(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	agents := New(nil).AgentRepository()
	if id, err := agents.ValidateSelected("  "); err != nil || id != "" {
		t.Fatalf("no agent = %q, %v", id, err)
	}
	if _, err := agents.ValidateSelected("reviewer"); err == nil || !strings.Contains(err.Error(), "subagents") || !strings.Contains(err.Error(), "role in the prompt") {
		t.Fatalf("want an agent refused with codex's way and the prompt named, got %v", err)
	}
	if options := agents.Options(); len(options) != 1 || options[0].Value != "" {
		t.Fatalf("options = %+v, want the default alone", options)
	}
	if note := coder.NoAgentStartNote(New(nil)); note != "Not available for Codex, it runs its agents only as subagents." {
		t.Fatalf("want the form told that codex starts no session as an agent, and why, got %q", note)
	}
	if _, ok := coder.NewStandardAgentRepository(t.TempDir(), ".md").(coder.NoAgentStart); ok {
		t.Fatal("the agents of claude, copilot and opencode start a session")
	}
}

// The rollout marks every turn, so it is watched, and the bell is read from
// it. The screen stands in before the first message, when there is no
// rollout yet.
func TestCodexReportsItsTurnsFromItsRecord(t *testing.T) {
	var c coder.Coder = New(nil)
	profile := c.ActivityProfile()
	if !profile.WatchRecord || !profile.WatchScreen || profile.InterruptKeys {
		t.Fatalf("the profile is %+v", profile)
	}
	if _, ok := c.(coder.ActivityReporter); !ok {
		t.Fatal("a watched record needs a reporter")
	}
	if _, ok := c.(coder.ActivityStamper); !ok {
		t.Fatal("a watched record needs a stamper")
	}
	if _, ok := c.(coder.ConversationReader); !ok {
		t.Fatal("codex hands over no conversation")
	}
	if _, ok := c.(coder.BellReader); !ok {
		t.Fatal("codex's bell is not read")
	}
	if _, ok := c.(coder.TurnSignaler); ok {
		t.Fatal("codex drops no hooks, it has none to read")
	}
}
