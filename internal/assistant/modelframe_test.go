package assistant

import "testing"

// A pick set at the ring, from the CLI or on another tab reaches every open
// page of that assistant over its own stream: SetModels publishes one models
// frame on the instance's own hub channel, the way the message frames go,
// carrying the assistant's id, the three picks as stored and the moment they
// were written. Nothing else moves on the stream for it, a refused name
// publishes nothing because nothing moved, and another assistant's stream
// hears none of it, so no page ever applies a reading that is not its own.
func TestSetModelsPublishesTheFreshPicksOnTheAssistantsOwnStream(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	a, err := svc.create("claude")
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := svc.create("claude")
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	framesA := collectFrames(svc, a.ID)
	framesB := collectFrames(svc, b.ID)

	first, err := svc.SetModels(a.ID, ModelChoice{Chat: "fable", ChatSet: true, Trigger: "haiku", TriggerSet: true})
	if err != nil {
		t.Fatalf("set the chat and the trigger model: %v", err)
	}
	if _, err := svc.SetModels(a.ID, ModelChoice{Chat: "two words", ChatSet: true}); err == nil {
		t.Fatal("want a name with a space refused")
	}
	second, err := svc.SetModels(a.ID, ModelChoice{Check: "sonnet", CheckSet: true})
	if err != nil {
		t.Fatalf("set the check model: %v", err)
	}

	gotA := framesA.stop()
	gotB := framesB.stop()
	if len(gotB) != 0 {
		t.Fatalf("want nothing on the other assistant's stream, got %+v", gotB)
	}
	want := []ModelPicks{
		{Assistant: a.ID, Chat: "fable", Trigger: "haiku", UpdatedAt: first.UpdatedAt},
		{Assistant: a.ID, Chat: "fable", Check: "sonnet", Trigger: "haiku", UpdatedAt: second.UpdatedAt},
	}
	if len(gotA) != len(want) {
		t.Fatalf("want one models frame per save that moved and nothing for the refused one, got %+v", gotA)
	}
	for i, frame := range gotA {
		if frame.Kind != FrameModels || frame.MessageID != "" || frame.RunID != "" || frame.Models == nil {
			t.Fatalf("frame %d: want a bare models frame, got %+v", i, frame)
		}
		got := *frame.Models
		if got.Assistant != want[i].Assistant || got.Chat != want[i].Chat || got.Check != want[i].Check || got.Trigger != want[i].Trigger || !got.UpdatedAt.Equal(want[i].UpdatedAt) {
			t.Fatalf("frame %d: want %+v, got %+v", i, want[i], got)
		}
	}
	if first.ModelPicks().Assistant != a.ID || second.ModelPicks().Check != "sonnet" {
		t.Fatalf("want the answered instance to carry the same reading, got %+v and %+v", first.ModelPicks(), second.ModelPicks())
	}
}
