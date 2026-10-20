package terminal

import (
	"testing"

	"github.com/marein/dev-cockpit/internal/tmux"
)

type recordingTarget struct {
	raw []byte
	key string
}

func (r *recordingTarget) SendRaw(name string, data []byte) error { r.raw = data; return nil }
func (r *recordingTarget) SendKey(name, key string) error         { r.key = key; return nil }
func (r *recordingTarget) SendLiteral(name, text string) error    { return nil }
func (r *recordingTarget) PasteLiteral(name, text string) error   { return nil }

type foregroundTarget struct {
	recordingTarget
	fg  tmux.PaneForeground
	err error
}

func (f *foregroundTarget) PaneForeground(name string) (tmux.PaneForeground, error) {
	return f.fg, f.err
}

func sendShiftEnterControl(t *testing.T, target Target) {
	t.Helper()
	err := SendInput(target, DefaultControlMapper(), "s", Input{Control: "shift-enter"})
	if err != nil {
		t.Fatalf("SendInput: %v", err)
	}
}

func TestShiftEnterSendsKittyKeyToExtendedKeysPrograms(t *testing.T) {
	for _, command := range []string{"claude", "copilot"} {
		target := &foregroundTarget{fg: tmux.PaneForeground{Command: command, AltScreen: true}}
		sendShiftEnterControl(t, target)
		if string(target.raw) != "\x1b[13;2u" || target.key != "" {
			t.Fatalf("%s: raw %q, key %q, want kitty sequence", command, target.raw, target.key)
		}
	}
	launched := &foregroundTarget{fg: tmux.PaneForeground{Command: "ollama", AltScreen: true, Coder: "claude"}}
	sendShiftEnterControl(t, launched)
	if string(launched.raw) != "\x1b[13;2u" {
		t.Fatalf("ollama in a claude session: raw %q, key %q, want kitty sequence", launched.raw, launched.key)
	}
}

func TestShiftEnterFallsBackToEnter(t *testing.T) {
	cases := map[string]*foregroundTarget{
		"plain shell":       {fg: tmux.PaneForeground{Command: "bash"}},
		"vim on alt screen": {fg: tmux.PaneForeground{Command: "vim", AltScreen: true}},
		"claude -p run":     {fg: tmux.PaneForeground{Command: "claude"}},
		"ollama launch":     {fg: tmux.PaneForeground{Command: "ollama", AltScreen: true}},
		"foreground error":  {err: errTest},
	}
	for label, target := range cases {
		sendShiftEnterControl(t, target)
		if target.key != "Enter" || target.raw != nil {
			t.Fatalf("%s: raw %q, key %q, want Enter", label, target.raw, target.key)
		}
	}
}

func TestShiftEnterWithoutForegroundReporterSendsEnter(t *testing.T) {
	target := &recordingTarget{}
	sendShiftEnterControl(t, target)
	if target.key != "Enter" || target.raw != nil {
		t.Fatalf("raw %q, key %q, want Enter", target.raw, target.key)
	}
}

var errTest = errTestType{}

type errTestType struct{}

func (errTestType) Error() string { return "test error" }

func TestInputInterrupts(t *testing.T) {
	cases := []struct {
		label     string
		items     []Input
		interrupt bool
	}{
		{"escape control", []Input{{Control: "escape"}}, true},
		{"raw escape byte", []Input{{Raw: "\x1b"}}, true},
		{"raw ctrl-c byte", []Input{{Raw: "\x03"}}, true},
		{"raw escape sequence is not the key", []Input{{Raw: "\x1b[A"}}, false},
		{"raw bracketed paste is not the key", []Input{{Raw: "\x1b[200~hello\x1b[201~"}}, false},
		{"typed escape", []Input{{Text: "\x1b"}}, true},
		{"ctrl-c control", []Input{{Control: "ctrl-c"}}, true},
		{"typed ctrl-c", []Input{{Text: "\x03"}}, true},
		{"plain typing", []Input{{Text: "hello\r"}}, false},
		{"a prompt", []Input{{Prompt: "do the thing"}}, false},
		{"arrows are escape sequences, not Escape", []Input{{Text: "\x1b[A"}}, false},
	}
	for _, c := range cases {
		if got := InputInterrupts(c.items); got != c.interrupt {
			t.Fatalf("%s: got %v, want %v", c.label, got, c.interrupt)
		}
	}
}

// Enter or a digit answers an ask, in every shape the terminal sends it;
// Tab, the arrows, Escape and plain typing decide nothing.
func TestInputAnswers(t *testing.T) {
	for _, items := range [][]Input{
		{{Control: "Enter"}}, {{Raw: "\r"}}, {{Text: "2"}}, {{Raw: "1"}},
	} {
		if !InputAnswers(items) {
			t.Errorf("%+v answers an ask", items)
		}
	}
	for _, items := range [][]Input{
		{{Control: "Tab"}}, {{Raw: "\x1b[B"}}, {{Raw: "\x1b"}}, {{Text: "yes"}}, {{Text: "0"}}, {{Paste: "1"}},
	} {
		if InputAnswers(items) {
			t.Errorf("%+v answers nothing", items)
		}
	}
}

// Writing is text going into a field: Tab opens an ask's amend field, and
// typed text, a paste or a prompt is written. Keys and a single answer key
// write nothing. Enter alone submits.
func TestInputWritesAndSubmits(t *testing.T) {
	for _, items := range [][]Input{
		{{Control: "Tab"}}, {{Raw: "\t"}}, {{Raw: "a"}}, {{Text: "use 2 lines"}}, {{Text: "12"}}, {{Paste: "x"}}, {{Prompt: "go"}}, {{Control: "space"}},
	} {
		if !InputWrites(items) {
			t.Errorf("%+v writes", items)
		}
	}
	for _, items := range [][]Input{
		{{Raw: "3"}}, {{Raw: "\r"}}, {{Control: "Enter"}}, {{Raw: "\x1b[B"}}, {{Raw: "\x1b"}}, {{Raw: "\x7f"}}, {{Control: "escape"}},
	} {
		if InputWrites(items) {
			t.Errorf("%+v writes nothing", items)
		}
	}
	if !InputSubmits([]Input{{Text: "fix"}, {Raw: "\r"}}) || InputSubmits([]Input{{Raw: "3"}}) {
		t.Error("only Enter submits")
	}
}
