package assistant

import (
	"strings"
	"testing"
	"time"
)

// A name is taken trimmed and refused for the one reason each: too long, a
// character no CLI takes in a model name, whitespace inside, a first rune of
// "-" that an argv would read as an option. Empty is a value of its own, the
// choice cleared, never a refusal.
func TestCleanModelTakesANameAndRefusesTheRest(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
		ok   bool
	}{
		{"", "", true},
		{"   ", "", true},
		{" haiku ", "haiku", true},
		{"claude-haiku-4-5", "claude-haiku-4-5", true},
		{"github-copilot/gpt-5.4-mini", "github-copilot/gpt-5.4-mini", true},
		{"claude-opus-4-1[1m]", "claude-opus-4-1[1m]", true},
		{"llama3.1:8b_q4", "llama3.1:8b_q4", true},
		{strings.Repeat("a", MaxModelRunes), strings.Repeat("a", MaxModelRunes), true},
		{strings.Repeat("a", MaxModelRunes+1), "", false},
		{"two words", "", false},
		{"a\tb", "", false},
		{"model;rm", "", false},
		{"$(model)", "", false},
		{"model\n", "model", true},
		{"-foo", "", false},
		{"--model", "", false},
		{" -m", "", false},
		{"a-b", "a-b", true},
	} {
		got, err := CleanModel(tc.raw)
		if tc.ok && (err != nil || got != tc.want) {
			t.Fatalf("CleanModel(%q) = %q, %v; want %q", tc.raw, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Fatalf("CleanModel(%q) = %q; want a refusal", tc.raw, got)
		}
	}
	if _, err := CleanModel("two words"); err == nil || !strings.Contains(err.Error(), "no spaces") {
		t.Fatalf("want the refusal to say what a name may hold, got %v", err)
	}
	if _, err := CleanModel(strings.Repeat("a", MaxModelRunes+1)); err == nil || !strings.Contains(err.Error(), "80") {
		t.Fatalf("want the refusal to name the bound, got %v", err)
	}
	if _, err := CleanModel("-foo"); err == nil || !strings.Contains(err.Error(), "start with a dash") {
		t.Fatalf("want the refusal to say why a leading dash is out, got %v", err)
	}
}

// One reading for every purpose, the assistant inheriting from the coder or
// overriding it and a check and a reaction inheriting from the chat: a chat
// turn takes the ring's chat pick, else the coder's start default, else
// empty, the CLI's own default; a check takes the ring's check pick, else
// that whole chat chain, ring pick included; a reaction the trigger's own
// model, else the ring's Triggers pick, else the chat chain the same way.
// Same as chat therefore means the assistant's resolved chat and nothing
// else. The three defaults of the Models tab are creation defaults and
// never change what a running turn gets: with all of them set and nothing
// picked a chat runs on the start default, and a check and a reaction still
// run on the chat. A pick of another purpose never crosses
// over, the check pick never reaches a reaction, the trigger pick never
// reaches a check and a trigger's model never reaches a check.
func TestModelForResolvesEveryPurpose(t *testing.T) {
	none := ModelDefaults{}
	all := ModelDefaults{Chat: "d-chat", Check: "d-check", Trigger: "d-trigger", Start: "d-start"}
	chatOnly := ModelDefaults{Chat: "d-chat"}
	startOnly := ModelDefaults{Start: "d-start"}
	for _, tc := range []struct {
		name     string
		kind     RunKind
		owner    Summary
		trigger  string
		defaults ModelDefaults
		want     string
	}{
		{"chat on the chat pick", RunChat, Summary{Model: "opus", CheckModel: "haiku"}, "", all, "opus"},
		{"chat without a pick takes the start default, never the tab's chat default", RunChat, Summary{CheckModel: "haiku"}, "", all, "d-start"},
		{"chat without a pick takes the start default", RunChat, Summary{}, "", startOnly, "d-start"},
		{"chat without any choice", RunChat, Summary{}, "", none, ""},
		{"check on the check pick", RunCheck, Summary{Model: "opus", CheckModel: "haiku"}, "", all, "haiku"},
		{"check without a pick takes the chat pick, never the check default", RunCheck, Summary{Model: "opus"}, "", all, "opus"},
		{"check without a pick takes the chat pick, the tab's chat default left aside", RunCheck, Summary{Model: "opus"}, "", chatOnly, "opus"},
		{"check with the chat picked and nothing else set takes the chat pick", RunCheck, Summary{Model: "fable"}, "", none, "fable"},
		{"check without a chat pick takes the start default, never the check default or the tab's chat default", RunCheck, Summary{}, "", all, "d-start"},
		{"check without a chat pick takes the start default", RunCheck, Summary{}, "", startOnly, "d-start"},
		{"check without any choice", RunCheck, Summary{}, "", none, ""},
		{"reaction on the trigger's pick", RunReaction, Summary{Model: "opus", CheckModel: "haiku"}, "sonnet", all, "sonnet"},
		{"reaction on the trigger's pick, above the ring's trigger pick", RunReaction, Summary{Model: "opus", TriggerModel: "haiku"}, "sonnet", all, "sonnet"},
		{"reaction without a pick takes the ring's trigger pick, above the chat pick", RunReaction, Summary{Model: "opus", TriggerModel: "haiku"}, "", all, "haiku"},
		{"reaction without a pick takes the ring's trigger pick with nothing else set", RunReaction, Summary{TriggerModel: "haiku"}, "", none, "haiku"},
		{"reaction without a pick takes the chat pick, never the trigger default", RunReaction, Summary{Model: "opus"}, "", all, "opus"},
		{"reaction without a pick takes the chat pick, the tab's chat default left aside", RunReaction, Summary{Model: "opus", CheckModel: "haiku"}, "", chatOnly, "opus"},
		{"reaction with the chat picked and nothing else set takes the chat pick", RunReaction, Summary{Model: "fable"}, "", none, "fable"},
		{"reaction without a chat pick takes the start default, never the trigger default or the tab's chat default", RunReaction, Summary{CheckModel: "haiku"}, "", all, "d-start"},
		{"reaction without a chat pick takes the start default", RunReaction, Summary{}, "", startOnly, "d-start"},
		{"reaction without any choice", RunReaction, Summary{}, "", none, ""},
		{"a trigger's model never reaches a chat turn", RunChat, Summary{}, "sonnet", none, ""},
		{"a trigger's model never reaches a check", RunCheck, Summary{}, "sonnet", none, ""},
		{"the check pick never reaches a chat turn", RunChat, Summary{CheckModel: "haiku"}, "", none, ""},
		{"the check pick never reaches a reaction", RunReaction, Summary{CheckModel: "haiku"}, "", none, ""},
		{"the trigger pick never reaches a chat turn", RunChat, Summary{TriggerModel: "haiku"}, "", none, ""},
		{"the trigger pick never reaches a check", RunCheck, Summary{TriggerModel: "haiku"}, "", none, ""},
		{"the check and trigger defaults never reach a chat turn", RunChat, Summary{}, "", ModelDefaults{Check: "d-check", Trigger: "d-trigger"}, ""},
		{"the tab's chat default never reaches a chat turn", RunChat, Summary{}, "", chatOnly, ""},
		{"the tab's chat default never reaches a check", RunCheck, Summary{}, "", chatOnly, ""},
		{"the tab's chat default never reaches a reaction", RunReaction, Summary{}, "", chatOnly, ""},
		{"the check default never reaches a check at run time", RunCheck, Summary{}, "", ModelDefaults{Check: "d-check"}, ""},
		{"the trigger default never reaches a reaction at run time", RunReaction, Summary{}, "", ModelDefaults{Trigger: "d-trigger"}, ""},
		{"the check default never reaches a reaction", RunReaction, Summary{}, "", ModelDefaults{Check: "d-check"}, ""},
		{"the trigger default never reaches a check", RunCheck, Summary{}, "", ModelDefaults{Trigger: "d-trigger"}, ""},
	} {
		if got := ModelFor(tc.kind, tc.owner, tc.trigger, tc.defaults); got != tc.want {
			t.Fatalf("%s: ModelFor(%s, %+v, %q, %+v) = %q, want %q", tc.name, tc.kind, tc.owner, tc.trigger, tc.defaults, got, tc.want)
		}
	}
}

// A trigger carries the model its reactions run on the way it carries its
// task: written where the spec names it, cleaned by the one reading every
// surface takes, cleared by an empty value, left alone by a spec that does not
// name it, and named in the line a change answers with.
func TestATriggerCarriesItsOwnModel(t *testing.T) {
	now := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	owner := "11111111-1111-4111-8111-111111111111"
	trigger, err := newTrigger(TriggerSpec{Owner: owner, Event: "job-done", Task: "carry on", Model: " haiku ", ModelSet: true}, now)
	if err != nil {
		t.Fatalf("new trigger: %v", err)
	}
	if trigger.Model != "haiku" {
		t.Fatalf("want the model stored trimmed, got %q", trigger.Model)
	}
	if _, err := newTrigger(TriggerSpec{Owner: owner, Event: "job-done", Task: "carry on", Model: "two words", ModelSet: true}, now); err == nil {
		t.Fatal("want a model no CLI takes refused")
	}

	same, err := editTrigger(trigger, TriggerSpec{Task: "carry on then"}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if same.Model != "haiku" {
		t.Fatalf("a spec that names no model has to leave it, got %q", same.Model)
	}
	moved, err := editTrigger(trigger, TriggerSpec{Model: "sonnet", ModelSet: true}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if moved.Model != "sonnet" || triggerChanges(trigger, moved) != "model sonnet" {
		t.Fatalf("want the change named, got %q and %q", moved.Model, triggerChanges(trigger, moved))
	}
	cleared, err := editTrigger(trigger, TriggerSpec{Model: "", ModelSet: true}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if cleared.Model != "" || triggerChanges(trigger, cleared) != "the assistant default" {
		t.Fatalf("want the model cleared back to the assistant default, got %q and %q", cleared.Model, triggerChanges(trigger, cleared))
	}
}
