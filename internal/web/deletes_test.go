package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/coder"
)

// helperSession is the second coder session of the list fixture.
const helperSession = "33333333-3333-4333-8333-333333333333"

// newCoderDeletesFixture is the coder delete fixture with a second stored
// session named helper and the coder list route.
func newCoderDeletesFixture(t *testing.T) (*composeFixture, *storedSessions) {
	t.Helper()
	f, sessions := newCoderDeleteFixture(t)
	sessions.list = append(sessions.list, coder.Session{SessionID: helperSession, Name: "helper", CWD: f.dir})
	f.router.POST("/coders/delete", f.s.handleDeletes(f.s.coderDeleteKind(), "terminal", nil))
	return f, sessions
}

func (f *composeFixture) deleteCodersAs(t *testing.T, as string, ids ...string) map[string]any {
	t.Helper()
	return decodeJSON(t, f.post(t, "/coders/delete", url.Values{"terminal": ids}, true, as))
}

// Several coders are one approval: one question listing every coder with its
// project, one key whatever the order, and one note naming what went.
func TestSeveralCodersAreOneApproval(t *testing.T) {
	f, sessions := newCoderDeletesFixture(t)
	answer := f.deleteCodersAs(t, f.owner, storedSession, helperSession, storedSession)
	if answer["pending"] != true || answer["what"] != "Delete 2 coders" {
		t.Fatalf("the waiting delete answered %v", answer)
	}
	q := f.waitQuestion(t)
	want := []askpass.Detail{{Label: "Coder", Value: "worker · 22222222"}, {Label: "Project", Value: "shop"}, {Label: "Coder", Value: "helper · 33333333"}, {Label: "Project", Value: "shop"}}
	if q.Action != "Delete 2 coders" || !reflect.DeepEqual(q.Details, want) {
		t.Fatalf("the question reads %+v", q)
	}
	if again := f.deleteCodersAs(t, f.owner, helperSession, storedSession); again["error"] != "Delete 2 coders already waits for the user's approval." {
		t.Fatalf("the same coders in another order answered %v", again)
	}
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Done || !strings.Contains(note.Content, "Coder worker is deleted") || !strings.Contains(note.Content, "Coder helper is deleted") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	if len(sessions.List()) != 0 {
		t.Fatal("the approved delete left a session")
	}
}

// A coder that went another way while the approval waited fails alone: the
// others are deleted and the note names each end.
func TestAFailedTargetDoesNotStopTheOthers(t *testing.T) {
	f, sessions := newCoderDeletesFixture(t)
	f.deleteCodersAs(t, f.owner, storedSession, helperSession)
	q := f.waitQuestion(t)
	_ = sessions.DeleteSession(storedSession)
	f.s.coders[0].Invalidate()
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Failed || !strings.Contains(note.Content, "Could not delete coder worker: the coder no longer exists.") ||
		!strings.Contains(note.Content, "Coder helper is deleted") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	if len(sessions.List()) != 0 {
		t.Fatal("the failure stopped the other delete")
	}
}

// A name nothing answers to refuses the whole request, and with the question
// off every coder is deleted at once and the answer names each outcome.
func TestSeveralCodersWithTheQuestionOff(t *testing.T) {
	f, sessions := newCoderDeletesFixture(t)
	if answer := f.deleteCodersAs(t, f.owner, storedSession, "gone"); answer["error"] != `No inactive coder "gone" was found.` {
		t.Fatalf("an unknown coder answered %v", answer)
	}
	f.postApprovals(t, url.Values{"approval-" + approvalCoderDelete: {"0"}})
	answer := f.deleteCodersAs(t, f.owner, storedSession, helperSession)
	results, _ := answer["results"].([]any)
	if len(results) != 2 || len(f.broker.Questions()) != 0 || len(sessions.List()) != 0 {
		t.Fatalf("the delete answered %v", answer)
	}
	for i, name := range []string{"coder worker", "coder helper"} {
		result := results[i].(map[string]any)
		if result["name"] != name || !strings.Contains(result["deleted"].(string), "is deleted") {
			t.Fatalf("result %d reads %v", i, result)
		}
	}
}

// Who asks is checked before what is named: a local call without a live
// assistant is refused even where a target is unknown.
func TestALocalCallWithoutAnAssistantIsRefusedFirst(t *testing.T) {
	f, sessions := newCoderDeletesFixture(t)
	for _, as := range []string{"", "stale"} {
		rec := f.post(t, "/coders/delete", url.Values{"terminal": {storedSession, "gone"}}, true, as)
		if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != assistantCallerRefusal {
			t.Fatalf("a local call as %q answered %d %s", as, rec.Code, rec.Body.String())
		}
	}
	if len(sessions.List()) != 2 || len(f.broker.Questions()) != 0 {
		t.Fatal("a refused call deleted or asked something")
	}
}

// Several projects travel on the one delete route, and the approval touches
// each of them: a user deleting either one declines it.
func TestSeveralProjectsAreOneApproval(t *testing.T) {
	f := newDeleteFixture(t)
	cart := filepath.Join(filepath.Dir(f.dir), "cart")
	if err := os.MkdirAll(cart, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := f.post(t, "/projects/delete", url.Values{"project": {f.project, "cart"}}, true, f.owner)
	if answer := decodeJSON(t, rec); answer["pending"] != true || answer["what"] != "Delete 2 projects" {
		t.Fatalf("the waiting delete answered %d %v", rec.Code, answer)
	}
	f.waitQuestion(t)
	if rec := f.post(t, "/projects/delete", url.Values{"project": {"cart"}}, false, ""); rec.Code != http.StatusOK {
		t.Fatalf("the browser's delete answered %d: %s", rec.Code, rec.Body.String())
	}
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Declined || !strings.Contains(note.Content, "the project was deleted") || !exists(f.dir) {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	f.waitNoQuestion(t)
}

// Names repeat, every untitled assistant reads the same, so the dialog names
// each assistant with its short id, the one asking included.
func TestTheDialogTellsSameNamedAssistantsApart(t *testing.T) {
	f := newComposeFixture(t)
	f.router.POST("/assistants/delete", f.s.handleDeletes(f.s.assistantDeleteKind(), "assistant", nil))
	var ids []string
	for range 2 {
		a, err := f.s.assistants.Create("claude")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	name := f.s.assistantName(ids[0])
	if name != f.s.assistantName(ids[1]) {
		t.Fatalf("the fixture's assistants are named apart: %q", name)
	}
	rec := f.post(t, "/assistants/delete", url.Values{"assistant": ids}, true, f.owner)
	if answer := decodeJSON(t, rec); answer["pending"] != true || answer["what"] != "Delete 2 assistants" {
		t.Fatalf("the waiting delete answered %d %v", rec.Code, answer)
	}
	q := f.waitQuestion(t)
	want := []askpass.Detail{{Label: "Assistant", Value: name + " · " + coder.ShortID(ids[0])}, {Label: "Assistant", Value: name + " · " + coder.ShortID(ids[1])}}
	if !reflect.DeepEqual(q.Details, want) || q.Assistant != f.s.assistantLabel(f.owner)+" · "+coder.ShortID(f.owner) {
		t.Fatalf("the question reads %+v", q)
	}
}

// A worktree project its main took along in the same delete is deleted, in
// either order, and the note says so instead of calling it gone.
func TestAWorktreeTheMainTookAlongIsDeleted(t *testing.T) {
	for _, order := range [][]string{{"shop", "shop-feature"}, {"shop-feature", "shop"}} {
		t.Run(strings.Join(order, ","), func(t *testing.T) {
			f := newDeleteFixture(t)
			worktree := f.addWorktreeProject(t)
			f.post(t, "/projects/delete", url.Values{"project": order}, true, f.owner)
			f.decide(t, f.waitQuestion(t), true, false)
			note := f.waitApprovalNotes(t, f.owner, 1)[0]
			if note.Note.Verdict != approval.Done || exists(f.dir) || exists(worktree) {
				t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
			}
			if order[0] == "shop" && !strings.Contains(note.Content, "Project shop-feature went with project shop.") {
				t.Fatalf("the note does not name the worktree taken along:\n%s", note.Content)
			}
		})
	}
}
