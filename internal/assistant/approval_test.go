package assistant

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/markdown"
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
	if got := ProjectDeleted("shop", []string{"shop-a", "shop-b"}, false); got != "Project shop is deleted, its terminals stopped and its directory removed. Its worktree projects went with it: shop-a, shop-b." {
		t.Fatalf("the delete outcome reads %q", got)
	}
	if got := ProjectDeleted("shop", nil, true); got != "Project shop is being deleted, its terminals stopped and its containers going down first." {
		t.Fatalf("the running delete outcome reads %q", got)
	}
}

// What a forge or a repository wrote into an outcome note reads on the page
// as it was written: a tag shaped word does not vanish with the HTML the
// renderer drops and an underscore makes nothing bold, the same for a
// refusal's sentence.
func TestAForgeOutcomeReadsAsWrittenOnThePage(t *testing.T) {
	title, branch, path := "Fix <form> in __init__", "fix/<b>_x_", "src/<i>.go"
	shown := []string{"Fix &lt;form&gt; in __init__", "fix/&lt;b&gt;_x_"}
	for name, c := range map[string]struct {
		note string
		want []string
	}{
		"changed":   {ForgeChanged(ForgeChange{Forge: "GitLab", Address: "gitlab.example.com/a_b", Noun: "merge request", Ref: "!4", Title: title, Branch: branch, Project: "shop", Target: "main", URL: "https://gitlab.example.com/a/-/merge_requests/4"}).Note, shown},
		"published": {ForgePublished(ForgePublish{Forge: "GitLab", Noun: "merge request", Ref: "!4", Title: title, Branch: branch, Project: "shop", Notes: []ForgePublishedNote{{Path: path, Line: 3, URL: "https://x.test/n"}}}).Note, append(shown, "src/&lt;i&gt;.go:3")},
		"thread":    {ForgeThreadWritten(ForgeThread{Forge: "GitLab", Noun: "merge request", Ref: "!4", Title: title, Branch: branch, Project: "shop", Path: path, Line: 3, Replied: true}).Note, append(shown, "src/&lt;i&gt;.go:3")},
		"retried":   {ForgeRetried(ForgeRetry{Forge: "GitLab", Pipeline: "pipeline #7", Branch: branch, Project: "shop", Job: "__tests__ <e2e>"}).Note, []string{"fix/&lt;b&gt;_x_", "__tests__ &lt;e2e&gt;"}},
		"failed":    {render("approval_note.md.tmpl", approvalNoteData{Verdict: approval.Failed, Text: "GitLab refused to comment: <form> is __bad__"}), []string{"&lt;form&gt; is __bad__"}},
	} {
		page, err := markdown.RenderGFM(c.note)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range c.want {
			if !strings.Contains(page, want) {
				t.Errorf("the %s note %q reads %q on the page, without %q", name, c.note, page, want)
			}
		}
		if strings.Contains(page, "<strong>") || strings.Contains(page, "<em>") {
			t.Errorf("the %s note turns bold on the page: %q", name, page)
		}
	}
}

// The same outcome in the answer the CLI prints is plain text: a name an
// assistant reuses reads as it was written, with no backslash before a mark.
func TestAForgeOutcomeReadsAsWrittenInTheCLI(t *testing.T) {
	title, branch, project, path := "Fix <form> in __init__", "fix_login", "p17b_ui", "src/log_in.go"
	for name, c := range map[string]struct {
		said ForgeOutcome
		want string
	}{
		"changed":   {ForgeChanged(ForgeChange{Forge: "GitLab", Address: "gitlab.example.com/a_b", Noun: "merge request", Ref: "!4", Title: title, Branch: branch, Project: project, Target: "main", URL: "https://gitlab.example.com/a_b/-/merge_requests/4"}), `On GitLab gitlab.example.com/a_b the merge request !4 "Fix <form> in __init__" of fix_login into main in p17b_ui is open: https://gitlab.example.com/a_b/-/merge_requests/4.`},
		"published": {ForgePublished(ForgePublish{Forge: "GitLab", Address: "gitlab.example.com/a_b", Noun: "merge request", Ref: "!4", Title: title, Branch: branch, Project: project, Notes: []ForgePublishedNote{{Path: path, Line: 3, URL: "https://x.test/n_1"}, {Path: "__init__.py", Line: 9, URL: "https://x.test/n_2"}}}), "On GitLab gitlab.example.com/a_b 2 line comments of fix_login in p17b_ui are published on the merge request !4 \"Fix <form> in __init__\", each a review thread of its own:\n- src/log_in.go:3 https://x.test/n_1\n- __init__.py:9 https://x.test/n_2"},
		"thread":    {ForgeThreadWritten(ForgeThread{Forge: "GitLab", Address: "gitlab.example.com/a_b", Noun: "merge request", Ref: "!4", Title: title, Branch: branch, Project: project, Path: path, Line: 3, Replied: true}), `On GitLab gitlab.example.com/a_b the review thread on src/log_in.go:3 of the merge request !4 "Fix <form> in __init__" of fix_login in p17b_ui holds your reply.`},
		"retried":   {ForgeRetried(ForgeRetry{Forge: "GitLab", Address: "gitlab.example.com/a_b", Pipeline: "pipeline #7", Branch: branch, Project: project, Job: "__tests__ <e2e>"}), `On GitLab gitlab.example.com/a_b the job "__tests__ <e2e>" of pipeline #7 of fix_login in p17b_ui runs again.`},
	} {
		if c.said.Plain != c.want {
			t.Errorf("the %s answer reads %q, want %q", name, c.said.Plain, c.want)
		}
	}
}
