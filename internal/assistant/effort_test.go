package assistant

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/settings"
)

// fakeEfforts are the levels the coder of the tests takes, and fakeCheckEffort
// its rule: a model named lean takes none of them, the way opencode's
// big-pickle takes no variant.
func fakeEfforts() []string { return []string{"low", "medium", "high", "max"} }

func fakeCheckEffort(model, level string) error {
	if model == "lean" {
		return errors.New("lean takes no effort level.")
	}
	_, err := CleanEffortIn(level, fakeEfforts())
	return err
}

func TestALevelIsOneLowercaseWord(t *testing.T) {
	for raw, want := range map[string]string{"": "", " high ": "high", "x1-y_z": "x1-y_z", "minimal": "minimal"} {
		if got, err := CleanEffort(raw); err != nil || got != want {
			t.Fatalf("CleanEffort(%q) = %q, %v, want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"High", "two words", "-high", "1st", "high;rm", strings.Repeat("a", MaxEffortRunes+1)} {
		if got, err := CleanEffort(raw); err == nil {
			t.Fatalf("CleanEffort(%q) = %q, want a refusal", raw, got)
		}
	}
}

// A level is checked against the coder's own, and the refusal names them, so
// whoever typed one reads what would have worked.
func TestALevelIsOneOfTheCodersOwn(t *testing.T) {
	if got, err := CleanEffortIn(" high ", fakeEfforts()); err != nil || got != "high" {
		t.Fatalf("want a level of the coder taken, got %q %v", got, err)
	}
	if got, err := CleanEffortIn("", nil); err != nil || got != "" {
		t.Fatalf("want an empty level to clear even for a coder without levels, got %q %v", got, err)
	}
	if _, err := CleanEffortIn("xhigh", fakeEfforts()); err == nil || err.Error() != "The effort is one of low, medium, high, max." {
		t.Fatalf("want the levels named, got %v", err)
	}
	if _, err := CleanEffortIn("high", nil); err == nil || !strings.Contains(err.Error(), "takes no effort level") {
		t.Fatalf("want a coder without levels to refuse every one, got %v", err)
	}
}

// The effort is read on the model's levels and together with the model: a
// pick, else the start default, else the CLI, and a check or a reaction
// without an effort of its own takes the chat's only where it runs on the
// chat's model too. One on a model of its own runs at its own level's effort
// or at the CLI's, never at a level picked for another model.
func TestTheEffortGoesWithItsModel(t *testing.T) {
	defaults := ModelDefaults{Start: "opus", StartEffort: "medium"}
	cases := []struct {
		name  string
		kind  RunKind
		owner Summary
		own   ModelPick
		want  EffortReading
	}{
		{"chat on the start pair", RunChat, Summary{}, ModelPick{}, EffortReading{Effort: "medium", From: ModelFromCoder}},
		{"chat picked", RunChat, Summary{Effort: "max"}, ModelPick{}, EffortReading{Effort: "max", From: ModelFromPick}},
		{"chat effort picked over the start model", RunChat, Summary{Effort: "low"}, ModelPick{}, EffortReading{Effort: "low", From: ModelFromPick}},
		{"chat on a model of its own leaves the start effort", RunChat, Summary{Model: "fable"}, ModelPick{}, EffortReading{From: ModelFromCLI}},
		{"check on the chat's model takes the chat's effort", RunCheck, Summary{Effort: "high"}, ModelPick{}, EffortReading{Effort: "high", From: ModelFromPick, SameAsChat: true}},
		{"check on its own model runs at the CLI's", RunCheck, Summary{Model: "fable", Effort: "high", CheckModel: "lean"}, ModelPick{}, EffortReading{From: ModelFromCLI}},
		{"check picked", RunCheck, Summary{CheckEffort: "high"}, ModelPick{}, EffortReading{Effort: "high", From: ModelFromPick}},
		{"reaction on the Triggers pair", RunReaction, Summary{TriggerModel: "haiku", TriggerEffort: "low"}, ModelPick{}, EffortReading{Effort: "low", From: ModelFromPick}},
		{"reaction on the Triggers model alone runs at the CLI's", RunReaction, Summary{Effort: "high", TriggerModel: "haiku"}, ModelPick{}, EffortReading{From: ModelFromCLI}},
		{"reaction on a trigger's own model runs at the CLI's", RunReaction, Summary{Model: "fable", Effort: "high", TriggerEffort: "low"}, ModelPick{Model: "lean"}, EffortReading{From: ModelFromCLI}},
		{"reaction on a trigger's own pair", RunReaction, Summary{Effort: "high"}, ModelPick{Model: "haiku", Effort: "max"}, EffortReading{Effort: "max", From: ModelFromPick}},
		{"reaction on the chat", RunReaction, Summary{Effort: "high"}, ModelPick{}, EffortReading{Effort: "high", From: ModelFromPick, SameAsChat: true}},
	}
	for _, c := range cases {
		if got := EffortOrigin(c.kind, c.owner, c.own, defaults); got != c.want {
			t.Fatalf("%s: want %+v, got %+v", c.name, c.want, got)
		}
	}
	if got := EffortOrigin(RunChat, Summary{}, ModelPick{}, ModelDefaults{Start: "opus"}); got != (EffortReading{From: ModelFromCLI}) {
		t.Fatalf("want a chat without an effort anywhere on the CLI's own, got %+v", got)
	}
	if got := DefaultEffortFor(RunCheck, Summary{Effort: "high", CheckEffort: "low", CheckModel: "lean"}, defaults); got != "high" {
		t.Fatalf("want a check's empty entry beside an empty model to stand for the chat's effort, got %q", got)
	}
	if got := ModelFor(RunCheck, Summary{Model: "fable", CheckModel: "haiku"}, "", defaults); got != "haiku" {
		t.Fatalf("the model chain moved: %q", got)
	}
}

// The ring refuses a pair it moves where its effort is a pick there, naming
// the purpose, and leaves alone what it does not move.
func TestCheckPicksRefusesAPairTheSaveMoves(t *testing.T) {
	was := Summary{Effort: "high"}
	if err := CheckPicks(was, Summary{Effort: "high", CheckModel: "lean"}, ModelDefaults{}, fakeCheckEffort); err != nil {
		t.Fatalf("a check on a model of its own takes no inherited effort and is no pair to refuse: %v", err)
	}
	if err := CheckPicks(was, Summary{Effort: "high", CheckModel: "lean", CheckEffort: "low"}, ModelDefaults{}, fakeCheckEffort); err == nil || err.Error() != "Checks: lean takes no effort level." {
		t.Fatalf("want the checks' own pair refused by name, got %v", err)
	}
	if err := CheckPicks(was, Summary{Model: "lean", Effort: "high"}, ModelDefaults{}, fakeCheckEffort); err == nil || err.Error() != "Chat: lean takes no effort level." {
		t.Fatalf("want a chat model that does not take the chat's effort refused, got %v", err)
	}
	if err := CheckPicks(Summary{Model: "lean"}, Summary{Model: "lean", CheckModel: "haiku"}, ModelDefaults{StartEffort: "high"}, fakeCheckEffort); err != nil {
		t.Fatalf("a pair the save does not move is not the save's to refuse: %v", err)
	}
}

func TestEffortDefaultsLiveBesideTheModelsInTheSettingsStore(t *testing.T) {
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	if _, err := StoreEffortDefault(store, "claude", ModelPurposeStart, "Not A Level"); err == nil {
		t.Fatal("want a level of the wrong shape refused")
	}
	for purpose, level := range map[ModelPurpose]string{ModelPurposeChat: "high", ModelPurposeCheck: "low", ModelPurposeTrigger: "medium", ModelPurposeStart: " max "} {
		if _, err := StoreEffortDefault(store, "claude", purpose, level); err != nil {
			t.Fatalf("store %s: %v", purpose, err)
		}
	}
	if _, err := StoreModelDefault(store, "claude", ModelPurposeStart, "opus"); err != nil {
		t.Fatal(err)
	}
	want := ModelDefaults{Start: "opus", ChatEffort: "high", CheckEffort: "low", TriggerEffort: "medium", StartEffort: "max"}
	if got := ModelDefaultsFor(store, "claude"); got != want {
		t.Fatalf("want %+v, got %+v", want, got)
	}
	if _, err := StoreEffortDefault(store, "claude", ModelPurposeChat, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Lookup(EffortDefaultKey("claude", ModelPurposeChat)); ok {
		t.Fatal("want an empty level to take the key out")
	}
	store.Set(EffortDefaultKey("claude", ModelPurposeStart), "Not A Level")
	if got := ModelDefaultsFor(store, "claude").StartEffort; got != "" {
		t.Fatalf("want a stored level the rule refuses to read as none, got %q", got)
	}
}

// The ring stores an effort beside every model, checked against the coder's
// levels, a refused one moving nothing, and the frame carries them.
func TestTheRingStoresTheEffortsAgainstTheCodersLevels(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	owner, err := svc.create("claude")
	if err != nil {
		t.Fatal(err)
	}
	frames := collectFrames(svc, owner.ID)
	saved, err := svc.SetModels(owner.ID, ModelChoice{ChatEffort: "high", ChatEffortSet: true, TriggerEffort: "low", TriggerEffortSet: true})
	if err != nil || saved.Effort != "high" || saved.TriggerEffort != "low" || saved.CheckEffort != "" {
		t.Fatalf("want the efforts stored, got %+v %v", saved.Summary, err)
	}
	if _, err := svc.SetModels(owner.ID, ModelChoice{CheckEffort: "xhigh", CheckEffortSet: true, Chat: "fable", ChatSet: true}); err == nil {
		t.Fatal("want a level the coder does not take refused")
	}
	if got, _ := svc.Get(owner.ID); got.Model != "" || got.CheckEffort != "" {
		t.Fatalf("a refused save moved a pick: %+v", got.Summary)
	}
	waitFor(t, "the models frame", func() bool { return frames.has(FrameModels) })
	got := frames.stop()
	if len(got) != 1 || got[0].Models == nil || got[0].Models.ChatEffort != "high" || got[0].Models.TriggerEffort != "low" {
		t.Fatalf("want one models frame carrying the efforts, got %+v", got)
	}
}

// A refusal of an effort says where it was set, the ring for a chat or a
// check, the trigger for a reaction whose level was the trigger's own.
func TestAnEffortRefusalSaysWhereToFixIt(t *testing.T) {
	refused := UnknownEffort("max", "gpt-5-mini")
	if got := refused.placed(RunRecord{Kind: RunChat, Effort: "max"}).Error(); got != "The coder does not take the effort max with the model gpt-5-mini. Pick another effort at the ring button of this assistant." {
		t.Fatalf("chat: %q", got)
	}
	if got := refused.placed(RunRecord{Kind: RunReaction, Model: "opencode/beam-free", Effort: "none", TriggerEffort: true}).Error(); got != "The coder does not take the effort none with the model opencode/beam-free. Pick another effort on the trigger." {
		t.Fatalf("reaction: %q", got)
	}
	if got := ErrEffortNeedsModel.placed(RunRecord{Kind: RunCheck, Effort: "high"}).Error(); got != "The coder takes an effort only together with a model. Pick a model or clear the effort at the ring button of this assistant." {
		t.Fatalf("needs a model: %q", got)
	}
	if got := refused.placed(RunRecord{Kind: RunChat, Effort: "max", EffortFrom: ModelFromCoder}).Error(); got != "The coder does not take the effort max with the model gpt-5-mini. Pick another effort under Settings, Coder, Models." {
		t.Fatalf("a start default's refusal must send the user to Settings: %q", got)
	}
	if got := UnknownEffort("Two Words", "two words"); got.Effort != "" || got.Model != "" {
		t.Fatalf("want what the rules refuse dropped, got %+v", got)
	}
	if !errors.Is(UnknownEffort("max", "").placed(RunRecord{}), &Refusal{Kind: RefusalUnknownEffort}) {
		t.Fatal("want a placed copy to match its kind")
	}
}

// A chat turn starts at the resolved effort and its answer says so; a
// refusal the runner names before anything starts ends the turn with the
// placed sentence.
func TestAChatTurnRunsAtItsEffortAndARunnersRefusalIsPlaced(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}}
	svc, _, _ := newTestService(t, runner)
	owner, err := svc.create("claude")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetModels(owner.ID, ModelChoice{ChatEffort: "high", ChatEffortSet: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(owner.ID, "hello", nil); err != nil {
		t.Fatal(err)
	}
	answer := waitIdle(t, svc, owner.ID)
	if got := runner.turns(); len(got) != 1 || got[0].Effort != "high" || answer.Effort != "high" {
		t.Fatalf("want the turn and its answer at high, got %+v and %+v", got, answer)
	}

	runner.mu.Lock()
	runner.commandErr = UnknownEffort("high", "opencode/big-pickle")
	runner.mu.Unlock()
	if _, err := svc.Send(owner.ID, "again", nil); err != nil {
		t.Fatal(err)
	}
	failed := waitIdle(t, svc, owner.ID)
	if failed.Error != "The coder does not take the effort high with the model opencode/big-pickle. Pick another effort at the ring button of this assistant." {
		t.Fatalf("want the placed refusal, got %q", failed.Error)
	}
}

// A check runs at the chat's effort where nothing narrower is picked, and
// its report says which effort it ran at.
func TestACheckRunsAtTheChatsEffort(t *testing.T) {
	f := newJobFixture(t, "WORKING: going")
	if _, err := f.svc.SetModels(f.owner.ID, ModelChoice{ChatEffort: "max", ChatEffortSet: true}); err != nil {
		t.Fatal(err)
	}
	f.steered(t)
	f.watcher.Handle("term-1")
	waitFor(t, "the check to start", func() bool { return len(f.runner.turns()) == 1 })
	if got := f.runner.turns()[0].Effort; got != "max" {
		t.Fatalf("want the check at the chat's effort, got %q", got)
	}
}

// A trigger carries an effort of its own only where somebody set one, which
// marks the register so a refusal of it points to the trigger.
func TestAReactionRunsAtTheTriggersOwnEffort(t *testing.T) {
	runner := &fakeRunner{answer: eventAnswer}
	f := newEventFixtureIn(t, t.TempDir(), runner)
	f.create(t)
	if _, err := f.svc.SetModels(f.owner.ID, ModelChoice{ChatEffort: "low", ChatEffortSet: true}); err != nil {
		t.Fatal(err)
	}
	f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize", BatchSet: true, Effort: "high", EffortSet: true})
	f.reactor.Publish(f.jobDone("readme"))
	waitFor(t, "the reaction to start", func() bool { return len(f.reactionTurns()) == 1 })
	if got := f.reactionTurns()[0].Effort; got != "high" {
		t.Fatalf("want the trigger's own effort, got %q", got)
	}
	f.waitPushed(t, 1)
}

func TestATriggersEffortIsCheckedAndNamedWhenItMoves(t *testing.T) {
	f := newEventFixtureIn(t, t.TempDir(), &fakeRunner{answer: eventAnswer})
	f.create(t)
	if _, err := f.reactor.Add(TriggerSpec{Owner: f.owner.ID, Event: "job-done", Task: "x", Effort: "xhigh", EffortSet: true}); err == nil || !strings.Contains(err.Error(), "The effort is one of") {
		t.Fatalf("want a level the owner's coder does not take refused, got %v", err)
	}
	if _, err := f.reactor.Add(TriggerSpec{Owner: f.owner.ID, Event: "job-done", Task: "x", Effort: "Bad Level", EffortSet: true}); err == nil {
		t.Fatal("want a level of the wrong shape refused")
	}
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "x", Effort: "low", EffortSet: true})
	if trigger.Effort != "low" {
		t.Fatalf("want the trigger's own effort stored, got %q", trigger.Effort)
	}
	if _, _, err := f.reactor.Edit(trigger.ID, "", TriggerSpec{Effort: "xhigh", EffortSet: true}); err == nil {
		t.Fatal("want an edit to a level the coder does not take refused")
	}
	edited, changed, err := f.reactor.Edit(trigger.ID, "", TriggerSpec{Effort: "max", EffortSet: true})
	if err != nil || edited.Effort != "max" || changed != "effort max" {
		t.Fatalf("want the effort moved and named, got %q %q %v", edited.Effort, changed, err)
	}
	if _, changed, err := f.reactor.Edit(trigger.ID, "", TriggerSpec{Effort: "", EffortSet: true}); err != nil || changed != "the assistant's effort" {
		t.Fatalf("want a cleared effort named, got %q %v", changed, err)
	}
	if _, changed, err := f.reactor.Edit(trigger.ID, "", TriggerSpec{Task: "y"}); err != nil || changed != "task" {
		t.Fatalf("want an edit without the field to leave the effort, got %q %v", changed, err)
	}
}
