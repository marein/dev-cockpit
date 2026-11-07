package assistant

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// An assistant another one starts is a plain one of the user's: named as
// asked, its first message in its own thread as the user's and answered by a
// turn of its own, a workspace and a session of its own, nothing of the one
// that asked. A message the send would refuse is refused before anything
// exists, and without a message nothing is written.
func TestAnAssistantStartsWithANameAndAFirstMessage(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "on it"}}}
	svc, _, _ := newTestService(t, runner)
	asker, _ := svc.create("claude")

	if _, _, err := svc.CreateWith(Handover{Name: "x", Message: strings.Repeat("x", MaxPromptBytes+1)}); err == nil || len(svc.List()) != 1 {
		t.Fatalf("a message too long made an assistant: %v, %d assistants", err, len(svc.List()))
	}

	created, run, err := svc.CreateWith(Handover{Name: " Release notes ", Message: " Write the notes for 2.1. "})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if created.Title != "Release notes" || run.RunID == "" || len(svc.List()) != 2 {
		t.Fatalf("started %+v with run %+v, %d assistants", created.Summary, run, len(svc.List()))
	}
	if answer := waitIdle(t, svc, created.ID); answer.Content != "on it" {
		t.Fatalf("the first message was answered with %+v", answer)
	}
	thread, err := svc.Get(created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(thread.Messages) != 2 || thread.Messages[0].Role != RoleUser || thread.Messages[0].Content != "Write the notes for 2.1." {
		t.Fatalf("the new thread holds %+v", thread.Messages)
	}
	if thread.NativeSessionID == asker.NativeSessionID || mustWorkdir(t, svc, created.ID) == mustWorkdir(t, svc, asker.ID) {
		t.Fatal("the new assistant shares a session or a workspace with the one that asked")
	}

	// Without a name the first message names it, the way it names one made
	// on the page; without a message it stands empty under the default.
	named, _, err := svc.CreateWith(Handover{Message: "Watch the deploy"})
	if err != nil || named.Title != "Watch the deploy" {
		t.Fatalf("a start without a name: %+v, %v", named.Summary, err)
	}
	waitIdle(t, svc, named.ID)
	empty, run, err := svc.CreateWith(Handover{})
	if err != nil || run.RunID != "" || empty.Title != DefaultTitle {
		t.Fatalf("a start without a message: %+v, %+v, %v", empty.Summary, run, err)
	}
	if after, _ := svc.Get(empty.ID); len(after.Messages) != 0 {
		t.Fatalf("a start without a message wrote %+v", after.Messages)
	}
}

// A prompt from another assistant is a message the user sent: answered at once
// when the assistant is free, waiting in its queue behind the turn that runs
// like a second message typed on the page, stored the same way down to the
// last field. Only the composer is left alone, its unsent words are the
// user's. What the page's send refuses is refused here too.
func TestAPromptGoesInAsTheUsersOwnMessage(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}, block: make(chan struct{})}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")

	for _, bad := range []string{"  ", strings.Repeat("x", MaxPromptBytes+1)} {
		if _, err := svc.Prompt(created.ID, bad); err == nil {
			t.Fatalf("a prompt of %d bytes went in", len(bad))
		}
	}
	if _, err := svc.Prompt("no-such-assistant", "hello"); err == nil {
		t.Fatal("a prompt reached an assistant that does not exist")
	}

	if _, _, err := svc.SaveDraft(created.ID, "half typed", nil); err != nil {
		t.Fatalf("draft: %v", err)
	}
	first, err := svc.Prompt(created.ID, " first ")
	if err != nil || first.Queued || first.RunID == "" || first.Title != "first" {
		t.Fatalf("a prompt into a free assistant: %+v, %v", first, err)
	}
	waiting, err := svc.Prompt(created.ID, "second")
	if err != nil || !waiting.Queued {
		t.Fatalf("a prompt into a busy assistant: %+v, %v", waiting, err)
	}
	if draft, _ := svc.Draft(created.ID); draft.Text != "half typed" {
		t.Fatalf("the prompt took the composer's words: %q", draft.Text)
	}
	typed, err := svc.Send(created.ID, "third", nil)
	if err != nil || !typed.Queued {
		t.Fatalf("a typed message into a busy assistant: %+v, %v", typed, err)
	}

	thread, _ := svc.Get(created.ID)
	queued := queuedMessages(thread)
	if len(queued) != 2 || thread.Messages[0].Role != RoleUser || thread.Messages[0].Content != "first" {
		t.Fatalf("the thread holds %+v", thread.Messages)
	}
	same := func(m Message) Message {
		m.ID, m.Content, m.CreatedAt = "", "", time.Time{}
		return m
	}
	if prompted, mine := same(queued[0]), same(queued[1]); !reflect.DeepEqual(prompted, mine) {
		t.Fatalf("a prompt is stored apart from a typed message: %+v against %+v", prompted, mine)
	}

	close(runner.block)
	waitFor(t, "the queue to go out", func() bool { return len(runner.turns()) == 2 })
	waitIdle(t, svc, created.ID)
	turn := runner.turns()[1].Prompt
	if !strings.Contains(turn, "--- Message 1 ---\nsecond") || !strings.Contains(turn, "--- Message 2 ---\nthird") {
		t.Fatalf("the queue went out as %q", turn)
	}
}

// A name given with assistant-new stays through its first message and every
// message after, even the default's own; so does one given by a rename before
// the first message. Only an assistant that got no name is named by it.
func TestAGivenNameSurvivesTheFirstMessage(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}})
	for _, name := range []string{DefaultTitle, "Release notes"} {
		created, run, err := svc.CreateWith(Handover{Name: name, Message: "Write the notes for 2.1"})
		if err != nil || run.RunID == "" || created.Title != name {
			t.Fatalf("a start named %q: %+v, %+v, %v", name, created.Summary, run, err)
		}
		waitIdle(t, svc, created.ID)
		if _, err := svc.Prompt(created.ID, "And the changelog"); err != nil {
			t.Fatalf("prompt: %v", err)
		}
		waitIdle(t, svc, created.ID)
		if got, _ := svc.Get(created.ID); got.Title != name || !got.Named {
			t.Fatalf("a start named %q is called %q, named %v", name, got.Title, got.Named)
		}
	}

	renamed, _ := svc.create("claude")
	if err := svc.Rename(renamed.ID, DefaultTitle); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := svc.Send(renamed.ID, "Watch the deploy", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, renamed.ID)
	if got, _ := svc.Get(renamed.ID); got.Title != DefaultTitle {
		t.Fatalf("the first message renamed a named assistant to %q", got.Title)
	}

	unnamed, _, err := svc.CreateWith(Handover{Message: "Watch the deploy"})
	if err != nil || unnamed.Title != "Watch the deploy" || unnamed.Named {
		t.Fatalf("a start without a name: %+v, %v", unnamed.Summary, err)
	}
	waitIdle(t, svc, unnamed.ID)
}
