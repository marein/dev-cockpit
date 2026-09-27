package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// storedSession is the one coder session the delete fixture holds, stored
// and not running, in the fixture's project.
const storedSession = "22222222-2222-4222-8222-222222222222"

// storedCoder is a coder whose store holds the given sessions and forgets one
// on a delete.
type storedCoder struct {
	coder.Coder
	sessions *storedSessions
}

func (storedCoder) ID() string                                   { return "claude" }
func (c storedCoder) SessionRepository() coder.SessionRepository { return c.sessions }

type storedSessions struct {
	coder.SessionRepository
	mu   sync.Mutex
	list []coder.Session
}

func (r *storedSessions) List() []coder.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]coder.Session(nil), r.list...)
}

func (r *storedSessions) DeleteSession(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, session := range r.list {
		if session.SessionID == id {
			r.list = append(r.list[:i], r.list[i+1:]...)
		}
	}
	return nil
}

// newCoderDeleteFixture is the delete fixture with one stored coder session
// named worker in the project, and the coder delete route.
func newCoderDeleteFixture(t *testing.T) (*composeFixture, *storedSessions) {
	t.Helper()
	f := newDeleteFixture(t)
	sessions := &storedSessions{list: []coder.Session{{SessionID: storedSession, Name: "worker", CWD: f.dir}}}
	f.s.coders = []*coder.Manager{coder.NewManager(config.Config{}, tmux.New(), storedCoder{sessions: sessions}, f.s.projects)}
	f.router.POST("/coders/:id/delete", f.s.handleCoderDelete)
	return f, sessions
}

// post sends a form to the path, over the local socket as the given assistant
// where local is set, empty for a call that names none, else as the browser.
func (f *composeFixture) post(t *testing.T, path string, form url.Values, local bool, as string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if local {
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
	}
	if as != "" {
		req.Header.Set(localapi.AssistantHeader, as)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *composeFixture) deleteCoderAs(t *testing.T, as string) *httptest.ResponseRecorder {
	return f.post(t, "/coders/"+storedSession+"/delete", url.Values{}, true, as)
}

func stored(sessions *storedSessions) bool {
	return len(sessions.List()) == 1
}

// An assistant's coder delete waits for the user: the answer says so at once
// and deletes nothing, the question names the coder and its project, and an
// approval deletes the session and writes what went as a note.
func TestAnAssistantsCoderDeleteWaitsForTheApproval(t *testing.T) {
	f, sessions := newCoderDeleteFixture(t)
	rec := f.deleteCoderAs(t, f.owner)
	answer := decodeJSON(t, rec)
	if rec.Code != http.StatusOK || answer["pending"] != true || answer["what"] != "Delete coder worker" {
		t.Fatalf("the waiting delete answered %d %v", rec.Code, answer)
	}
	if !stored(sessions) {
		t.Fatal("a delete that waits removed the session")
	}
	q := f.waitQuestion(t)
	want := []askpass.Detail{{Label: "Coder", Value: "worker"}, {Label: "Project", Value: "shop"}}
	if q.Kind != askpass.KindApproval || q.Action != "Delete coder worker" || !reflect.DeepEqual(q.Details, want) {
		t.Fatalf("the question reads %+v", q)
	}
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Done || note.Note.Name != "Delete coder worker" || !strings.Contains(note.Content, "Coder worker is deleted") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	if stored(sessions) {
		t.Fatal("the approved delete left the session")
	}
	f.waitNoQuestion(t)
}

// A denial keeps the coder and says so in the thread.
func TestADeniedCoderDeleteKeepsTheCoder(t *testing.T) {
	f, sessions := newCoderDeleteFixture(t)
	f.deleteCoderAs(t, f.owner)
	f.decide(t, f.waitQuestion(t), false, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Declined || note.Note.Headline != "Declined: Delete coder worker" {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	if !stored(sessions) {
		t.Fatal("a declined delete removed the session")
	}
}

// An approval that comes after the coder went another way deletes nothing
// and says why.
func TestAnApprovedDeleteOfACoderThatIsGoneFails(t *testing.T) {
	f, sessions := newCoderDeleteFixture(t)
	f.deleteCoderAs(t, f.owner)
	q := f.waitQuestion(t)
	_ = sessions.DeleteSession(storedSession)
	f.s.coders[0].Invalidate()
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Failed || !strings.Contains(note.Content, "the coder no longer exists") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
}

// With the Coder delete approval off the delete runs at once and asks nobody,
// and the browser never asks whatever the switch says.
func TestTheCoderDeleteApprovalOffDeletesAtOnce(t *testing.T) {
	f, sessions := newCoderDeleteFixture(t)
	f.postApprovals(t, url.Values{"approval-" + approvalCoderDelete: {"0"}})
	if !ApprovalAsks(f.s.settings, approvalProjectDelete) {
		t.Fatal("the coder switch turned the project delete question off")
	}
	rec := f.deleteCoderAs(t, f.owner)
	answer := decodeJSON(t, rec)
	if rec.Code != http.StatusOK || answer["pending"] == true || answer["name"] != "worker" || len(f.broker.Questions()) != 0 {
		t.Fatalf("the delete asked with the approval off: %d %v", rec.Code, answer)
	}
	if stored(sessions) {
		t.Fatal("the delete left the session")
	}

	f, sessions = newCoderDeleteFixture(t)
	rec = f.post(t, "/coders/"+storedSession+"/delete", url.Values{}, false, "")
	if rec.Code != http.StatusOK || decodeJSON(t, rec)["pending"] == true || stored(sessions) {
		t.Fatalf("the browser's delete answered %d: %s", rec.Code, rec.Body.String())
	}
}

// A local call that names no live assistant is refused rather than read as
// the user, so leaving --as off is no way around the question.
func TestALocalCoderDeleteWithoutAnAssistantIsRefused(t *testing.T) {
	f, sessions := newCoderDeleteFixture(t)
	for _, id := range []string{"", "gone"} {
		rec := f.deleteCoderAs(t, id)
		if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != assistantCallerRefusal {
			t.Fatalf("a local delete as %q answered %d: %s", id, rec.Code, rec.Body.String())
		}
	}
	if !stored(sessions) || len(f.broker.Questions()) != 0 {
		t.Fatal("a refused delete moved something")
	}
}

// newAssistantDeleteFixture is the compose fixture with the assistant route
// and a second assistant, Helper, to delete.
func newAssistantDeleteFixture(t *testing.T) (*composeFixture, string) {
	t.Helper()
	f := newComposeFixture(t)
	f.router.POST("/assistants/:id", f.s.handleAssistantAction)
	other, err := f.s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.assistants.Rename(other.ID, "Helper"); err != nil {
		t.Fatal(err)
	}
	return f, other.ID
}

func (f *composeFixture) deleteAssistantAs(t *testing.T, id, as string) *httptest.ResponseRecorder {
	return f.post(t, "/assistants/"+id, url.Values{"form": {"delete"}}, true, as)
}

func (f *composeFixture) assistantExists(id string) bool {
	_, err := f.s.assistants.Get(id)
	return err == nil
}

// An assistant's delete of another one waits for the user: the question
// names the assistant, and an approval deletes it and writes the note into
// the thread of the one that asked.
func TestAnAssistantsAssistantDeleteWaitsForTheApproval(t *testing.T) {
	f, other := newAssistantDeleteFixture(t)
	rec := f.deleteAssistantAs(t, other, f.owner)
	answer := decodeJSON(t, rec)
	if rec.Code != http.StatusOK || answer["pending"] != true || answer["what"] != "Delete assistant Helper" {
		t.Fatalf("the waiting delete answered %d %v", rec.Code, answer)
	}
	if !f.assistantExists(other) {
		t.Fatal("a delete that waits removed the assistant")
	}
	q := f.waitQuestion(t)
	if q.Action != "Delete assistant Helper" || !reflect.DeepEqual(q.Details, []askpass.Detail{{Label: "Assistant", Value: "Helper"}}) {
		t.Fatalf("the question reads %+v", q)
	}
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Done || !strings.Contains(note.Content, "Assistant Helper is deleted") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	if f.assistantExists(other) {
		t.Fatal("the approved delete left the assistant")
	}
	f.waitNoQuestion(t)
}

// An assistant deleting itself asks like any other delete, and the approval
// takes it with its thread: the note has nowhere to land and is dropped.
func TestAnApprovedSelfDeleteRemovesTheAssistant(t *testing.T) {
	f, _ := newAssistantDeleteFixture(t)
	if rec := f.deleteAssistantAs(t, f.owner, f.owner); decodeJSON(t, rec)["pending"] != true {
		t.Fatalf("the self delete did not wait: %s", rec.Body.String())
	}
	f.decide(t, f.waitQuestion(t), true, false)
	deadline := time.Now().Add(5 * time.Second)
	for f.assistantExists(f.owner) {
		if time.Now().After(deadline) {
			t.Fatal("the approved self delete left the assistant")
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.waitNoQuestion(t)
}

// With the Assistant delete approval off the delete runs at once, and a local
// call that names no live assistant is refused.
func TestTheAssistantDeleteApprovalOffDeletesAtOnce(t *testing.T) {
	f, other := newAssistantDeleteFixture(t)
	for _, as := range []string{"", "gone"} {
		if rec := f.deleteAssistantAs(t, other, as); rec.Code != http.StatusForbidden {
			t.Fatalf("a local delete as %q answered %d: %s", as, rec.Code, rec.Body.String())
		}
	}
	f.postApprovals(t, url.Values{"approval-" + approvalAssistantDelete: {"0"}})
	rec := f.deleteAssistantAs(t, other, f.owner)
	if rec.Code != http.StatusOK || decodeJSON(t, rec)["deleted"] != true || len(f.broker.Questions()) != 0 {
		t.Fatalf("the delete asked with the approval off: %d %s", rec.Code, rec.Body.String())
	}
	if f.assistantExists(other) {
		t.Fatal("the delete left the assistant")
	}
}

// A deleted project ends every approval that belongs to it, a compose
// command, its own delete and the delete of a coder in it, each owner reading
// why, while an approval that belongs to no project keeps standing.
func TestADeletedProjectDeclinesItsApprovals(t *testing.T) {
	f, _ := newCoderDeleteFixture(t)
	f.router.POST("/assistants/:id", f.s.handleAssistantAction)
	other, err := f.s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	if code, answer := f.compose(t, "down-volumes", true); code != http.StatusOK || answer["pending"] != true {
		t.Fatalf("the compose did not wait: %d %v", code, answer)
	}
	for _, rec := range []*httptest.ResponseRecorder{f.deleteAs(t, f.owner), f.deleteCoderAs(t, f.owner), f.deleteAssistantAs(t, other.ID, f.owner)} {
		if decodeJSON(t, rec)["pending"] != true {
			t.Fatalf("the delete did not wait: %s", rec.Body.String())
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(f.broker.Questions()) != 4 {
		if time.Now().After(deadline) {
			t.Fatal("the questions never stood")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rec := f.post(t, "/projects/delete", url.Values{"project": {f.project}}, false, ""); rec.Code != http.StatusOK {
		t.Fatalf("the delete answered %d: %s", rec.Code, rec.Body.String())
	}
	if exists(f.dir) {
		t.Fatal("the delete left the directory")
	}
	notes := f.waitApprovalNotes(t, f.owner, 3)
	for _, note := range notes {
		if note.Note.Verdict != approval.Declined || !strings.Contains(note.Content, "the project was deleted") {
			t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
		}
	}
	q := f.waitQuestion(t)
	if q.Action != "Delete assistant "+f.s.assistantName(other.ID) {
		t.Fatalf("the question left standing reads %+v", q)
	}
}

// A deletion that cascades into a linked worktree project ends the approvals
// of the worktree project too, and its owner reads why.
func TestADeletedProjectDeclinesTheApprovalsOfItsWorktrees(t *testing.T) {
	f := newDeleteFixture(t)
	root := filepath.Dir(f.dir)
	worktree := filepath.Join(root, "shop-feature")
	gitDir := filepath.Join(f.dir, ".git", "worktrees", "shop-feature")
	for path, content := range map[string]string{
		filepath.Join(f.dir, ".git", "HEAD"): "ref: refs/heads/master\n",
		filepath.Join(gitDir, "HEAD"):        "ref: refs/heads/feature\n",
		filepath.Join(gitDir, "commondir"):   "../..\n",
		filepath.Join(gitDir, "gitdir"):      filepath.Join(worktree, ".git") + "\n",
		filepath.Join(worktree, ".git"):      "gitdir: " + gitDir + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	main, err := f.s.projects.FindByName(f.project)
	if err != nil || len(main.GitWorktrees) != 1 || main.GitWorktrees[0].Project != "shop-feature" {
		t.Fatalf("the main project reads %+v, %v", main, err)
	}
	if rec := f.post(t, "/projects/delete", url.Values{"project": {"shop-feature"}}, true, f.owner); decodeJSON(t, rec)["pending"] != true {
		t.Fatalf("the worktree delete did not wait: %s", rec.Body.String())
	}
	f.waitQuestion(t)
	if rec := f.post(t, "/projects/delete", url.Values{"project": {f.project}}, false, ""); rec.Code != http.StatusOK {
		t.Fatalf("the delete answered %d: %s", rec.Code, rec.Body.String())
	}
	if exists(f.dir) || exists(worktree) {
		t.Fatal("the delete left a directory")
	}
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Declined || note.Note.Name != "Delete project shop-feature" || !strings.Contains(note.Content, "the project was deleted") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	f.waitNoQuestion(t)
}
