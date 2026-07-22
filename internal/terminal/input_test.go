package terminal

import (
	"testing"

	"github.com/local/dev-cockpit/internal/tmux"
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
}

func TestShiftEnterFallsBackToEnter(t *testing.T) {
	cases := map[string]*foregroundTarget{
		"plain shell":       {fg: tmux.PaneForeground{Command: "bash"}},
		"vim on alt screen": {fg: tmux.PaneForeground{Command: "vim", AltScreen: true}},
		"claude -p run":     {fg: tmux.PaneForeground{Command: "claude"}},
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
