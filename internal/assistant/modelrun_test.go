package assistant

import (
	"strings"
	"testing"
)

// The chain is what a turn really starts on, not only what a label says: for
// an assistant whose chat is picked at the ring and nothing else set, a check
// of one of its jobs carries that model in the TurnRequest the runner builds
// its command from, and on the RunRecord in the register, which is what a
// report or a log reads to say which model a run ran on. The turn is held
// open so the register still holds the record while it is read.
func TestACheckStartsOnTheRingsChatPick(t *testing.T) {
	f := newJobFixture(t, "WORKING: going")
	block := make(chan struct{})
	f.runner.mu.Lock()
	f.runner.hold = func(TurnRequest) chan struct{} { return block }
	f.runner.mu.Unlock()
	if _, err := f.svc.SetModels(f.owner.ID, ModelChoice{Chat: "fable", ChatSet: true}); err != nil {
		t.Fatalf("set the chat model: %v", err)
	}
	f.steered(t)
	f.watcher.Handle("term-1")

	waitFor(t, "the check to start", func() bool { return len(f.runner.turns()) == 1 })
	req := f.runner.turns()[0]
	if req.Model != "fable" {
		t.Fatalf("want the check started on the ring's chat pick, got TurnRequest.Model %q", req.Model)
	}
	rec := waitRegistered(t, f.svc, RunCheck)
	if rec.Model != "fable" || rec.TriggerModel || rec.SessionID != req.SessionID {
		t.Fatalf("want the register to say the check ran on fable, got %+v", rec)
	}
	close(block)
	f.waitWakes(t, "term-1", 1)
}

// The same for a reaction: a trigger without a model of its own follows the
// assistant's chat, ring pick included, and the register says so without
// marking the model as the trigger's own. A trigger that picks its own model
// stands above the ring pick, and that one the register marks as the
// trigger's, which is what sends a refusal of it to the trigger and not to
// the ring.
func TestAReactionStartsOnTheRingsChatPick(t *testing.T) {
	req, rec := heldReaction(t, "fable", "")
	if req.Model != "fable" {
		t.Fatalf("want the reaction started on the ring's chat pick, got TurnRequest.Model %q", req.Model)
	}
	if rec.Model != "fable" || rec.TriggerModel || rec.SessionID != req.SessionID {
		t.Fatalf("want the register to say the reaction ran on fable as the assistant's model, got %+v", rec)
	}

	req, rec = heldReaction(t, "fable", "haiku")
	if req.Model != "haiku" {
		t.Fatalf("want the trigger's own model above the ring's chat pick, got TurnRequest.Model %q", req.Model)
	}
	if rec.Model != "haiku" || !rec.TriggerModel {
		t.Fatalf("want the register to say the reaction ran on the trigger's own haiku, got %+v", rec)
	}
}

// heldReaction fires one trigger of a fresh assistant whose chat is picked as
// chat, the trigger on triggerModel where one is named, and answers the
// reaction's request and its register entry while the turn is still held.
func heldReaction(t *testing.T, chat, triggerModel string) (TurnRequest, RunRecord) {
	t.Helper()
	block := make(chan struct{})
	runner := &fakeRunner{answer: eventAnswer, hold: func(req TurnRequest) chan struct{} {
		if strings.Contains(req.Prompt, "This turn was started by the cockpit") {
			return block
		}
		return nil
	}}
	f := newEventFixtureIn(t, t.TempDir(), runner)
	f.create(t)
	if _, err := f.svc.SetModels(f.owner.ID, ModelChoice{Chat: chat, ChatSet: true}); err != nil {
		t.Fatalf("set the chat model: %v", err)
	}
	f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize what is done", BatchSet: true, Model: triggerModel, ModelSet: triggerModel != ""})
	f.reactor.Publish(f.jobDone("readme"))

	waitFor(t, "the reaction to start", func() bool { return len(f.reactionTurns()) == 1 })
	req := f.reactionTurns()[0]
	rec := waitRegistered(t, f.svc, RunReaction)
	close(block)
	f.waitPushed(t, 1)
	return req, rec
}

// waitRegistered answers the one running entry of a kind once the launch has
// written it: the record is saved after the runner built the command, so a
// turn that already stands in the runner's requests may not stand in the
// register yet.
func waitRegistered(t *testing.T, svc *Service, kind RunKind) RunRecord {
	t.Helper()
	var found RunRecord
	waitFor(t, "the "+string(kind)+" to stand in the register", func() bool {
		for _, rec := range svc.runs.List() {
			if rec.Kind == kind {
				found = rec
				return true
			}
		}
		return false
	})
	return found
}

// The chat is read when the check starts, never when the job was steered:
// a job steered before the chat is moved at the ring runs its next check on
// the chat as it stands then, with nothing stored on the assistant's check
// model. That is what Same as chat stored as nothing means, and it is what a
// copied name could never do.
func TestACheckFollowsTheChatMovedAfterTheSteer(t *testing.T) {
	f := newJobFixture(t, "WORKING: going")
	block := make(chan struct{})
	f.runner.mu.Lock()
	f.runner.hold = func(TurnRequest) chan struct{} { return block }
	f.runner.mu.Unlock()
	f.steered(t)
	if _, err := f.svc.SetModels(f.owner.ID, ModelChoice{Chat: "sonnet", ChatSet: true}); err != nil {
		t.Fatalf("set the chat model: %v", err)
	}
	f.watcher.Handle("term-1")

	waitFor(t, "the check to start", func() bool { return len(f.runner.turns()) == 1 })
	if req := f.runner.turns()[0]; req.Model != "sonnet" {
		t.Fatalf("want the check on the chat moved after the steer, got TurnRequest.Model %q", req.Model)
	}
	if fresh, _ := f.svc.Get(f.owner.ID); fresh.CheckModel != "" {
		t.Fatalf("want nothing stored for Same as chat, got CheckModel %q", fresh.CheckModel)
	}
	close(block)
	f.waitWakes(t, "term-1", 1)
}

// The same for a reaction: a trigger made without a model of its own before
// the chat is moved at the ring reacts on the chat as it stands when the
// event fires, and its own model stays empty through it.
func TestAReactionFollowsTheChatMovedAfterTheTrigger(t *testing.T) {
	block := make(chan struct{})
	runner := &fakeRunner{answer: eventAnswer, hold: func(req TurnRequest) chan struct{} {
		if strings.Contains(req.Prompt, "This turn was started by the cockpit") {
			return block
		}
		return nil
	}}
	f := newEventFixtureIn(t, t.TempDir(), runner)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize what is done", BatchSet: true})
	if trigger.Model != "" {
		t.Fatalf("want a trigger made without a model to store none, got %q", trigger.Model)
	}
	if _, err := f.svc.SetModels(f.owner.ID, ModelChoice{Chat: "sonnet", ChatSet: true}); err != nil {
		t.Fatalf("set the chat model: %v", err)
	}
	f.reactor.Publish(f.jobDone("readme"))

	waitFor(t, "the reaction to start", func() bool { return len(f.reactionTurns()) == 1 })
	if req := f.reactionTurns()[0]; req.Model != "sonnet" {
		t.Fatalf("want the reaction on the chat moved after the trigger was made, got TurnRequest.Model %q", req.Model)
	}
	if rec := waitRegistered(t, f.svc, RunReaction); rec.Model != "sonnet" || rec.TriggerModel {
		t.Fatalf("want the register to say the reaction ran on the assistant's sonnet, got %+v", rec)
	}
	if stored, _ := f.reactor.Get(trigger.ID); stored.Model != "" {
		t.Fatalf("want the trigger's own model still empty, got %q", stored.Model)
	}
	close(block)
	f.waitPushed(t, 1)
}

// The defaults of the Models tab never reach a running turn. An assistant
// and a trigger made before the defaults were set carry nothing of their
// own, so with the defaults set afterwards and nothing picked, a check and a
// reaction still run on the chat, here the coder's start default, read
// through CoderInfo.Defaults the way the service reads it: Same as chat is
// read when the turn starts, and the tab's three are creation defaults that
// a turn never asks for, the chat default among them.
func TestTheTabsDefaultsNeverChangeARunningTurn(t *testing.T) {
	block := make(chan struct{})
	runner := &fakeRunner{answer: eventAnswer, hold: func(req TurnRequest) chan struct{} {
		if strings.Contains(req.Prompt, "This turn was started by the cockpit") {
			return block
		}
		return nil
	}}
	f := newEventFixtureIn(t, t.TempDir(), runner)
	f.create(t)
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize what is done", BatchSet: true})
	f.svc.coders = fakeCoders{runner: runner, defaults: ModelDefaults{Chat: "d-chat", Check: "d-check", Trigger: "d-trigger", Start: "haiku"}}
	f.reactor.Publish(f.jobDone("readme"))

	waitFor(t, "the reaction to start", func() bool { return len(f.reactionTurns()) == 1 })
	if req := f.reactionTurns()[0]; req.Model != "haiku" {
		t.Fatalf("want the reaction on the coder's start default and never a default of the tab, got TurnRequest.Model %q", req.Model)
	}
	if stored, _ := f.reactor.Get(trigger.ID); stored.Model != "" {
		t.Fatalf("want a default set later copied onto nothing, got %q on the trigger", stored.Model)
	}
	close(block)
	f.waitPushed(t, 1)

	g := newJobFixture(t, "WORKING: going")
	hold := make(chan struct{})
	g.runner.mu.Lock()
	g.runner.hold = func(TurnRequest) chan struct{} { return hold }
	g.runner.mu.Unlock()
	g.steered(t)
	g.svc.coders = fakeCoders{runner: g.runner, defaults: ModelDefaults{Chat: "d-chat", Check: "d-check", Trigger: "d-trigger", Start: "haiku"}}
	g.watcher.Handle("term-1")
	waitFor(t, "the check to start", func() bool { return len(g.runner.turns()) == 1 })
	if req := g.runner.turns()[0]; req.Model != "haiku" {
		t.Fatalf("want the check on the coder's start default and never a default of the tab, got TurnRequest.Model %q", req.Model)
	}
	if fresh, _ := g.svc.Get(g.owner.ID); fresh.Model != "" || fresh.CheckModel != "" {
		t.Fatalf("want a default set later copied onto nothing, got Model %q and CheckModel %q", fresh.Model, fresh.CheckModel)
	}
	close(hold)
	g.waitWakes(t, "term-1", 1)
}

// A chat turn reads the coder's start default through the same
// CoderInfo.Defaults, which is the join between the settings store and
// startLocked: with the start default set and nothing picked at the ring,
// the turn's request carries it, and the chat default the tab holds beside
// it, set after this assistant was made, is copied onto nothing and reaches
// no turn.
func TestAChatTurnStartsOnTheCodersStartDefault(t *testing.T) {
	runner := &fakeRunner{}
	svc, _, _ := newTestService(t, runner)
	c, err := svc.create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	svc.coders = fakeCoders{runner: runner, defaults: ModelDefaults{Chat: "d-chat", Check: "d-check", Trigger: "d-trigger", Start: "haiku"}}
	if _, err := svc.Send(c.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, "the chat turn to start", func() bool { return len(runner.turns()) == 1 })
	if req := runner.turns()[0]; req.Model != "haiku" {
		t.Fatalf("want the chat turn on the coder's start default, got TurnRequest.Model %q", req.Model)
	}
	if fresh, _ := svc.Get(c.ID); fresh.Model != "" {
		t.Fatalf("want a chat default set after the create copied onto nothing, got Model %q", fresh.Model)
	}
}

// The three are creation defaults for assistants alone: an assistant made
// while the Chat, Checks and Trigger defaults stand carries them as its chat
// model, its check model and its trigger model, and with the defaults empty
// it is made with nothing, the coder's default and Same as chat. A trigger
// is copied nothing at all, it carries a model only where the spec set one:
// one made without a model while the Trigger default stands carries none,
// its owner's trigger model is what it follows.
func TestTheTabsDefaultsAreCopiedOntoANewAssistantOnly(t *testing.T) {
	f := newEventFixture(t)
	f.svc.coders = fakeCoders{runner: f.runner, defaults: ModelDefaults{Chat: "haiku", Check: "sonnet", Trigger: "opus"}}
	owner := f.create(t)
	if owner.Model != "haiku" || owner.CheckModel != "sonnet" || owner.TriggerModel != "opus" {
		t.Fatalf("want the Chat, Checks and Trigger defaults copied onto the new assistant, got %+v", owner.Summary)
	}
	plain := f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize", BatchSet: true})
	if plain.Model != "" {
		t.Fatalf("want a trigger made without a model to carry none while the Trigger default stands, got %q", plain.Model)
	}
	own := f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize", BatchSet: true, Model: "fable", ModelSet: true})
	if own.Model != "fable" {
		t.Fatalf("want a trigger's own pick kept, got %q", own.Model)
	}

	f.svc.coders = fakeCoders{runner: f.runner}
	bare := f.create(t)
	if bare.Model != "" || bare.CheckModel != "" || bare.TriggerModel != "" {
		t.Fatalf("want an assistant made without the defaults to carry none, got %+v", bare.Summary)
	}
}

// The ring's Triggers pick is what a trigger without a model of its own runs
// on, read when the reaction starts: a trigger made before the pick was set
// reacts on the pick, and with the pick cleared again the next reaction runs
// on the chat, with nothing stored on the trigger through either. The
// register says the model was the assistant's, not the trigger's own, so a
// refusal of it goes to the ring.
func TestAReactionFollowsTheRingsTriggerPick(t *testing.T) {
	block := make(chan struct{})
	runner := &fakeRunner{answer: eventAnswer, hold: func(req TurnRequest) chan struct{} {
		if strings.Contains(req.Prompt, "This turn was started by the cockpit") {
			return block
		}
		return nil
	}}
	f := newEventFixtureIn(t, t.TempDir(), runner)
	f.create(t)
	if _, err := f.svc.SetModels(f.owner.ID, ModelChoice{Chat: "sonnet", ChatSet: true}); err != nil {
		t.Fatalf("set the chat model: %v", err)
	}
	trigger := f.trigger(t, TriggerSpec{Event: "job-done", Task: "summarize what is done", BatchSet: true})
	if _, err := f.svc.SetModels(f.owner.ID, ModelChoice{Trigger: "haiku", TriggerSet: true}); err != nil {
		t.Fatalf("set the trigger model: %v", err)
	}
	f.reactor.Publish(f.jobDone("readme"))

	waitFor(t, "the reaction to start", func() bool { return len(f.reactionTurns()) == 1 })
	if req := f.reactionTurns()[0]; req.Model != "haiku" {
		t.Fatalf("want the reaction on the ring's trigger pick set after the trigger was made, got TurnRequest.Model %q", req.Model)
	}
	if rec := waitRegistered(t, f.svc, RunReaction); rec.Model != "haiku" || rec.TriggerModel {
		t.Fatalf("want the register to say the reaction ran on the assistant's haiku, got %+v", rec)
	}
	if stored, _ := f.reactor.Get(trigger.ID); stored.Model != "" {
		t.Fatalf("want the trigger's own model still empty, got %q", stored.Model)
	}
	close(block)
	f.waitPushed(t, 1)

	again := make(chan struct{})
	runner.mu.Lock()
	runner.hold = func(req TurnRequest) chan struct{} {
		if strings.Contains(req.Prompt, "This turn was started by the cockpit") {
			return again
		}
		return nil
	}
	runner.mu.Unlock()
	if _, err := f.svc.SetModels(f.owner.ID, ModelChoice{Trigger: "", TriggerSet: true}); err != nil {
		t.Fatalf("clear the trigger model: %v", err)
	}
	f.reactor.Publish(f.jobDone("changelog"))
	waitFor(t, "the second reaction to start", func() bool { return len(f.reactionTurns()) == 2 })
	if req := f.reactionTurns()[1]; req.Model != "sonnet" {
		t.Fatalf("want the reaction on the chat once the trigger pick is cleared, got TurnRequest.Model %q", req.Model)
	}
	if stored, _ := f.reactor.Get(trigger.ID); stored.Model != "" {
		t.Fatalf("want the trigger's own model still empty, got %q", stored.Model)
	}
	close(again)
	f.waitPushed(t, 2)
}
