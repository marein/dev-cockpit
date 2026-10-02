package cli

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/cost"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web"
)

// cost-show reads the answer the /costs handler builds, so a key the handler
// stops sending fails here.
func TestCostShowNamesThePeriodsAndTheTopProjects(t *testing.T) {
	report := cost.Report{
		Today: 1.5, Week: 4, Month: 12.25,
		Projects: []cost.Share{
			{Project: "shop", USD: 8},
			{Project: "", USD: 4.25},
			{Assistants: true, USD: 3.5},
		},
		Assistants: []cost.Share{{Name: "Ops", USD: 3}, {USD: 0.5}},
		Unpriced:   2,
	}
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(web.CostsAnswer(report))
	})
	var out strings.Builder
	if err := runCostShow(&out, inspectOptions{stateDir: dir}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Spend at API list price.\n",
		"Today $1.50, this week $4.00, this month $12.25.",
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
