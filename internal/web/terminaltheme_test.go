package web

import (
	"testing"

	"github.com/marein/dev-cockpit/internal/tmux"
)

func TestTheSchemeReportFollowsTheCoderOfASessionAndTheProgramOfAShell(t *testing.T) {
	for _, id := range []string{"claude", "opencode"} {
		if !schemeReportCoder(id) {
			t.Fatalf("want every session of %s reported to, whatever launched it", id)
		}
	}
	if schemeReportCoder("copilot") || schemeReportCoder("ollama") {
		t.Fatal("want no report for copilot and none for the launcher's own name")
	}
	if !shellSchemeReport(tmux.PaneForeground{Command: "claude", AltScreen: true}) {
		t.Fatal("want claude run by hand in a shell reported to")
	}
	if !shellSchemeReport(tmux.PaneForeground{Command: "ollama", AltScreen: true, Coder: "claude"}) {
		t.Fatal("want ollama in a claude session counted as claude")
	}
	for label, fg := range map[string]tmux.PaneForeground{
		"ollama launch in a shell": {Command: "ollama", AltScreen: true},
		"claude -p in a shell":     {Command: "claude"},
		"vim in a shell":           {Command: "vim", AltScreen: true},
	} {
		if shellSchemeReport(fg) {
			t.Fatalf("%s: want no report", label)
		}
	}
}
