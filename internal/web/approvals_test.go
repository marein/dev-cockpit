package web

import (
	"context"
	"encoding/json"
	"log"
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
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/restore"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/tmux"
)

var composeApprovalField = "approval-" + approvalComposeActions

// Absent asks, off asks nobody, and switching it back on removes the key
// rather than storing a copy of the default.
func TestApprovalAsksReadsAndStores(t *testing.T) {
	if !ApprovalAsks(nil, approvalComposeActions) {
		t.Fatal("no store did not ask")
	}
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	if !ApprovalAsks(store, approvalComposeActions) {
		t.Fatal("an install that never saved it does not ask")
	}
	setApprovalAsks(store, approvalComposeActions, false)
	if ApprovalAsks(store, approvalComposeActions) || store.Get("assistant-approval-compose-actions") != "off" {
		t.Fatal("off was not stored under assistant-approval-compose-actions")
	}
	setApprovalAsks(store, approvalComposeActions, true)
	if _, ok := store.Lookup("assistant-approval-compose-actions"); ok || !ApprovalAsks(store, approvalComposeActions) {
		t.Fatal("on left the key behind")
	}
}

// Each approval is one entry of the kinds the tab renders and the save reads,
// stored on its own key.
func TestTheApprovalsAreKinds(t *testing.T) {
	want := map[string]string{
		approvalComposeActions:  "Compose actions approval",
		approvalProjectDelete:   "Project delete approval",
		approvalCoderDelete:     "Coder delete approval",
		approvalAssistantDelete: "Assistant delete approval",
	}
	for _, kind := range approvalKinds {
		if want[kind.ID] != kind.Label {
			t.Fatalf("the kinds read %+v", approvalKinds)
		}
		delete(want, kind.ID)
	}
	if len(want) != 0 || approvalKey(approvalComposeActions) != "assistant-approval-compose-actions" || approvalKey(approvalProjectDelete) != "assistant-approval-project-delete" ||
		approvalKey(approvalCoderDelete) != "assistant-approval-coder-delete" || approvalKey(approvalAssistantDelete) != "assistant-approval-assistant-delete" {
		t.Fatalf("the kinds read %+v", approvalKinds)
	}
}

func (f *composeFixture) postApprovals(t *testing.T, form url.Values) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/settings/assistant/approvals", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("the save answered %d", rec.Code)
	}
}

// decide answers the standing question in the browser.
func (f *composeFixture) decide(t *testing.T, q askpass.Question, approve, remember bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"key": q.Key, "id": q.ID, "approve": approve, "remember": remember})
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(string(body))))
	if got := decodeJSON(t, rec); got["ok"] != true {
		t.Fatalf("the decision was refused: %v", got)
	}
}

// waitApprovalNotes waits until the owner's thread holds n approval notes and
// answers them.
func (f *composeFixture) waitApprovalNotes(t *testing.T, owner string, n int) []assistant.Message {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, _ := f.s.assistants.Get(owner)
		var notes []assistant.Message
		for _, m := range c.Messages {
			if m.IsNote() && m.Note.Source == assistant.NoteApproval {
				notes = append(notes, m)
			}
		}
		if len(notes) >= n {
			return notes
		}
		if time.Now().After(deadline) {
			t.Fatalf("the thread holds %d approval notes, want %d", len(notes), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitNews waits for the target's unread entry, which the broker's change
// hook writes a moment after the question stands.
func (f *composeFixture) waitNews(t *testing.T, target string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f.notifier.UnreadTargets()[target] {
		if time.Now().After(deadline) {
			t.Fatalf("the question is no news: %v", f.notifier.UnreadTargets())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitNoQuestion waits until no question stands and none is news any more.
func (f *composeFixture) waitNoQuestion(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		news := false
		for target := range f.notifier.UnreadTargets() {
			news = news || notify.IsApprovalTarget(target)
		}
		if len(f.broker.Questions()) == 0 && !news {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a question still stands")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *composeFixture) composeRuns() []docker.RunView {
	return f.s.docker.ComposeRunsForDir(f.dir)
}

// A confirm action the assistant starts runs nothing yet: the answer says it
// waits and carries no run, the question stands with the action and the
// project, it is news of its own, and approving it starts the run. The note
// names the run, whose own end then reports done.
func TestAConfirmActionOfAnAssistantWaitsForTheApproval(t *testing.T) {
	f := newComposeFixture(t)
	code, answer := f.compose(t, "down-volumes", true)
	if code != http.StatusOK || answer["pending"] != true || answer["run"] != nil || answer["what"] != "Compose down with volumes in shop" {
		t.Fatalf("the waiting action answered %d %v", code, answer)
	}
	if runs := f.composeRuns(); len(runs) != 0 {
		t.Fatalf("a run exists before the approval: %+v", runs)
	}
	q := f.waitQuestion(t)
	id, ok := askpass.ApprovalID(q.Key)
	if q.Kind != askpass.KindApproval || !ok || q.Assistant != assistant.Name || q.Action != "Compose down with volumes in shop" || q.Project != "" || q.Command != "docker compose down -v" || q.Dir != f.dir {
		t.Fatalf("the question reads %+v", q)
	}
	if want := []askpass.Detail{{Label: "Action", Value: "Compose down with volumes"}, {Label: "Stack", Value: "project root"}, {Label: "Project", Value: "shop"}}; !reflect.DeepEqual(q.Details, want) {
		t.Fatalf("the dialog's rows read %+v", q.Details)
	}
	f.waitNews(t, notify.ApprovalTarget(id))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/git/prompt", nil))
	if body := rec.Body.String(); !strings.Contains(body, `"kind":"approval"`) || !strings.Contains(body, `"target":"`+notify.ApprovalTarget(id)+`"`) || strings.Contains(body, `"url"`) {
		t.Fatalf("the dialog's list reads %s", body)
	}

	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	runs := f.composeRuns()
	if len(runs) != 1 || runs[0].Owner != f.owner {
		t.Fatalf("the approval started %+v", runs)
	}
	if note.Note.Verdict != approval.Done || note.Note.Headline != "Approved: Compose down with volumes in shop" || !strings.Contains(note.Content, "Run "+runs[0].ID+" started") {
		t.Fatalf("the approval's note reads %+v\n%s", note.Note, note.Content)
	}
	f.waitNoteOf(t, runs[0].ID)
	f.waitNoQuestion(t)
	if !ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("an approval without the box turned the approval off")
	}
}

// A denial runs nothing and says so in the thread; so does a question that
// ends before anybody decided, which is what the timeout does. Both fire the
// compose-declined event a trigger listens to; only the one nobody decided
// rings.
func TestADeniedOrUnansweredComposeRunsNothing(t *testing.T) {
	f := newComposeFixture(t)
	trigger, err := f.s.assistants.Events().Add(assistant.TriggerSpec{Owner: f.owner, Event: "compose-declined", Task: "say so"})
	if err != nil {
		t.Fatal(err)
	}
	rang := make(chan string, 8)
	f.s.assistants.SetHooks(func() {}, func(id string) { rang <- id })

	f.compose(t, "down-volumes", true)
	f.decide(t, f.waitQuestion(t), false, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Declined || note.Note.Headline != "Declined: Compose down with volumes in shop" || !strings.Contains(note.Content, "Not done, declined by the user.") {
		t.Fatalf("the denial's note reads %+v\n%s", note.Note, note.Content)
	}
	if len(rang) != 0 {
		t.Fatal("the user's own denial rang")
	}

	f.compose(t, "down-volumes", true)
	q := f.waitQuestion(t)
	f.broker.Find(q.Key).End()
	note = f.waitApprovalNotes(t, f.owner, 2)[1]
	if note.Note.Verdict != approval.Declined || !strings.Contains(note.Content, "the approval expired unanswered") {
		t.Fatalf("the unanswered note reads %+v\n%s", note.Note, note.Content)
	}
	select {
	case id := <-rang:
		if id != f.owner {
			t.Fatalf("the expiry rang %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an unanswered approval rang nobody")
	}
	if runs := f.composeRuns(); len(runs) != 0 {
		t.Fatalf("a declined command ran: %+v", runs)
	}
	if got, _ := f.s.assistants.Events().Get(trigger.ID); len(got.Pending) != 2 || got.Pending[0].Kind != assistant.ComposeKindDeclined {
		t.Fatalf("the compose-declined events read %+v", got.Pending)
	}
	f.waitNoQuestion(t)
}

// An approved command that cannot start, on a stack another run holds, ends
// as a failed approval with the refusal, rings, and fires compose-failed.
func TestAnApprovedCommandThatCannotStartFails(t *testing.T) {
	f := newComposeFixture(t)
	gate := gatedDockerCLI(t)
	trigger, err := f.s.assistants.Events().Add(assistant.TriggerSpec{Owner: f.owner, Event: "compose-failed", Task: "say so"})
	if err != nil {
		t.Fatal(err)
	}
	_, mine := f.compose(t, "up", false)
	userRun := mine["run"].(string)
	f.waitRun(t, userRun, func(v docker.RunView) bool { return v.Running })
	f.compose(t, "down-volumes", true)
	f.decide(t, f.waitQuestion(t), true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Failed || !strings.Contains(note.Content, "a compose run is already under way here") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	if got, _ := f.s.assistants.Events().Get(trigger.ID); len(got.Pending) != 1 || got.Pending[0].Kind != assistant.ComposeKindFailed {
		t.Fatalf("the compose-failed event reads %+v", got.Pending)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	f.waitRun(t, userRun, func(v docker.RunView) bool { return !v.Running })
}

// A git question and an approval on one project stand side by side: the list
// carries both, each is answered under its own key, and an answer posted
// under the other's key reaches nothing. The git question is keyed by the
// project, so the older page's project field still answers it, and that
// field names no approval.
func TestAGitQuestionAndAnApprovalOnOneProjectAreAnsweredApart(t *testing.T) {
	f := newComposeFixture(t)
	listener, err := askpass.Listen(f.stateDir)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() { _ = http.Serve(listener, f.broker.Handler()) }()
	socket := askpass.SocketPath(f.stateDir)

	push := f.broker.Begin(f.project, "push")
	t.Cleanup(push.End)
	helper := make(chan string, 2)
	ask := func() {
		go func() {
			text, err := askpass.Ask(socket, tokenOf(t, push), "Enter passphrase:")
			if err != nil {
				text = "error: " + err.Error()
			}
			helper <- text
		}()
	}
	ask()
	f.compose(t, "down-volumes", true)

	var gitQ, approvalQ askpass.Question
	list := func() {
		t.Helper()
		gitQ, approvalQ = askpass.Question{}, askpass.Question{}
		deadline := time.Now().Add(5 * time.Second)
		for {
			rec := httptest.NewRecorder()
			f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/git/prompt", nil))
			var body struct {
				Questions []askpass.Question `json:"questions"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Questions) == 2 {
				for _, q := range body.Questions {
					if q.Kind == askpass.KindApproval {
						approvalQ = q
					} else {
						gitQ = q
					}
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the list never carried both questions: %s", rec.Body.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	list()
	if _, ok := askpass.ApprovalID(approvalQ.Key); gitQ.Key != f.project || gitQ.Project != f.project || !ok {
		t.Fatalf("the two questions read %+v and %+v", gitQ, approvalQ)
	}

	post := func(body string) bool {
		t.Helper()
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(body)))
		return decodeJSON(t, rec)["ok"] == true
	}
	if post(`{"key":"` + approvalQ.Key + `","id":"` + gitQ.ID + `","answer":"secret"}`) {
		t.Fatal("the approval's key answered the git question")
	}
	if post(`{"key":"` + gitQ.Key + `","id":"` + approvalQ.ID + `","approve":true}`) {
		t.Fatal("the git question's key decided the approval")
	}
	if post(`{"project":"` + f.project + `","id":"` + approvalQ.ID + `","approve":true}`) {
		t.Fatal("the older project field decided the approval")
	}
	if len(f.broker.Questions()) != 2 || len(f.composeRuns()) != 0 {
		t.Fatal("a crossed answer moved a question")
	}

	if !post(`{"key":"` + gitQ.Key + `","id":"` + gitQ.ID + `","answer":"secret"}`) {
		t.Fatal("the git question refused its own key")
	}
	if text := <-helper; text != "secret" {
		t.Fatalf("the helper read %q", text)
	}
	if f.broker.Find(approvalQ.Key) == nil {
		t.Fatal("answering the git question moved the approval")
	}

	ask()
	list()
	if !post(`{"project":"` + f.project + `","id":"` + gitQ.ID + `","answer":"again"}`) {
		t.Fatal("the older project field no longer answers a git question")
	}
	if text := <-helper; text != "again" {
		t.Fatalf("the helper read %q", text)
	}

	if !post(`{"key":"` + approvalQ.Key + `","id":"` + approvalQ.ID + `","approve":false}`) {
		t.Fatal("the approval refused its own key")
	}
	f.waitApprovalNotes(t, f.owner, 1)
	if f.broker.Find(gitQ.Key) != push {
		t.Fatal("deciding the approval ended the git action")
	}
}

// The same action waits once: a second one while the first still waits is
// refused and asks nothing.
func TestTheSameActionWaitsOnce(t *testing.T) {
	f := newComposeFixture(t)
	f.compose(t, "down-volumes", true)
	q := f.waitQuestion(t)
	code, second := f.compose(t, "down-volumes", true)
	if code != http.StatusConflict || !strings.Contains(second["error"].(string), "already waits for the user's approval") {
		t.Fatalf("the second ask answered %d %v", code, second)
	}
	if len(f.broker.Questions()) != 1 {
		t.Fatalf("the refused ask left questions: %v", f.broker.Questions())
	}
	f.broker.Find(q.Key).End()
	f.waitApprovalNotes(t, f.owner, 1)
}

// Deleting an assistant takes its approvals and their questions down without
// a report nobody could read, not even a log line about one.
func TestDeletingTheAssistantDeclinesItsApprovals(t *testing.T) {
	f := newComposeFixture(t)
	f.compose(t, "down-volumes", true)
	f.waitQuestion(t)
	var logged strings.Builder
	var mu sync.Mutex
	log.SetOutput(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return logged.Write(p)
	}))
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f.deleteOwner(t)
	f.waitNoQuestion(t)
	time.Sleep(200 * time.Millisecond)
	if runs := f.composeRuns(); len(runs) != 0 {
		t.Fatalf("a command of a deleted assistant ran: %+v", runs)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(logged.String(), "dropping its report") {
		t.Fatalf("the declined approval logged its dropped report: %s", logged.String())
	}
}

// A question that cannot be put up is said once: the command answers the
// refusal, nothing runs, and no note repeats it.
func TestAnApprovalThatCannotBeAskedIsNoNote(t *testing.T) {
	f := newComposeFixture(t)
	f.s.approvals.SetBroker(nil)
	code, answer := f.compose(t, "down-volumes", true)
	if code != http.StatusConflict || answer["error"] != "The approval could not be asked." {
		t.Fatalf("the start answered %d %v", code, answer)
	}
	time.Sleep(200 * time.Millisecond)
	if runs := f.composeRuns(); len(runs) != 0 {
		t.Fatalf("a run exists: %+v", runs)
	}
	c, _ := f.s.assistants.Get(f.owner)
	for _, m := range c.Messages {
		if m.IsNote() {
			t.Fatalf("the refusal was reported a second time: %+v", m.Note)
		}
	}
}

// The Compose actions approval off asks nothing for any assistant and the
// command starts at once, a form that does not carry the field moves nothing,
// and switched on again every assistant is asked.
func TestTheComposeActionsApprovalDecidesForEveryAssistant(t *testing.T) {
	f := newComposeFixture(t)
	f.postApprovals(t, url.Values{composeApprovalField: {"0"}})
	if ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("the save did not switch the approval off")
	}
	code, answer := f.compose(t, "down-volumes", true)
	runs := f.composeRuns()
	if code != http.StatusOK || answer["pending"] == true || len(f.broker.Questions()) != 0 || len(runs) != 1 || answer["run"] != runs[0].ID || answer["action"] != "Compose down with volumes" || answer["url"] != dockerRunPath("shop", runs[0].ID) {
		t.Fatalf("a confirm action waited with the approval off: %d %v", code, answer)
	}
	f.waitRun(t, runs[0].ID, func(v docker.RunView) bool { return !v.Running })

	f.postApprovals(t, url.Values{"unrelated": {"1"}})
	if ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("a form without the field moved the approval")
	}

	f.postApprovals(t, url.Values{composeApprovalField: {"0", "1"}})
	if !ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("the save did not switch the approval back on")
	}
	if _, again := f.compose(t, "down-volumes", true); again["pending"] != true {
		t.Fatalf("the approval did not come back: %v", again)
	}
	f.decide(t, f.waitQuestion(t), false, false)
	f.waitApprovalNotes(t, f.owner, 1)
}

// "Approve and don't ask again" approves and turns the approval off for every
// assistant; a denial with the same box remembers nothing.
func TestApproveAndDontAskAgainTurnsTheApprovalOff(t *testing.T) {
	f := newComposeFixture(t)
	f.compose(t, "down-volumes", true)
	f.decide(t, f.waitQuestion(t), false, true)
	f.waitApprovalNotes(t, f.owner, 1)
	if !ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("a denial switched the approval off")
	}

	f.compose(t, "down-volumes", true)
	f.decide(t, f.waitQuestion(t), true, true)
	f.waitApprovalNotes(t, f.owner, 2)
	if ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("the box did not switch the approval off")
	}
	first := f.composeRuns()[0].ID
	f.waitRun(t, first, func(v docker.RunView) bool { return !v.Running })
	other, err := f.s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	rec := f.localPost(t, "/projects/shop/docker/compose", other.ID, url.Values{"stack": {""}, "action": {"down-volumes"}})
	if got := decodeJSON(t, rec); rec.Code != http.StatusOK || got["pending"] == true {
		t.Fatalf("an assistant was still asked: %d %s", rec.Code, rec.Body.String())
	}
	for _, run := range f.composeRuns() {
		f.waitRun(t, run.ID, func(v docker.RunView) bool { return !v.Running })
	}
	// Each run's end writes a note into its owner's thread; the test ends
	// after both, or the write lands in a directory already cleaned up.
	f.waitApprovalNotes(t, f.owner, 2)
	deadline := time.Now().Add(15 * time.Second)
	for {
		mine, _ := f.s.assistants.Get(f.owner)
		theirs, _ := f.s.assistants.Get(other.ID)
		if len(mine.Messages) >= 3 && len(theirs.Messages) >= 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the runs' notes never landed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The approval is the user's word: the assistant that asked cannot answer its
// own question over the socket, neither with nor without the don't ask again
// box, and cannot flip the approval on the Approvals tab. The question stays
// up and the browser still answers it.
func TestAnAssistantCannotApproveItself(t *testing.T) {
	f := newComposeFixture(t)
	rec := f.localPost(t, "/settings/assistant/approvals", f.owner, url.Values{composeApprovalField: {"0"}})
	if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != approvalLocalRefusal || !ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatalf("a local save answered %d and moved the approval", rec.Code)
	}
	f.compose(t, "down-volumes", true)
	q := f.waitQuestion(t)
	for _, body := range []string{`,"approve":true,"remember":true}`, `,"approve":true}`} {
		req := httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(`{"key":"`+q.Key+`","id":"`+q.ID+`"`+body))
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		req.Header.Set(localapi.AssistantHeader, f.owner)
		rec = httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != approvalLocalRefusal {
			t.Fatalf("a local approval answered %d: %s", rec.Code, rec.Body.String())
		}
	}
	if len(f.broker.Questions()) != 1 || len(f.composeRuns()) != 0 || !ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("a local approval moved something")
	}
	f.decide(t, q, false, false)
	f.waitApprovalNotes(t, f.owner, 1)
}

// newDeleteFixture is the compose fixture with what a project delete walks
// through on top: the purge over shells, the per project state, the delete
// register and the route itself.
func newDeleteFixture(t *testing.T) *composeFixture {
	t.Helper()
	f := newComposeFixture(t)
	fakeTmux(t, "11111111-1111-4111-8111-111111111111", t.TempDir())
	s := f.s
	s.cfg = config.Config{StateDir: f.stateDir}
	s.shells = shell.NewShells(config.Config{}, tmux.New(), s.projects, nil)
	s.quickOpen = filesystem.NewQuickOpenCache()
	s.commitDrafts = newCommitDrafts(f.stateDir)
	s.searchDrafts = newSearchDrafts(f.stateDir)
	s.lineComments = newLineComments(f.stateDir)
	s.deletes = newProjectDeletes(f.stateDir)
	s.restorer = restore.New(filepath.Join(f.stateDir, "terminal-restore.json"), func() bool { return false },
		nil, s.shells, tmux.New(), s.notifier, nil, func() []string { return nil })
	f.router.POST("/projects/delete", s.handleProjectDelete)
	return f
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
	if q.Kind != askpass.KindApproval || q.Action != "Delete project shop" || !reflect.DeepEqual(q.Details, []askpass.Detail{{Label: "Project", Value: "shop"}}) {
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

// The question lives in the process that asked it. The next process declines
// what it finds waiting in approvals.json, and the assistant reads why; the
// project stays.
func TestAWaitingApprovalIsDeclinedAfterARestart(t *testing.T) {
	f := newDeleteFixture(t)
	f.deleteAs(t, f.owner)
	f.waitQuestion(t)
	restarted := &Server{assistants: f.s.assistants, settings: f.s.settings}
	restarted.approvals = restarted.newApprovals(f.stateDir)
	restarted.RecoverApprovals()
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Declined || !strings.Contains(note.Content, "lost in a restart") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	var left map[string]any
	raw, _ := os.ReadFile(filepath.Join(f.stateDir, "approvals.json"))
	if err := json.Unmarshal(raw, &left); err != nil || len(left) != 0 {
		t.Fatalf("the restart left %s", raw)
	}
	if !exists(f.dir) {
		t.Fatal("the restart deleted the project")
	}
}

// The dialog names the stack where the command runs on one, and the same
// command on another stack of the project is another action that may wait
// beside it.
func TestTheComposeDialogNamesTheStack(t *testing.T) {
	f := newComposeFixture(t)
	api := filepath.Join(f.dir, "api")
	f.s.docker.SetStateForTest(docker.State{Available: true, Host: "tcp://fake:1", Containers: []docker.Container{
		{Name: "api", Service: "api", State: "running", WorkingDir: api},
	}})
	post := func(stack string) int {
		form := url.Values{"stack": {stack}, "action": {"down-volumes"}}
		rec := f.localPost(t, "/projects/shop/docker/compose", f.owner, form)
		return rec.Code
	}
	if code := post("api"); code != http.StatusOK {
		t.Fatalf("the start on the stack answered %d", code)
	}
	q := f.waitQuestion(t)
	want := []askpass.Detail{{Label: "Action", Value: "Compose down with volumes"}, {Label: "Stack", Value: "api"}, {Label: "Project", Value: "shop"}}
	if q.Action != "Compose down with volumes on api in shop" || !reflect.DeepEqual(q.Details, want) || q.Dir != api {
		t.Fatalf("the question reads %q %+v in %q", q.Action, q.Details, q.Dir)
	}
	if code := post(""); code != http.StatusOK {
		t.Fatalf("the root stack's command answered %d beside the other", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(f.broker.Questions()) != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the two stacks do not wait side by side: %+v", f.broker.Questions())
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, q := range f.broker.Questions() {
		f.broker.Find(q.Key).End()
	}
	f.waitApprovalNotes(t, f.owner, 2)
}

// The approval runs what stands when the user approves, not what stood when
// the assistant asked: a command taken out of the settings meanwhile starts
// nothing and the note says why.
func TestAnApprovedComposeResolvesTheCommandAgain(t *testing.T) {
	f := newComposeFixture(t)
	f.compose(t, "down-volumes", true)
	q := f.waitQuestion(t)
	f.s.settings.Set(docker.ActionsSettingKey, "[]")
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Failed || !strings.Contains(note.Content, "the compose command no longer exists") {
		t.Fatalf("the note reads %+v\n%s", note.Note, note.Content)
	}
	if runs := f.composeRuns(); len(runs) != 0 {
		t.Fatalf("a removed command ran: %+v", runs)
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
