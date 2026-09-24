package assistant

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/settings"
)

// The defaults are settings, one key per coder and purpose: a store answers
// them cleaned, a nil store answers none, a name the rule refuses is never
// stored, a stored value it refuses reads as none, an empty value takes the
// key out, and the keys of one coder never reach another.
func TestModelDefaultsLiveInTheSettingsStore(t *testing.T) {
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	if got := ModelDefaultsFor(nil, "claude"); got != (ModelDefaults{}) {
		t.Fatalf("want no defaults without a store, got %+v", got)
	}
	if _, err := StoreModelDefault(store, "claude", ModelPurposeChat, "two words"); err == nil || !strings.Contains(err.Error(), "no spaces") {
		t.Fatalf("want the shared rule's refusal, got %v", err)
	}
	for purpose, name := range map[ModelPurpose]string{
		ModelPurposeChat: " haiku ", ModelPurposeCheck: "sonnet", ModelPurposeTrigger: "opus", ModelPurposeStart: "fable",
	} {
		stored, err := StoreModelDefault(store, "claude", purpose, name)
		if err != nil || stored != strings.TrimSpace(name) {
			t.Fatalf("StoreModelDefault(%s, %q) = %q, %v", purpose, name, stored, err)
		}
	}
	want := ModelDefaults{Chat: "haiku", Check: "sonnet", Trigger: "opus", Start: "fable"}
	got := ModelDefaultsFor(store, "claude")
	if got != want {
		t.Fatalf("want %+v, got %+v", want, got)
	}
	for _, purpose := range ModelPurposes {
		if got.Of(purpose) != want.Of(purpose) || got.Of(purpose) == "" {
			t.Fatalf("Of(%s) = %q", purpose, got.Of(purpose))
		}
	}
	if other := ModelDefaultsFor(store, "copilot"); other != (ModelDefaults{}) {
		t.Fatalf("want another coder's defaults untouched, got %+v", other)
	}
	if _, err := StoreModelDefault(store, "claude", ModelPurposeCheck, ""); err != nil {
		t.Fatalf("an empty value is a value: %v", err)
	}
	if _, ok := store.Lookup(ModelDefaultKey("claude", ModelPurposeCheck)); ok {
		t.Fatal("want an empty value to take the key out of the store")
	}
	store.Set(ModelDefaultKey("claude", ModelPurposeChat), "-bad")
	if got := ModelDefaultsFor(store, "claude").Chat; got != "" {
		t.Fatalf("want a stored value the rule refuses to read as none, got %q", got)
	}
}

// The origin travels with the resolution and names the level the model
// really stands on, whatever purpose asked: a pick on the ring or a trigger,
// the coder's start default, or the CLI's own default where everything was
// empty; the Models tab is no level, its chat default stands in no chain and
// a chat without a pick answers the start default and then the CLI whatever
// that tab holds. A check without a check pick
// and a reaction of a trigger without a model of its own carry the chat's
// own reading marked as such, so a check that fell through to the ring's
// chat pick reports the ring and says it did so as the chat; a reaction that
// fell through to the ring's Triggers pick reports the ring as a pick and
// never as the chat, the pick stands above the chat like the trigger's own;
// the Checks and Trigger defaults stand in none of it. The chain below a
// purpose's own pick is what that pick's empty entry stands for.
func TestModelOriginSaysWhereTheAnswerCameFrom(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     RunKind
		owner    Summary
		trigger  string
		defaults ModelDefaults
		want     ModelReading
	}{
		{"chat pick", RunChat, Summary{Model: "opus"}, "", ModelDefaults{Chat: "haiku"}, ModelReading{Model: "opus", From: ModelFromPick}},
		{"chat on the coder's start default, the tab's chat default left aside", RunChat, Summary{}, "", ModelDefaults{Chat: "haiku", Start: "fable"}, ModelReading{Model: "fable", From: ModelFromCoder}},
		{"chat on the coder's start default", RunChat, Summary{}, "", ModelDefaults{Start: "fable"}, ModelReading{Model: "fable", From: ModelFromCoder}},
		{"chat on the CLI", RunChat, Summary{}, "", ModelDefaults{}, ModelReading{From: ModelFromCLI}},
		{"chat on the CLI, the tab's chat default left aside", RunChat, Summary{}, "", ModelDefaults{Chat: "haiku"}, ModelReading{From: ModelFromCLI}},
		{"check pick", RunCheck, Summary{Model: "opus", CheckModel: "sonnet"}, "", ModelDefaults{Check: "haiku"}, ModelReading{Model: "sonnet", From: ModelFromPick}},
		{"check as the chat on the ring's chat pick, the check default left aside", RunCheck, Summary{Model: "opus"}, "", ModelDefaults{Check: "haiku"}, ModelReading{Model: "opus", From: ModelFromPick, SameAsChat: true}},
		{"check as the chat with nothing else set", RunCheck, Summary{Model: "fable"}, "", ModelDefaults{}, ModelReading{Model: "fable", From: ModelFromPick, SameAsChat: true}},
		{"check as the chat on the CLI, the tab's chat and check defaults left aside", RunCheck, Summary{}, "", ModelDefaults{Chat: "haiku", Check: "sonnet"}, ModelReading{From: ModelFromCLI, SameAsChat: true}},
		{"check as the chat on the coder's start default", RunCheck, Summary{}, "", ModelDefaults{Start: "fable"}, ModelReading{Model: "fable", From: ModelFromCoder, SameAsChat: true}},
		{"check as the chat on the CLI", RunCheck, Summary{}, "", ModelDefaults{Check: "haiku"}, ModelReading{From: ModelFromCLI, SameAsChat: true}},
		{"reaction pick", RunReaction, Summary{Model: "opus"}, "sonnet", ModelDefaults{Trigger: "haiku"}, ModelReading{Model: "sonnet", From: ModelFromPick}},
		{"reaction pick above the ring's trigger pick", RunReaction, Summary{Model: "opus", TriggerModel: "fable"}, "sonnet", ModelDefaults{}, ModelReading{Model: "sonnet", From: ModelFromPick}},
		{"reaction on the ring's trigger pick, not as the chat", RunReaction, Summary{Model: "opus", TriggerModel: "fable"}, "", ModelDefaults{Chat: "haiku"}, ModelReading{Model: "fable", From: ModelFromPick}},
		{"reaction on the ring's trigger pick with nothing else set", RunReaction, Summary{TriggerModel: "fable"}, "", ModelDefaults{}, ModelReading{Model: "fable", From: ModelFromPick}},
		{"reaction as the chat on the ring's chat pick, the trigger default left aside", RunReaction, Summary{Model: "opus", CheckModel: "sonnet"}, "", ModelDefaults{Trigger: "haiku"}, ModelReading{Model: "opus", From: ModelFromPick, SameAsChat: true}},
		{"reaction as the chat with nothing else set", RunReaction, Summary{Model: "fable"}, "", ModelDefaults{}, ModelReading{Model: "fable", From: ModelFromPick, SameAsChat: true}},
		{"reaction as the chat on the CLI, the tab's chat and trigger defaults left aside", RunReaction, Summary{CheckModel: "sonnet"}, "", ModelDefaults{Chat: "haiku", Trigger: "opus"}, ModelReading{From: ModelFromCLI, SameAsChat: true}},
		{"reaction as the chat on the coder's start default", RunReaction, Summary{}, "", ModelDefaults{Start: "fable"}, ModelReading{Model: "fable", From: ModelFromCoder, SameAsChat: true}},
		{"reaction as the chat on the CLI", RunReaction, Summary{}, "", ModelDefaults{Trigger: "haiku"}, ModelReading{From: ModelFromCLI, SameAsChat: true}},
	} {
		got := ModelOrigin(tc.kind, tc.owner, tc.trigger, tc.defaults)
		if got != tc.want {
			t.Fatalf("%s: ModelOrigin = %+v, want %+v", tc.name, got, tc.want)
		}
		if ModelFor(tc.kind, tc.owner, tc.trigger, tc.defaults) != got.Model {
			t.Fatalf("%s: ModelFor disagrees with ModelOrigin", tc.name)
		}
		// The purpose's own pick is what the chain below it stands under:
		// where that pick is empty, DefaultModelOrigin answers the same
		// reading, whether the chain ended at the ring's chat pick or below it.
		own := tc.owner.Model
		switch tc.kind {
		case RunCheck:
			own = tc.owner.CheckModel
		case RunReaction:
			own = tc.trigger
		}
		if own == "" {
			if below := DefaultModelOrigin(tc.kind, tc.owner, tc.defaults); below != got || DefaultModelFor(tc.kind, tc.owner, tc.defaults) != got.Model {
				t.Fatalf("%s: DefaultModelOrigin = %+v, want the chain below the pick, %+v", tc.name, below, got)
			}
		}
	}
}
