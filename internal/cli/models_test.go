package cli

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/localapi"
)

// The listing is one block per coder: the head with the count, one row per
// name with its source in front, the defaults where any is set, and the note
// as the last line; a coder that lists nothing says so, and no coder at all
// is its own line.
func TestFormatModelsPrintsEveryRowWithItsSource(t *testing.T) {
	answer := map[string]any{"coders": []any{
		map[string]any{
			"id":   "claude",
			"note": "Aliases point at the newest model.",
			"models": []any{
				map[string]any{"name": "opus", "source": "cli"},
				map[string]any{"name": "claude-haiku-4-5", "source": "added"},
			},
			"defaults": map[string]any{"chat": "haiku", "check": "", "trigger": "", "start": "opus"},
		},
		map[string]any{"id": "copilot", "note": "", "models": []any{}, "defaults": map[string]any{}},
	}}
	want := "Models of claude (2)\n" +
		"  cli    opus\n" +
		"  added  claude-haiku-4-5\n" +
		"  defaults: chat haiku, start opus\n" +
		"  Aliases point at the newest model.\n" +
		"\n" +
		"Models of copilot (0)\n" +
		"  none, the CLI names none and none was added\n"
	if got := formatModels(answer); got != want {
		t.Fatalf("formatModels = %q, want %q", got, want)
	}
	if got := formatModels(map[string]any{"coders": []any{}}); got != "No coder is installed.\n" {
		t.Fatalf("formatModels without coders = %q", got)
	}
}

// The calling assistant's own reading is three lines and nothing else: the
// model with where it comes from, the ring, the coder's own default or the
// CLI's default, never the Models tab, Default where it names no model, and on the
// checks and the triggers the words same as chat in front of where the chat
// resolved, so a check that runs on the ring's chat pick never reads as if it
// had been picked at the ring itself; a Triggers pick of its own reads as
// the ring alone, the way a Checks pick does.
func TestFormatAssistantModelsIsThreeLinesWithTheSource(t *testing.T) {
	answer := map[string]any{
		"chat":    map[string]any{"model": "opus", "source": "pick"},
		"check":   map[string]any{"model": "haiku", "source": "pick"},
		"trigger": map[string]any{"model": "opus", "source": "pick", "sameAsChat": true},
	}
	want := "Chat: opus (ring)\nChecks: haiku (ring)\nTriggers: opus (same as chat, ring)\n"
	if got := formatAssistantModels(answer); got != want {
		t.Fatalf("formatAssistantModels = %q, want %q", got, want)
	}
	answer["trigger"] = map[string]any{"model": "sonnet", "source": "pick"}
	want = "Chat: opus (ring)\nChecks: haiku (ring)\nTriggers: sonnet (ring)\n"
	if got := formatAssistantModels(answer); got != want {
		t.Fatalf("formatAssistantModels = %q, want %q", got, want)
	}
	answer["chat"] = map[string]any{"model": "fable", "source": "coder"}
	answer["check"] = map[string]any{"model": "fable", "source": "coder", "sameAsChat": true}
	answer["trigger"] = map[string]any{"model": "fable", "source": "coder", "sameAsChat": true}
	want = "Chat: fable (coder default)\nChecks: fable (same as chat, coder default)\nTriggers: fable (same as chat, coder default)\n"
	if got := formatAssistantModels(answer); got != want {
		t.Fatalf("formatAssistantModels = %q, want %q", got, want)
	}
	answer["chat"] = map[string]any{"model": "", "source": "cli"}
	answer["check"] = map[string]any{"model": "", "source": "cli", "sameAsChat": true}
	answer["trigger"] = map[string]any{"model": "", "source": "cli", "sameAsChat": true}
	want = "Chat: Default (CLI)\nChecks: Default (same as chat, CLI)\nTriggers: Default (same as chat, CLI)\n"
	if got := formatAssistantModels(answer); got != want {
		t.Fatalf("formatAssistantModels = %q, want %q", got, want)
	}
}

// assistant-models-get reads the calling assistant's own resolutions off the
// resolved route, with --as on the header, and prints the three lines.
func TestAssistantModelsGetReadsTheCallersOwnResolutions(t *testing.T) {
	var seen []string
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" as "+r.Header.Get(localapi.AssistantHeader))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"chat":    map[string]any{"model": "opus", "source": "pick"},
			"check":   map[string]any{"model": "opus", "source": "pick", "sameAsChat": true},
			"trigger": map[string]any{"model": "haiku", "source": "pick"},
		})
	})
	var out strings.Builder
	if err := runAssistantModelsGet(&out, inspectOptions{stateDir: dir, assistantID: "abc"}); err != nil {
		t.Fatalf("assistant-models-get: %v", err)
	}
	if len(seen) != 1 || seen[0] != "GET /assistants/models/resolved as abc" {
		t.Fatalf("want one read of the resolved route as the caller, got %v", seen)
	}
	if out.String() != "Chat: opus (ring)\nChecks: opus (same as chat, ring)\nTriggers: haiku (ring)\n" {
		t.Fatalf("want the three lines, got %q", out.String())
	}
}

// What assistant-models-set posts is one field per named flag and nothing
// for the rest, the way trigger-edit posts what was typed: a flag left out
// leaves that pick standing, `default` posts the empty field that clears it,
// and a call naming nothing is refused before anything is sent.
func TestModelsSetPostsOnlyWhatWasNamed(t *testing.T) {
	named := func(flags ...string) func(string) bool {
		return func(name string) bool {
			for _, f := range flags {
				if f == name {
					return true
				}
			}
			return false
		}
	}
	if _, err := modelsSetForm(modelsSetArgs{}, named()); err == nil || !strings.Contains(err.Error(), "--chat, --checks or --triggers") {
		t.Fatalf("want a call naming nothing refused with the flags named, got %v", err)
	}
	form, err := modelsSetForm(modelsSetArgs{chat: "default", checks: " haiku "}, named("chat", "checks"))
	if err != nil {
		t.Fatalf("modelsSetForm: %v", err)
	}
	if form.Get("form") != "model" || !form.Has("model") || form.Get("model") != "" || form.Get("check_model") != "haiku" || form.Has("trigger_model") {
		t.Fatalf("want form=model with the chat cleared, the checks named and the triggers untouched, got %v", form)
	}
	form, err = modelsSetForm(modelsSetArgs{triggers: "Default"}, named("triggers"))
	if err != nil {
		t.Fatalf("modelsSetForm: %v", err)
	}
	if !form.Has("trigger_model") || form.Get("trigger_model") != "" || form.Has("model") || form.Has("check_model") {
		t.Fatalf("want only the triggers cleared, got %v", form)
	}
}

// assistant-models-set goes to the calling assistant's own path, the one the
// ring's menu posts to, as that assistant, and prints the sentence the ring's
// save shows; a refusal is the cockpit's sentence, and without --as nothing
// is sent at all, there is no path to build.
func TestAssistantModelsSetPostsToTheCallersOwnPath(t *testing.T) {
	var seen []string
	var posted url.Values
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		posted = r.PostForm
		seen = append(seen, r.Method+" "+r.URL.Path+" as "+r.Header.Get(localapi.AssistantHeader))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"saved": true, "model": "", "checkModel": "haiku", "triggerModel": "", "message": "Chat and triggers on the CLI's default, checks on haiku."})
	})
	checks := func(name string) bool { return name == "checks" }
	var out strings.Builder
	if err := runAssistantModelsSet(&out, inspectOptions{stateDir: dir, assistantID: "abc"}, modelsSetArgs{checks: "haiku"}, checks); err != nil {
		t.Fatalf("assistant-models-set: %v", err)
	}
	if len(seen) != 1 || seen[0] != "POST /assistants/abc as abc" {
		t.Fatalf("want one post to the caller's own path as the caller, got %v", seen)
	}
	if posted.Get("form") != "model" || posted.Get("check_model") != "haiku" || posted.Has("model") || posted.Has("trigger_model") {
		t.Fatalf("want form=model with the checks alone, got %v", posted)
	}
	if out.String() != "Chat and triggers on the CLI's default, checks on haiku.\n" {
		t.Fatalf("want the ring's sentence, got %q", out.String())
	}

	seen = nil
	err := runAssistantModelsSet(&out, inspectOptions{stateDir: dir}, modelsSetArgs{checks: "haiku"}, checks)
	if err == nil || !strings.Contains(err.Error(), "--as") {
		t.Fatalf("want a call without --as refused naming the flag, got %v", err)
	}
	if len(seen) != 0 {
		t.Fatalf("want nothing sent without --as, got %v", seen)
	}

	refused := refusing(t, "A model name holds letters, digits and . _ / : - [ ] only, no spaces.")
	var quiet strings.Builder
	err = runAssistantModelsSet(&quiet, inspectOptions{stateDir: refused, assistantID: "abc"}, modelsSetArgs{checks: "two words"}, checks)
	if err == nil || !strings.Contains(err.Error(), "no spaces") {
		t.Fatalf("want the cockpit's refusal as the error, got %v", err)
	}
	if quiet.String() != "" {
		t.Fatalf("a refused call must not report success, got %q", quiet.String())
	}
}
