package assistant

import (
	"strings"
	"testing"
	"time"
)

// The end of a compose run an assistant started is a note in that
// assistant's thread and nobody else's, read by a headline that says what
// happened and where, carrying the run it is about, and it is an event a
// trigger of that owner matches under the kind the end was.
func TestAComposeEndIsANoteAndAnEventOfItsOwner(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	owner, err := svc.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	// A trigger of the owner on the umbrella and one of the other assistant
	// are the two witnesses: an event lands in the window of the first and
	// never in the second's.
	mine, err := svc.Events().Add(TriggerSpec{Owner: owner.ID, Event: "compose-ended", Task: "say so"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := svc.Events().Add(TriggerSpec{Owner: other.ID, Event: "compose-ended", Task: "say so"})
	if err != nil {
		t.Fatal(err)
	}

	id := svc.RecordCompose(ComposeReport{
		Owner: owner.ID, Run: "run-1", Project: "shop", Stack: "ops", Action: "Compose up",
		URL: "/projects/shop/docker/runs/run-1", Exited: true, Exit: 0, Output: "Container shop-web-1 Started",
	})
	if id == "" {
		t.Fatal("no note was written")
	}
	fresh, _ := svc.Get(owner.ID)
	if len(fresh.Messages) != 1 || !fresh.Messages[0].IsNote() {
		t.Fatalf("the owner's thread holds %+v", fresh.Messages)
	}
	note := fresh.Messages[0].Note
	if note.Source != NoteCompose || note.Verdict != ComposeKindDone || note.Run != "run-1" || note.Name != "Compose up" || note.Project != "shop" {
		t.Fatalf("the note reads %+v", note)
	}
	if note.Headline != "Compose done: Compose up on ops in shop" {
		t.Fatalf("the headline reads %q", note.Headline)
	}
	body := fresh.Messages[0].Content
	for _, want := range []string{"On ops in shop: went through, exit status 0.", "Container shop-web-1 Started", "[Open the run](/projects/shop/docker/runs/run-1)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the note does not say %q:\n%s", want, body)
		}
	}
	if elsewhere, _ := svc.Get(other.ID); len(elsewhere.Messages) != 0 {
		t.Fatal("the note landed in another assistant's thread")
	}
	got, _ := svc.Events().Get(mine.ID)
	if len(got.Pending) != 1 {
		t.Fatalf("the owner's trigger holds %+v", got.Pending)
	}
	seen := got.Pending[0]
	if seen.Source != EventCompose || seen.Kind != ComposeKindDone || seen.Target != "run-1" || seen.Owner != owner.ID {
		t.Fatalf("the event reads %+v", seen)
	}
	if seen.Headline != note.Headline || !strings.Contains(seen.Body, "went through") {
		t.Fatalf("the event does not carry the note: %+v", seen)
	}
	if not, _ := svc.Events().Get(theirs.ID); len(not.Pending) != 0 {
		t.Fatalf("the other assistant's trigger holds %+v", not.Pending)
	}
}

// A failed run and a declined run read as what they are, in the headline,
// in the text and in the event's kind; a declined run carries no exit code
// and quotes no output.
func TestAFailedAndADeclinedComposeEndReadAsSuch(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	owner, _ := svc.Create("claude")
	watch, err := svc.Events().Add(TriggerSpec{Owner: owner.ID, Event: "compose-ended", Task: "say so"})
	if err != nil {
		t.Fatal(err)
	}

	svc.RecordCompose(ComposeReport{Owner: owner.ID, Run: "r-fail", Project: "shop", Action: "Compose build", URL: "/x",
		Failed: true, Reason: "exit status 1", Exited: true, Exit: 1, Output: "ERROR: build failed"})
	svc.RecordCompose(ComposeReport{Owner: owner.ID, Run: "r-no", Project: "shop", Action: "Compose down with volumes", URL: "/y",
		Failed: true, Declined: true, Reason: "declined by the user"})
	fresh, _ := svc.Get(owner.ID)
	if len(fresh.Messages) != 2 {
		t.Fatalf("the thread holds %d messages", len(fresh.Messages))
	}
	failed, declined := fresh.Messages[0], fresh.Messages[1]
	if failed.Note.Headline != "Compose failed: Compose build on shop" || failed.Note.Verdict != ComposeKindFailed {
		t.Fatalf("the failed note reads %+v", failed.Note)
	}
	if !strings.Contains(failed.Content, "On shop: failed, exit status 1.") || !strings.Contains(failed.Content, "ERROR: build failed") {
		t.Fatalf("the failed note says:\n%s", failed.Content)
	}
	if declined.Note.Headline != "Compose declined: Compose down with volumes on shop" || declined.Note.Verdict != ComposeKindDeclined {
		t.Fatalf("the declined note reads %+v", declined.Note)
	}
	if !strings.Contains(declined.Content, "On shop: never ran, declined by the user.") || strings.Contains(declined.Content, "```") || strings.Contains(declined.Content, "exit status") {
		t.Fatalf("the declined note says:\n%s", declined.Content)
	}
	got, _ := svc.Events().Get(watch.ID)
	if len(got.Pending) != 2 || got.Pending[0].Kind != ComposeKindFailed || got.Pending[1].Kind != ComposeKindDeclined {
		t.Fatalf("the events read %+v", got.Pending)
	}
}

// A note is dropped rather than written into whichever assistant is around
// when its owner is gone, the way a check's report is.
func TestAComposeNoteOfAGoneAssistantIsDropped(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	other, _ := svc.Create("claude")
	if id := svc.RecordCompose(ComposeReport{Owner: "44444444-4444-4444-8444-444444444444", Run: "r", Action: "Compose up"}); id != "" {
		t.Fatalf("want nothing written, got %q", id)
	}
	if fresh, _ := svc.Get(other.ID); len(fresh.Messages) != 0 {
		t.Fatal("the note landed in another assistant")
	}
}

// The compose kinds are trigger events: each narrow kind matches its own end
// and the umbrella every end, only for the owner, and a terminal is no
// filter on them.
func TestComposeEventsMatchTriggersByKindAndOwner(t *testing.T) {
	ev := func(kind, owner string) CockpitEvent {
		return CockpitEvent{Source: EventCompose, Kind: kind, Target: "run-1", Owner: owner, Time: time.Now()}
	}
	for _, tc := range []struct {
		trigger string
		kind    string
		owner   string
		want    bool
	}{
		{"compose-done", ComposeKindDone, "me", true},
		{"compose-done", ComposeKindFailed, "me", false},
		{"compose-failed", ComposeKindFailed, "me", true},
		{"compose-declined", ComposeKindDeclined, "me", true},
		{"compose-declined", ComposeKindFailed, "me", false},
		{"compose-ended", ComposeKindDone, "me", true},
		{"compose-ended", ComposeKindFailed, "me", true},
		{"compose-ended", ComposeKindDeclined, "me", true},
		{"compose-ended", ComposeKindDone, "somebody else", false},
	} {
		option, err := ParseEventOption(tc.trigger)
		if err != nil {
			t.Fatal(err)
		}
		trigger := Trigger{Owner: "me", Source: option.Source, Kind: option.Kind, State: TriggerStanding}
		if got := trigger.matches(ev(tc.kind, tc.owner)); got != tc.want {
			t.Fatalf("%s against %s of %s answered %v", tc.trigger, tc.kind, tc.owner, got)
		}
	}
	if _, err := ParseEventOption("compose-cancelled"); err == nil {
		t.Fatal("an unknown compose kind was accepted")
	}
}

// A compose trigger takes no terminal: the event is about a run, and a
// terminal named on it would filter every event out.
func TestAComposeTriggerRefusesATerminal(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	owner, _ := svc.Create("claude")
	_, err := svc.Events().Add(TriggerSpec{Owner: owner.ID, Event: "compose-ended", Task: "say so", Targets: []TriggerTarget{{Terminal: "term-1"}}, TargetsSet: true})
	if err == nil || !strings.Contains(err.Error(), "no terminal") {
		t.Fatalf("want the terminal refused, got %v", err)
	}
	trigger, err := svc.Events().Add(TriggerSpec{Owner: owner.ID, Event: "compose-ended", Task: "say so", Name: "compose watch"})
	if err != nil {
		t.Fatal(err)
	}
	if trigger.Source != EventCompose || trigger.Kind != ComposeKindEnded || len(trigger.Targets) != 0 {
		t.Fatalf("the trigger reads %+v", trigger)
	}
}

// Any or all asks about terminals, and a compose run has none: a mode posted
// for a compose trigger, a form that still showed the select, makes no
// barrier and no refusal, it is dropped.
func TestAComposeTriggerIgnoresTheMode(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	owner, _ := svc.Create("claude")
	trigger, err := svc.Events().Add(TriggerSpec{Owner: owner.ID, Event: "compose-ended", Task: "say so", All: true, AllSet: true})
	if err != nil {
		t.Fatalf("a compose trigger with a mode was refused: %v", err)
	}
	if trigger.All {
		t.Fatalf("a compose trigger became a barrier: %+v", trigger)
	}
}

// The end of an owned run rings the user as news of the owner's thread, done,
// failed and declined alike, once per run and for the owner alone.
func TestRecordComposeRingsTheOwnersThread(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	owner, _ := svc.Create("claude")
	news := make(chan string, 8)
	svc.SetHooks(func() {}, func(id string) { news <- id })
	for _, report := range []ComposeReport{
		{Owner: owner.ID, Run: "run-1", Project: "shop", Action: "Compose up", Exited: true},
		{Owner: owner.ID, Run: "run-2", Project: "shop", Action: "Compose up", Failed: true, Reason: "exit status 1"},
		{Owner: owner.ID, Run: "run-3", Project: "shop", Action: "Compose down", Declined: true, Reason: "declined by the user"},
	} {
		if svc.RecordCompose(report) == "" {
			t.Fatalf("no note for %s", report.Run)
		}
		select {
		case id := <-news:
			if id != owner.ID {
				t.Fatalf("%s rang %q, want the owner", report.Run, id)
			}
		default:
			t.Fatalf("%s rang nobody", report.Run)
		}
	}
	if len(news) != 0 {
		t.Fatal("a run rang twice")
	}
	svc.RecordCompose(ComposeReport{Owner: "44444444-4444-4444-8444-444444444444", Run: "run-4"})
	if len(news) != 0 {
		t.Fatal("a run of a gone assistant rang")
	}
}

// An end the user made themselves, a Deny or a Cancel of a parked run, is
// written into the thread and published like every other, and rings nobody:
// it is no news to the one who clicked.
func TestAComposeEndTheUserMadeRingsNobody(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	owner, _ := svc.Create("claude")
	trigger, err := svc.Events().Add(TriggerSpec{Owner: owner.ID, Event: "compose-declined", Task: "say so"})
	if err != nil {
		t.Fatal(err)
	}
	news := make(chan string, 2)
	svc.SetHooks(func() {}, func(id string) { news <- id })
	id := svc.RecordCompose(ComposeReport{Owner: owner.ID, Run: "run-1", Project: "shop", Action: "Compose down", Failed: true, Declined: true, Reason: "declined by the user", ByUser: true})
	if id == "" {
		t.Fatal("no note was written")
	}
	if len(news) != 0 {
		t.Fatal("the user's own decline rang")
	}
	if got, _ := svc.Events().Get(trigger.ID); len(got.Pending) != 1 || got.Pending[0].Kind != ComposeKindDeclined {
		t.Fatalf("the event did not fire: %+v", got.Pending)
	}
}
