package coderlogin

import (
	"strings"
	"testing"
	"time"
)

// scriptLogin stands in for a coder CLI with an inline shell script. Its
// output protocol is deliberately tiny: a "url:" line, an optional "code:"
// line, a "paste> " prompt while it waits for stdin.
type scriptLogin struct {
	script    string
	takesCode bool
}

func (l scriptLogin) Command() (string, []string) { return "sh", []string{"-c", l.script} }
func (l scriptLogin) TakesCode() bool             { return l.takesCode }
func (l scriptLogin) Probe() State                { return State{} }

func (l scriptLogin) Read(stdout, stderr string) Reading {
	reading := Reading{Note: LastLine(stderr), Waiting: strings.Contains(stdout, "paste> ")}
	for _, line := range strings.Split(stdout, "\n") {
		if rest, ok := strings.CutPrefix(line, "url: "); ok {
			reading.URL = strings.TrimSpace(rest)
		}
		if rest, ok := strings.CutPrefix(line, "code: "); ok {
			reading.Code = strings.TrimSpace(rest)
		}
	}
	return reading
}

func waitFor(t *testing.T, s *Service, id, want string, cond func(FlowState) bool) FlowState {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last Description
	for time.Now().Before(deadline) {
		last = s.Describe(id)
		if last.Flow != nil && cond(*last.Flow) {
			return *last.Flow
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waiting for %s, last state %+v", want, last.Flow)
	return FlowState{}
}

// The claude shape: a URL, a paste prompt, a complaint on stderr about a
// wrong code, then success on the right one.
func TestFlowWithAPastedCode(t *testing.T) {
	script := `echo "url: https://example.test/auth"
printf "paste> "
read line
if [ "$line" = "right" ]; then exit 0; fi
echo "That code is wrong." >&2
read line
if [ "$line" = "right" ]; then exit 0; fi
exit 1`
	s := NewService(map[string]Login{"c": scriptLogin{script: script, takesCode: true}})
	if err := s.Start("c"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waiting := waitFor(t, s, "c", "waiting", func(f FlowState) bool { return f.State == "waiting" })
	if waiting.URL != "https://example.test/auth" {
		t.Fatalf("url = %q", waiting.URL)
	}
	if !waiting.TakesCode {
		t.Fatal("the flow must say it takes a code")
	}
	if err := s.Answer("c", "wrong"); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	complained := waitFor(t, s, "c", "the complaint", func(f FlowState) bool { return f.Note != "" })
	if complained.State != "waiting" {
		t.Fatalf("state = %q, want waiting for the retry", complained.State)
	}
	if complained.Note != "That code is wrong." {
		t.Fatalf("note = %q", complained.Note)
	}
	if err := s.Answer("c", "right"); err != nil {
		t.Fatalf("Answer retry: %v", err)
	}
	waitFor(t, s, "c", "done", func(f FlowState) bool { return f.State == "done" })
}

// The copilot shape: URL and code shown, no answer taken, the process
// finishes on its own.
func TestFlowThatFinishesOnItsOwn(t *testing.T) {
	script := `echo "url: https://example.test/device"
echo "code: AAAA-1111"
sleep 0.2
exit 0`
	s := NewService(map[string]Login{"c": scriptLogin{script: script}})
	if err := s.Start("c"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	shown := waitFor(t, s, "c", "the code", func(f FlowState) bool { return f.Code != "" || f.State == "done" })
	if shown.State != "done" {
		if shown.Code != "AAAA-1111" || shown.URL != "https://example.test/device" {
			t.Fatalf("shown = %+v", shown)
		}
		if err := s.Answer("c", "anything"); err == nil {
			t.Fatal("a codeless flow must refuse an answer")
		}
	}
	waitFor(t, s, "c", "done", func(f FlowState) bool { return f.State == "done" })
}

func TestFlowCancelKillsTheProcess(t *testing.T) {
	script := `echo "url: https://example.test/auth"
printf "paste> "
sleep 60`
	s := NewService(map[string]Login{"c": scriptLogin{script: script, takesCode: true}})
	if err := s.Start("c"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, s, "c", "waiting", func(f FlowState) bool { return f.State == "waiting" })
	s.Cancel("c")
	waitFor(t, s, "c", "cancelled", func(f FlowState) bool { return f.State == "cancelled" })
	s.Cancel("c")
}

func TestFlowFailureCarriesTheCLIsWords(t *testing.T) {
	script := `echo "url: https://example.test/auth"
echo "The provider said no." >&2
exit 3`
	s := NewService(map[string]Login{"c": scriptLogin{script: script}})
	if err := s.Start("c"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	failed := waitFor(t, s, "c", "failed", func(f FlowState) bool { return f.State == "failed" })
	if failed.Error != "The provider said no." {
		t.Fatalf("error = %q", failed.Error)
	}
}

// A second start while a flow runs attaches to it instead of racing a second
// login process.
func TestStartAttachesToTheRunningFlow(t *testing.T) {
	script := `echo "url: https://example.test/auth"
printf "paste> "
sleep 60`
	s := NewService(map[string]Login{"c": scriptLogin{script: script, takesCode: true}})
	if err := s.Start("c"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, s, "c", "waiting", func(f FlowState) bool { return f.State == "waiting" })
	first := s.flow("c")
	if err := s.Start("c"); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if s.flow("c") != first {
		t.Fatal("the second start must attach, not replace")
	}
	s.Cancel("c")
	waitFor(t, s, "c", "cancelled", func(f FlowState) bool { return f.State == "cancelled" })
}

func TestStartRefusesAnUnknownCoder(t *testing.T) {
	s := NewService(map[string]Login{})
	if err := s.Start("nope"); err == nil {
		t.Fatal("an unsupported coder must be refused")
	}
	if s.Supported("nope") {
		t.Fatal("Supported must say no")
	}
}

func TestAnswerValidation(t *testing.T) {
	script := `echo "url: https://example.test/auth"
printf "paste> "
sleep 60`
	s := NewService(map[string]Login{"c": scriptLogin{script: script, takesCode: true}})
	if err := s.Answer("c", "code"); err == nil {
		t.Fatal("an answer without a flow must be refused")
	}
	if err := s.Start("c"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, s, "c", "waiting", func(f FlowState) bool { return f.State == "waiting" })
	if err := s.Answer("c", "   "); err == nil {
		t.Fatal("an empty code must be refused")
	}
	if err := s.Answer("c", strings.Repeat("x", maxCode+1)); err == nil {
		t.Fatal("an overlong code must be refused")
	}
	s.Cancel("c")
	waitFor(t, s, "c", "cancelled", func(f FlowState) bool { return f.State == "cancelled" })
}

func TestStripEscapes(t *testing.T) {
	hyperlink := "visit: \x1b]8;;https://example.test/a\x07https://example.test/a\x1b]8;;\x07 now"
	if got := StripEscapes(hyperlink); got != "visit: https://example.test/a now" {
		t.Fatalf("StripEscapes = %q", got)
	}
	colored := "\x1b[31mred\x1b[0m line"
	if got := StripEscapes(colored); got != "red line" {
		t.Fatalf("StripEscapes = %q", got)
	}
}

func TestLastLine(t *testing.T) {
	if got := LastLine("first\nsecond\n\n"); got != "second" {
		t.Fatalf("LastLine = %q", got)
	}
	if got := LastLine("  \n\n"); got != "" {
		t.Fatalf("LastLine = %q, want empty", got)
	}
	long := strings.Repeat("a", maxLine+10)
	if got := LastLine(long); len([]rune(got)) != maxLine+1 {
		t.Fatalf("LastLine length = %d", len([]rune(got)))
	}
}
