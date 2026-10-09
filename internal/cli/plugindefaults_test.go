package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/plugin"
)

type serveFunc func(s plugin.Serve) error

func (f serveFunc) ConfigureServe(s plugin.Serve) error { return f(s) }

type fixedCoders []assistant.CoderInfo

func (c fixedCoders) Available() []assistant.CoderInfo { return c }

func TestNewAssistantWritesPluginMemoryAfterTheMove(t *testing.T) {
	for name, seed := range map[string]func(t *testing.T, root string){
		"with transcripts": func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "conversations"), 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"workspace only": func(t *testing.T, root string) {},
	} {
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			root := assistant.Root(stateDir)
			seed(t, root)
			mine := filepath.Join(root, "workspace", "memory", "mine.md")
			if err := os.MkdirAll(filepath.Dir(mine), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(mine, []byte("---\ntitle: Mine\n---\nkept"), 0o600); err != nil {
				t.Fatal(err)
			}
			serves, err := pluginhost.ConfigureServe([]plugin.Named[plugin.ServePlugin]{{ID: "example", Plugin: serveFunc(func(s plugin.Serve) error {
				s.AddAssistantMemory(fstest.MapFS{"worktrees.md": {Data: []byte("---\ntitle: Worktrees\n---\nplugin")}}, false)
				return nil
			})}}, "", stateDir, nil, nil)
			if err != nil {
				t.Fatal(err)
			}

			if _, _, err := newAssistant(stateDir, assistantCoders{}, assistant.Cockpit{}, serves); err != nil {
				t.Fatal(err)
			}

			_, _, memory := assistant.Paths(stateDir)
			for file, want := range map[string]string{"mine.md": "kept", "worktrees.md": "plugin"} {
				data, err := os.ReadFile(filepath.Join(memory, file))
				if err != nil {
					t.Fatalf("%s is not in the memory: %v", file, err)
				}
				if !strings.HasSuffix(string(data), want) {
					t.Fatalf("%s = %q", file, data)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "workspace")); !os.IsNotExist(err) {
				t.Fatalf("the old workspace is still there: %v", err)
			}
		})
	}
}

func TestNewAssistantWritesOnlyWhatTheMemoryReads(t *testing.T) {
	stateDir := t.TempDir()
	serves, err := pluginhost.ConfigureServe([]plugin.Named[plugin.ServePlugin]{{ID: "example", Plugin: serveFunc(func(s plugin.Serve) error {
		s.AddAssistantMemory(fstest.MapFS{
			"worktrees.md":  {Data: []byte("---\ntitle: Worktrees\n---\nplugin")},
			"Notes.md":      {Data: []byte("upper case")},
			"my_notes.md":   {Data: []byte("underscore")},
			"nested/one.md": {Data: []byte("nested")},
			"notes.txt":     {Data: []byte("not markdown")},
			"big.md":        {Data: []byte("---\ntitle: Big\n---\n" + strings.Repeat("x", assistant.MaxMemoryBytes+1))},
		}, false)
		return nil
	})}}, "", stateDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, workspace, err := newAssistant(stateDir, assistantCoders{}, assistant.Cockpit{}, serves)
	if err != nil {
		t.Fatal(err)
	}

	_, _, memory := assistant.Paths(stateDir)
	entries, err := os.ReadDir(memory)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{"worktrees.md"}) {
		t.Fatalf("the memory directory holds %v, want only worktrees.md", names)
	}
	if listed := workspace.Memory(); len(listed) != 1 || listed[0].Slug != "worktrees" {
		t.Fatalf("the memory lists %v", listed)
	}
}

func TestNewAssistantPutsPluginMemoryIntoExistingInstructions(t *testing.T) {
	stateDir := t.TempDir()
	coders := fixedCoders{{ID: "claude"}}
	svc, _, err := assistant.New(stateDir, coders, assistant.Cockpit{})
	if err != nil {
		t.Fatal(err)
	}
	existing, err := svc.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	serves, err := pluginhost.ConfigureServe([]plugin.Named[plugin.ServePlugin]{{ID: "example", Plugin: serveFunc(func(s plugin.Serve) error {
		s.AddAssistantMemory(fstest.MapFS{"worktrees.md": {Data: []byte("---\ntitle: Worktrees\n---\nfork a worktree per branch")}}, false)
		return nil
	})}}, "", stateDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, workspace, err := newAssistant(stateDir, coders, assistant.Cockpit{}, serves)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(workspace.Dir(existing.ID), "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "fork a worktree per branch") {
		t.Fatalf("CLAUDE.md of %s does not carry the plugin memory:\n%s", existing.ID, data)
	}
}
