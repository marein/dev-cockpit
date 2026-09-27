package web

import (
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
)

// A spent trigger outlives the terminal it waited for, so its row names that
// terminal by the name it had and says it was deleted, and leads nowhere.
func TestASpentTriggerNamesItsDeletedTerminal(t *testing.T) {
	view := (&Server{}).assistantTriggerView(assistant.Trigger{
		ID:      "6a2f1d9c4b8e0175",
		Source:  assistant.EventJob,
		Kind:    "job-done",
		Once:    true,
		State:   assistant.TriggerDone,
		Fired:   1,
		Targets: []assistant.TriggerTarget{{Terminal: "term-1", Name: "Refactor auth", Gone: true}},
	}, false)
	if view.Where != "Refactor auth (deleted)" {
		t.Fatalf("want the stored name marked deleted, got %q", view.Where)
	}
	if view.TargetURL != "" {
		t.Fatalf("a deleted terminal is no place to open, got %q", view.TargetURL)
	}
}
