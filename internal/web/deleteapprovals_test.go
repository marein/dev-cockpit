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
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/notify"
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
	want := []askpass.Detail{
		{As: askpass.AsWarning, Value: coderDeleteWarning},
		{Label: "Coder", Value: "worker"},
		{Label: "ID", Value: "22222222", As: askpass.AsMono},
		{Label: "Project", Value: "shop"},
	}
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
	want := []askpass.Detail{
		{As: askpass.AsWarning, Value: assistantDeleteWarning},
		{Label: "Assistant", Value: "Helper"},
		{Label: "ID", Value: coder.ShortID(other), As: askpass.AsMono},
	}
	if q.Action != "Delete assistant Helper" || !reflect.DeepEqual(q.Details, want) {
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
	worktree := f.addWorktreeProject(t)
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

// addWorktreeProject makes shop-feature a linked worktree project of the
// fixture's project and answers its directory.
func (f *composeFixture) addWorktreeProject(t *testing.T) string {
	t.Helper()
	worktree := filepath.Join(filepath.Dir(f.dir), "shop-feature")
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
	return worktree
}

// deleteAs posts the delete of the fixture's project over the local socket,
// as the given assistant, empty for a call that names none.
func (f *composeFixture) deleteAs(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/projects/delete", strings.NewReader(url.Values{"project": {f.project}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
	if id != "" {
		req.Header.Set(localapi.AssistantHeader, id)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// An assistant's delete waits for the user: the answer says so at once and
// deletes nothing, the question stands with the project alone and is news,
// and an approval deletes the project and writes the outcome as a note.
func TestAnAssistantsProjectDeleteWaitsForTheApproval(t *testing.T) {
	f := newDeleteFixture(t)
	rec := f.deleteAs(t, f.owner)
	answer := decodeJSON(t, rec)
	if rec.Code != http.StatusOK || answer["pending"] != true || answer["what"] != "Delete project shop" {
		t.Fatalf("the waiting delete answered %d %v", rec.Code, answer)
	}
	if !exists(f.dir) {
		t.Fatal("a delete that waits removed the directory")
	}
	q := f.waitQuestion(t)
	id, _ := askpass.ApprovalID(q.Key)
	want := []askpass.Detail{
		{As: askpass.AsWarning, Value: projectDeleteWarning},
		{Label: "Project", Value: "shop", As: askpass.AsMono},
		{Label: "Path", Value: f.dir, As: askpass.AsMono},
	}
	if q.Kind != askpass.KindApproval || q.Action != "Delete project shop" || q.Icon != "trash" || q.Tone != "danger" || !reflect.DeepEqual(q.Details, want) {
		t.Fatalf("the question reads %+v", q)
	}
	f.waitNews(t, notify.ApprovalTarget(id))
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Done || note.Note.Name != "Delete project shop" || !strings.Contains(note.Content, "Project shop is deleted") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	if exists(f.dir) {
		t.Fatal("the approved delete left the directory")
	}
	f.waitNoQuestion(t)
}

// A denial keeps the project and says so in the thread; so does a question
// that ends before anybody decided, which is what the expiry does.
func TestADeniedOrUnansweredProjectDeleteKeepsTheProject(t *testing.T) {
	f := newDeleteFixture(t)
	f.deleteAs(t, f.owner)
	f.decide(t, f.waitQuestion(t), false, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Declined || note.Note.Headline != "Declined: Delete project shop" || !strings.Contains(note.Content, "declined by the user") {
		t.Fatalf("the denied delete's note reads %+v\n%s", note.Note, note.Content)
	}
	other, err := f.s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	f.deleteAs(t, other.ID)
	q := f.waitQuestion(t)
	f.broker.Find(q.Key).End()
	note = f.waitApprovalNotes(t, other.ID, 1)[0]
	if note.Note.Verdict != approval.Declined || !strings.Contains(note.Content, "the approval expired unanswered") {
		t.Fatalf("the unanswered delete's note reads %+v\n%s", note.Note, note.Content)
	}
	f.waitNoQuestion(t)
	if !exists(f.dir) {
		t.Fatal("a declined delete removed the directory")
	}
}

// An approval that comes after the project went another way deletes nothing
// and says why.
func TestAnApprovedDeleteOfAProjectThatIsGoneFails(t *testing.T) {
	f := newDeleteFixture(t)
	f.deleteAs(t, f.owner)
	q := f.waitQuestion(t)
	if err := os.RemoveAll(f.dir); err != nil {
		t.Fatal(err)
	}
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Failed || !strings.Contains(note.Content, "the project no longer exists") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
}

// An approval that finds another delete of the project running claims
// nothing, and the note says that delete's end is not known here rather
// than calling the project gone.
func TestAnApprovedDeleteBehindARunningOneSaysSo(t *testing.T) {
	f := newDeleteFixture(t)
	f.deleteAs(t, f.owner)
	q := f.waitQuestion(t)
	if !f.s.deletes.start("shop", f.dir) {
		t.Fatal("the running delete could not be claimed")
	}
	defer f.s.deletes.finish("shop", "")
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Failed || !strings.Contains(note.Content, "a delete of the project is already running") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	if !exists(f.dir) {
		t.Fatal("the approval deleted behind the running delete")
	}
}

// With the Project delete approval off the delete runs at once and asks
// nobody; the compose approval is a switch of its own and moves nothing here.
func TestTheProjectDeleteApprovalOffDeletesAtOnce(t *testing.T) {
	f := newDeleteFixture(t)
	setApprovalAsks(f.s.settings, approvalComposeActions, false)
	if !ApprovalAsks(f.s.settings, approvalProjectDelete) {
		t.Fatal("the compose switch turned the project delete question off")
	}
	f.postApprovals(t, url.Values{"approval-" + approvalProjectDelete: {"0"}})
	rec := f.deleteAs(t, f.owner)
	answer := decodeJSON(t, rec)
	if rec.Code != http.StatusOK || answer["pending"] == true || len(f.broker.Questions()) != 0 || answer["name"] != "shop" || answer["deleting"] != nil {
		t.Fatalf("the delete asked with the approval off: %d %v", rec.Code, answer)
	}
	if exists(f.dir) {
		t.Fatal("the delete left the directory")
	}
}

// A local call that names no live assistant is refused rather than read as
// the user, so leaving --as off is no way around the question.
func TestALocalProjectDeleteWithoutAnAssistantIsRefused(t *testing.T) {
	f := newDeleteFixture(t)
	for _, id := range []string{"", "gone"} {
		rec := f.deleteAs(t, id)
		if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != assistantCallerRefusal {
			t.Fatalf("a local delete as %q answered %d: %s", id, rec.Code, rec.Body.String())
		}
	}
	if !exists(f.dir) || len(f.broker.Questions()) != 0 {
		t.Fatal("a refused delete moved something")
	}
}

// One project waits for one delete, whoever asks for it.
func TestASecondDeleteOfAWaitingProjectIsRefused(t *testing.T) {
	f := newDeleteFixture(t)
	f.deleteAs(t, f.owner)
	f.waitQuestion(t)
	other, err := f.s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	rec := f.deleteAs(t, other.ID)
	if rec.Code != http.StatusConflict || !strings.Contains(decodeJSON(t, rec)["error"].(string), "already waits for the user's approval") {
		t.Fatalf("the second delete answered %d: %s", rec.Code, rec.Body.String())
	}
	if len(f.broker.Questions()) != 1 {
		t.Fatalf("the refusal put up a question: %+v", f.broker.Questions())
	}
}

// An approved delete of a project that runs containers waits for the
// deletion's real end: a compose down that fails is the note's failure and
// the project stays.
func TestAnApprovedDeleteReportsAFailingComposeDown(t *testing.T) {
	f := newDeleteFixture(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\necho no daemon\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.s.docker.SetStateForTest(docker.State{Available: true, Host: "tcp://fake:1", Containers: []docker.Container{
		{Name: "web", Service: "web", Project: "shop", State: "running", WorkingDir: f.dir},
	}})
	f.deleteAs(t, f.owner)
	f.decide(t, f.waitQuestion(t), true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Failed || !strings.Contains(note.Content, "Compose down in") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	if !exists(f.dir) {
		t.Fatal("the failed down removed the directory")
	}
}

// The dialog warns with the owner's own delete note where the project calls
// for it: the containers the delete brings down, volumes included, and a
// worktree elsewhere that loses its repository. Several projects carry it
// under the project's name.
func TestAProjectDeleteWarnsWhatItsDeleteTakes(t *testing.T) {
	f := newDeleteFixture(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.s.docker.SetStateForTest(docker.State{Available: true, Host: "tcp://fake:1", Containers: []docker.Container{
		{Name: "web", Service: "web", Project: "shop", State: "running", WorkingDir: f.dir},
	}})
	elsewhere := filepath.Join(t.TempDir(), "spike")
	gitDir := filepath.Join(f.dir, ".git", "worktrees", "spike")
	for path, content := range map[string]string{
		filepath.Join(f.dir, ".git", "HEAD"): "ref: refs/heads/master\n",
		filepath.Join(gitDir, "HEAD"):        "ref: refs/heads/spike\n",
		filepath.Join(gitDir, "commondir"):   "../..\n",
		filepath.Join(gitDir, "gitdir"):      filepath.Join(elsewhere, ".git") + "\n",
		filepath.Join(elsewhere, ".git"):     "gitdir: " + gitDir + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	note := "Its directory goes, and its containers are brought down with compose first, volumes included. The worktree at " + elsewhere + " loses its repository."
	f.deleteAs(t, f.owner)
	q := f.waitQuestion(t)
	if len(q.Details) < 2 || !reflect.DeepEqual(q.Details[:2], []askpass.Detail{{As: askpass.AsWarning, Value: projectDeleteWarning}, {As: askpass.AsWarning, Value: note}}) {
		t.Fatalf("the warnings read %+v", q.Details)
	}
	f.decide(t, q, false, false)
	f.waitNoQuestion(t)
	cart := filepath.Join(filepath.Dir(f.dir), "cart")
	if err := os.MkdirAll(cart, 0o755); err != nil {
		t.Fatal(err)
	}
	f.post(t, "/projects/delete", url.Values{"project": {f.project, "cart"}}, true, f.owner)
	if q := f.waitQuestion(t); !reflect.DeepEqual(q.Details, []askpass.Detail{
		{As: askpass.AsWarning, Value: projectDeleteWarning},
		{As: askpass.AsWarning, Value: "Project shop: " + note},
		{Label: "Projects", Value: f.project, As: askpass.AsItem, Icon: "folder"},
		{Label: "Projects", Value: "cart", As: askpass.AsItem, Icon: "folder"},
	}) {
		t.Fatalf("the rows read %+v", q.Details)
	}
}
