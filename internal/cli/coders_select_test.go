package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
	codercodex "github.com/marein/dev-cockpit/internal/coder/codex"
	codercopilot "github.com/marein/dev-cockpit/internal/coder/copilot"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// A coder is served when its CLI is installed and left out when it is not,
// so no picker offers one that could not start.
func TestACoderWithoutItsCLIIsNotServed(t *testing.T) {
	bin := t.TempDir()
	install := func(tool string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range tmux.RequiredTools {
		install(tool)
	}
	install("copilot")
	t.Setenv("PATH", bin)
	t.Setenv("HOME", t.TempDir())
	registry := coder.NewRegistry(codercopilot.New(nil), codercodex.New(nil))
	ids := func() []string {
		t.Helper()
		selected, err := selectProviders(registry)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range selected {
			out = append(out, c.ID())
		}
		return out
	}

	if got := ids(); len(got) != 1 || got[0] != "copilot" {
		t.Fatalf("served = %v, want copilot alone while codex is not installed", got)
	}
	install("codex")
	if got := ids(); len(got) != 2 || got[1] != "codex" {
		t.Fatalf("served = %v, want codex beside copilot once it is installed", got)
	}
}

// coder-new's help names the coders --agent works for, read from what the new
// coder form reads, so a turn does not try an agent on a coder that refuses it.
func TestCoderNewHelpNamesTheCodersThatTakeAnAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	usage := newCoderCommand(&inspectOptions{}).Flags().Lookup("agent").Usage
	if usage != "agent the session starts with, for claude, copilot and opencode" {
		t.Fatalf("the --agent line reads %q", usage)
	}
}
