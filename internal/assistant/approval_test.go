package assistant

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/approval"
)

// Every end of an approval is one note, whatever the kind: the verdict and
// the action in the headline, the run's outcome or the reason in the text.
// Only news rings: an approval that went as the user decided is none.
func TestAnApprovalEndIsOneNote(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	owner, _ := svc.Create("claude")
	news := make(chan string, 8)
	svc.SetHooks(func() {}, func(id string) { news <- id })
	for _, tc := range []struct {
		outcome  approval.Outcome
		headline string
		text     string
		rings    bool
	}{
		{approval.Outcome{Verdict: approval.Done, Text: "Project shop is deleted.", Quiet: true}, "Approved: Delete project shop", "Project shop is deleted.", false},
		{approval.Outcome{Verdict: approval.Failed, Text: "the directory is busy"}, "Failed: Delete project shop", "Approved, and it failed: the directory is busy", true},
		{approval.Outcome{Verdict: approval.Declined, Text: "declined by the user", Quiet: true}, "Declined: Delete project shop", "Not done, declined by the user.", false},
		{approval.Outcome{Verdict: approval.Declined, Text: "the approval expired unanswered"}, "Declined: Delete project shop", "Not done, the approval expired unanswered.", true},
	} {
		id := svc.RecordApproval(ApprovalReport{Owner: owner.ID, What: "Delete project shop", Outcome: tc.outcome})
		if id == "" {
			t.Fatalf("no note for %+v", tc.outcome)
		}
		fresh, _ := svc.Get(owner.ID)
		note := fresh.Messages[len(fresh.Messages)-1]
		if note.Note == nil || note.Note.Source != NoteApproval || note.Note.Headline != tc.headline || note.Note.Verdict != tc.outcome.Verdict || note.Note.Name != "Delete project shop" {
			t.Fatalf("the note reads %+v", note.Note)
		}
		if strings.TrimSpace(note.Content) != tc.text {
			t.Fatalf("the note says %q, want %q", note.Content, tc.text)
		}
		if rang := len(news) > 0; rang != tc.rings {
			t.Fatalf("%s rang %v, want %v", tc.headline, rang, tc.rings)
		}
		for len(news) > 0 {
			<-news
		}
	}
	if svc.RecordApproval(ApprovalReport{Owner: "44444444-4444-4444-8444-444444444444", What: "x"}) != "" {
		t.Fatal("a note was written for a gone assistant")
	}
}

// A kind's event is published for the verdicts it names and for no other:
// the compose kind publishes declined and failed, so a trigger on
// compose-ended hears a command that never ran, leaves done to the run's own
// end, and a report without an event publishes nothing.
func TestAnApprovalPublishesItsKindsEvent(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{})
	owner, _ := svc.Create("claude")
	trigger, err := svc.Events().Add(TriggerSpec{Owner: owner.ID, Event: "compose-ended", Task: "say so"})
	if err != nil {
		t.Fatal(err)
	}
	compose := func(verdict, text string) ApprovalReport {
		return ApprovalReport{Owner: owner.ID, What: "Compose down in shop", Event: ComposeApprovalEvent, Outcome: approval.Outcome{Verdict: verdict, Text: text}}
	}
	svc.RecordApproval(compose(approval.Declined, "declined by the user"))
	svc.RecordApproval(compose(approval.Failed, "no reachable Docker host"))
	svc.RecordApproval(compose(approval.Done, "run r started"))
	svc.RecordApproval(ApprovalReport{Owner: owner.ID, What: "Delete project shop", Outcome: approval.Outcome{Verdict: approval.Declined, Text: "declined by the user"}})
	got, _ := svc.Events().Get(trigger.ID)
	if len(got.Pending) != 2 || got.Pending[0].Kind != ComposeKindDeclined || got.Pending[1].Kind != ComposeKindFailed {
		t.Fatalf("the events read %+v", got.Pending)
	}
	if got.Pending[0].Headline != "Declined: Compose down in shop" || got.Pending[0].Target != "" {
		t.Fatalf("the declined event reads %+v", got.Pending[0])
	}
}

// The outcomes a kind writes come out of their templates.
func TestTheKindOutcomesRead(t *testing.T) {
	if got := ComposeStarted("r1", "Down", "shop", "api"); !strings.HasPrefix(got, "Run r1 started: Down on ") || !strings.Contains(got, "compose-show") {
		t.Fatalf("the compose outcome reads %q", got)
	}
	if got := ProjectDeleted("shop", []string{"shop-a", "shop-b"}); got != "Project shop is deleted, its terminals stopped and its directory removed. Its worktree projects went with it: shop-a, shop-b." {
		t.Fatalf("the delete outcome reads %q", got)
	}
}
