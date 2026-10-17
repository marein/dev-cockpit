package claude

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
)

func TestTheLevelsAreClaudesOwn(t *testing.T) {
	if got := strings.Join((&Coder{}).Efforts(), ","); got != "low,medium,high,xhigh,max" {
		t.Fatalf("want claude's own levels, got %s", got)
	}
}

// A level rides behind --effort on a start, never on a resume: a resumed
// session keeps the level it has.
func TestStartCommandCarriesTheEffortAndAResumeNever(t *testing.T) {
	r := runtime{}
	command := r.StartCommand(coder.SessionStart{SessionID: "sid", Workdir: "/work", Model: "opus", Effort: " high "})
	if !strings.Contains(command, " --model 'opus' --effort 'high'") {
		t.Errorf("a picked level must ride behind --effort: %s", command)
	}
	if plain := r.StartCommand(coder.SessionStart{SessionID: "sid", Workdir: "/work"}); strings.Contains(plain, "--effort") {
		t.Errorf("a session without a level must not carry the flag: %s", plain)
	}
	if resume := r.ResumeCommand("sid", "/work", true); strings.Contains(resume, "--effort") {
		t.Errorf("a resume must never carry a level: %s", resume)
	}
}

func TestATurnCarriesTheEffortBeforeThePrompt(t *testing.T) {
	r := &runner{sessions: stubSessions{}}
	cmd, err := r.Command(assistant.TurnRequest{SessionID: sessionID, Workdir: t.TempDir(), Prompt: "hello", Model: "haiku", Effort: "max"})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	argv := strings.Join(cmd.Args, " ")
	if !strings.Contains(argv, " --model haiku --effort max -- hello") {
		t.Fatalf("want the level behind its flag and before the prompt, got %q", argv)
	}
	cmd, err = r.Command(assistant.TurnRequest{SessionID: sessionID, Workdir: t.TempDir(), Prompt: "hello"})
	if err != nil || strings.Contains(strings.Join(cmd.Args, " "), "--effort") {
		t.Fatalf("want no level on a turn that names none, got %v %v", cmd.Args, err)
	}
}
