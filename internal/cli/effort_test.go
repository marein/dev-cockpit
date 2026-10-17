package cli

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
)

// A coder's levels stand under its models, a model that names its own
// levels carries them in brackets, and the default efforts stand with the
// default models.
func TestFormatModelsNamesTheLevels(t *testing.T) {
	answer := map[string]any{"coders": []any{map[string]any{
		"id": "opencode",
		"models": []any{
			map[string]any{"name": "opencode/beam-free", "source": "cli", "efforts": []any{"low", "high", "max"}},
			map[string]any{"name": "opencode/big-pickle", "source": "cli"},
		},
		"efforts":  []any{"none", "low", "high", "max"},
		"defaults": map[string]any{"start": "opencode/beam-free", "startEffort": "high", "chatEffort": "low"},
	}}}
	want := "Models of opencode (2)\n" +
		"  cli    opencode/beam-free (low, high, max)\n" +
		"  cli    opencode/big-pickle\n" +
		"  efforts: none, low, high, max\n" +
		"  defaults: chat effort low, start opencode/beam-free, start effort high\n"
	if got := formatModels(answer); got != want {
		t.Fatalf("formatModels = %q, want %q", got, want)
	}
}

func TestFormatAssistantModelsNamesTheEffortBesideTheModel(t *testing.T) {
	answer := map[string]any{
		"chat":    map[string]any{"model": "opus", "source": "pick", "effort": map[string]any{"effort": "high", "source": "pick"}},
		"check":   map[string]any{"model": "haiku", "source": "pick", "effort": map[string]any{"effort": "high", "source": "pick", "sameAsChat": true}},
		"trigger": map[string]any{"model": "opus", "source": "pick", "sameAsChat": true, "effort": map[string]any{"effort": "", "source": "cli"}},
	}
	want := "Chat: opus (ring), effort high (ring)\n" +
		"Checks: haiku (ring), effort high (same as chat, ring)\n" +
		"Triggers: opus (same as chat, ring), effort Default (CLI)\n"
	if got := formatAssistantModels(answer); got != want {
		t.Fatalf("formatAssistantModels = %q, want %q", got, want)
	}
}

func TestModelsSetPostsTheNamedEfforts(t *testing.T) {
	named := func(flags ...string) func(string) bool {
		return func(name string) bool { return strings.Contains(","+strings.Join(flags, ",")+",", ","+name+",") }
	}
	form, err := modelsSetForm(modelsSetArgs{chatEffort: " high ", triggersEffort: "default"}, named("chat-effort", "triggers-effort"))
	if err != nil {
		t.Fatalf("modelsSetForm: %v", err)
	}
	if form.Get("effort") != "high" || !form.Has("trigger_effort") || form.Get("trigger_effort") != "" || form.Has("check_effort") || form.Has("model") {
		t.Fatalf("want the chat effort set, the triggers effort cleared and nothing else, got %v", form)
	}
}

// The started and the added line name a level a flag asked for where it made
// a difference, the way they name a model.
func TestTheStartedAndTheAddedLineNameAnEffortThatMadeADifference(t *testing.T) {
	created := map[string]any{"model": "opus", "modelDefault": "opus", "effort": "max", "effortDefault": "high"}
	if got := startedLine("cid-1", "demo", assistant.Pick{Model: "opus", Effort: "max"}, "", created); got != "coder cid-1 started in demo at max effort\n" {
		t.Fatalf("startedLine = %q", got)
	}
	created["effortDefault"] = "max"
	if got := startedLine("cid-1", "demo", assistant.Pick{Effort: "max"}, "task", created); got != "coder cid-1 started in demo, working on the task\n" {
		t.Fatalf("startedLine = %q", got)
	}
	answer := map[string]any{"id": "t1", "summary": "Job done", "model": "haiku", "modelDefault": "opus", "effort": "low", "effortDefault": ""}
	if got := addedLine(assistant.Pick{Model: "haiku", Effort: "low"}, answer); got != "added t1: Job done on haiku at low effort\n" {
		t.Fatalf("addedLine = %q", got)
	}
}

// coder-new posts the level in the dialog's field, trigger-edit posts it only
// where it was named, and `default` clears it.
func TestTheEffortFlagsPostTheFormsFields(t *testing.T) {
	var posted []url.Values
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		posted = append(posted, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "cid-1", "changed": "effort max"})
	})
	var out strings.Builder
	if err := runNewCoder(&out, inspectOptions{stateDir: dir, projectsDir: "/projects"}, "demo", "", "copilot", "", assistant.Pick{Effort: " max "}, "", "", ""); err != nil {
		t.Fatalf("coder-new: %v", err)
	}
	if len(posted) != 1 || posted[0].Get("effort") != "max" || posted[0].Has("model") {
		t.Fatalf("want the level in the effort field and no model, got %v", posted)
	}
	named := func(name string) bool { return name == "effort" }
	if err := runTriggerEdit(&out, inspectOptions{stateDir: dir}, "t1", triggerArgs{effort: "default"}, named); err != nil {
		t.Fatalf("trigger-edit: %v", err)
	}
	if len(posted) != 2 || !posted[1].Has("effort") || posted[1].Get("effort") != "" || posted[1].Has("model") {
		t.Fatalf("want an emptied effort field and nothing else, got %v", posted[1])
	}
}

func TestTheTriggerRowAndTheThreadHeaderNameTheEfforts(t *testing.T) {
	var row strings.Builder
	printTrigger(&row, map[string]any{"id": "t1", "label": "Job done", "state": "standing", "open": true, "model": "haiku", "effort": "low"}, triggerReport{})
	if !strings.Contains(row.String(), "  model     haiku\n  effort    low\n") {
		t.Fatalf("want the effort under the model, got %q", row.String())
	}
	header := formatAssistantThread("", map[string]any{"title": "Ops", "coderId": "claude", "model": "opus", "effort": "high", "checkEffort": "low", "messageCount": float64(0)})
	if !strings.HasPrefix(header, "Assistant \"Ops\" on coder claude, chat model opus, chat effort high, check effort low, 0 messages.") {
		t.Fatalf("want the efforts in the header, got %q", header)
	}
}
