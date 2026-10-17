package copilot

import (
	"errors"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
)

func TestTheLevelsAreCopilotsOwn(t *testing.T) {
	if got := strings.Join((&Coder{}).Efforts(), ","); got != "none,minimal,low,medium,high,xhigh,max" {
		t.Fatalf("want copilot's own levels, got %s", got)
	}
}

func TestStartCommandCarriesTheEffortAndAResumeNever(t *testing.T) {
	command := runtime{}.StartCommand(coder.SessionStart{SessionID: "sid", Workdir: "/work", Effort: "minimal"})
	if !strings.Contains(command, " --reasoning-effort 'minimal'") {
		t.Errorf("a picked level must ride behind --reasoning-effort: %s", command)
	}
	if plain := (runtime{}).StartCommand(coder.SessionStart{SessionID: "sid", Workdir: "/work"}); strings.Contains(plain, "--reasoning-effort") {
		t.Errorf("a session without a level must not carry the flag: %s", plain)
	}
	if resume := (runtime{}).ResumeCommand("sid", "/work", true); strings.Contains(resume, "--reasoning-effort") {
		t.Errorf("a resume must never carry a level: %s", resume)
	}
}

func TestATurnCarriesTheEffortBehindItsFlag(t *testing.T) {
	cmd, err := testRunner(t).Command(assistant.TurnRequest{SessionID: sessionID, Workdir: t.TempDir(), Prompt: "hello", Model: "gpt-5.4", Effort: "xhigh"})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if argv := strings.Join(cmd.Args, " "); !strings.Contains(argv, "--model gpt-5.4 --reasoning-effort xhigh") {
		t.Fatalf("want the level behind --reasoning-effort, got %q", argv)
	}
}

// The three lines copilot 1.0.98 prints for a level it refuses name an effort
// refusal, the one that opens like a model's line included, and the model's
// own line stays a model refusal.
func TestDiagnoseNamesAnEffortCopilotRefuses(t *testing.T) {
	parser := testRunner(t).Parse(sessionID, make(chan assistant.Event, 1))
	generic := errors.New("The coder stopped before it finished the answer.")
	for _, c := range []struct{ stderr, effort, model string }{
		{"Error: Reasoning effort \"max\" is not supported for model \"gpt-5-mini\".\n", "max", "gpt-5-mini"},
		{"Error: Model \"claude-haiku-4.5\" does not support reasoning effort configuration (requested: \"high\").\n", "high", "claude-haiku-4.5"},
		{"error: invalid value 'bogus' for '--reasoning-effort <level>'\n  [possible values: none, minimal, low, medium, high, xhigh, max]\n", "bogus", ""},
	} {
		var refusal *assistant.Refusal
		err := parser.Diagnose(generic, c.stderr)
		if !errors.As(err, &refusal) || refusal.Kind != assistant.RefusalUnknownEffort || refusal.Effort != c.effort || refusal.Model != c.model {
			t.Fatalf("%q: want an effort refusal of %s for %s, got %+v", c.stderr, c.effort, c.model, err)
		}
	}
	var refusal *assistant.Refusal
	err := parser.Diagnose(generic, "Error: Model \"no-such-model\" from --model flag is not available.\n")
	if !errors.As(err, &refusal) || refusal.Kind != assistant.RefusalUnknownModel {
		t.Fatalf("want the model's line to stay a model refusal, got %v", err)
	}
}
