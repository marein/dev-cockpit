package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/project"
)

func TestCostLinesNameThePeriodsAndTheTopProjects(t *testing.T) {
	got := costLines(map[string]any{
		"today": 1.5, "week": 4.0, "month": 12.25, "burn": 0.3,
		"projects": []any{
			map[string]any{"project": "shop", "usd": 8.0},
			map[string]any{"project": "", "usd": 4.25},
			map[string]any{"usd": 3.5, "assistants": []any{
				map[string]any{"name": "Ops", "usd": 3.0},
				map[string]any{"name": "", "usd": 0.5},
			}},
		},
		"unpriced": 2.0,
	})
	for _, want := range []string{
		"API list price", "subscription",
		"Today $1.50, this week $4.00, this month $12.25, last hour $0.30.",
		"  shop $8.00\n", "  (no project) $4.25\n",
		"  Assistants $3.50\n    Ops $3.00\n    (unnamed) $0.50\n",
		"2 sessions ran a model without a list price, their tokens are counted, no spend.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if empty := costLines(map[string]any{}); !strings.Contains(empty, "Nothing booked this month.") {
		t.Fatalf("empty answer = %q", empty)
	}
}

func TestThePlacerReadsAnAssistantsWorkspaceElseTheProject(t *testing.T) {
	stateDir := t.TempDir()
	_, workspace, err := assistant.New(stateDir, assistantCoders{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	root := t.TempDir()
	place := costPlacer(workspace, project.NewRepository(root, nil))
	cases := []struct{ cwd, assistant, project string }{
		{workspace.Dir("ops-0001"), "ops-0001", ""},
		{workspace.Dir("gone-0001"), "gone-0001", ""},
		{filepath.Join(root, "shop"), "", "shop"},
		{filepath.Join(assistant.Root(stateDir), "workspace"), "", ""},
	}
	for _, c := range cases {
		if a, p := place(c.cwd); a != c.assistant || p != c.project {
			t.Errorf("%s = %q %q, want %q %q", c.cwd, a, p, c.assistant, c.project)
		}
	}
}
