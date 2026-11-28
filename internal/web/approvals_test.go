package web

import (
	"context"
	"encoding/json"
	"log"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/settings"
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
		approvalComposeActions:     "Compose actions approval",
		approvalProjectDelete:      "Project delete approval",
		approvalCoderDelete:        "Coder delete approval",
		approvalAssistantDelete:    "Assistant delete approval",
		approvalWorktreePostScript: "Worktree post script approval",
		approvalForgeRetry:         "Pipeline retry approval",
		approvalForgeChange:        "Change request approval",
		approvalForgePublish:       "Review comment approval",
		approvalForgeThread:        "Review thread approval",
	}
	for _, kind := range approvalKinds {
		if want[kind.ID] != kind.Label {
			t.Fatalf("the kinds read %+v", approvalKinds)
		}
		delete(want, kind.ID)
	}
	if len(want) != 0 || approvalKey(approvalComposeActions) != "assistant-approval-compose-actions" || approvalKey(approvalProjectDelete) != "assistant-approval-project-delete" ||
		approvalKey(approvalCoderDelete) != "assistant-approval-coder-delete" || approvalKey(approvalAssistantDelete) != "assistant-approval-assistant-delete" ||
		approvalKey(approvalForgeRetry) != "assistant-approval-forge-retry" || approvalKey(approvalForgeThread) != "assistant-approval-forge-thread" {
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

// approvalsLetGo keeps a test's state directory until its register of
// approvals holds no settled entry any more: an approval leaves the register a
// moment after its note landed, and a test that ends on that note would have
// the directory removed under that write.
func approvalsLetGo(t *testing.T, stateDir string) {
	t.Helper()
	t.Cleanup(func() {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			var register map[string]approval.Approval
			raw, _ := os.ReadFile(filepath.Join(stateDir, "approvals.json"))
			_ = json.Unmarshal(raw, &register)
			if !slices.ContainsFunc(slices.Collect(maps.Values(register)), func(a approval.Approval) bool { return a.Outcome != nil }) {
				return
			}
		}
	})
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
	if q.Kind != askpass.KindApproval || !ok || q.Asker() != assistant.Name+" · "+coder.ShortID(f.owner) || q.Title != "Compose down with volumes" || q.Icon != "brand-docker" || q.Tone != "docker" || q.Action != "Compose down with volumes in shop" || q.Project != "" || q.Command != "docker compose down -v" || q.Dir != f.dir {
		t.Fatalf("the question reads %+v", q)
	}
	if want := []askpass.Detail{{Label: "Project", Value: "shop", As: askpass.AsProject}, {Label: "Stack", Value: "project root", As: askpass.AsStack}}; !reflect.DeepEqual(q.Details, want) {
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
	got, _ := f.s.assistants.Events().Get(trigger.ID)
	if len(got.Pending) != 2 || got.Pending[0].Kind != assistant.ComposeKindDeclined {
		t.Fatalf("the compose-declined events read %+v", got.Pending)
	}
	// Each is named by its approval, so one handed again is the same event
	// and the other approval's is another.
	for _, ev := range got.Pending {
		if !strings.HasPrefix(ev.ID, "approval ") || !strings.HasSuffix(ev.ID, " "+assistant.ComposeKindDeclined) {
			t.Fatalf("the event of a declined approval is named %q", ev.ID)
		}
	}
	if got.Pending[0].ID == got.Pending[1].ID {
		t.Fatalf("two approvals name their events alike, %q", got.Pending[0].ID)
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

// Approve with "Don't ask again for this kind" ticked approves and turns the
// approval off for every assistant; a denial with the same box remembers
// nothing.
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

// An approval the last process had settled stands in approvals.json with its
// end, and the next process tells that end, not a decline of its own: the
// approved command that could not start reads as failed and fires
// compose-failed. A process that dies before it let the entry go leaves it
// for the one after, which tells it again into the note that stands.
func TestASettledApprovalIsToldWithItsOwnEndAfterARestart(t *testing.T) {
	f := newComposeFixture(t)
	trigger, err := f.s.assistants.Events().Add(assistant.TriggerSpec{Owner: f.owner, Event: "compose-failed", Task: "say so", Batch: time.Hour, BatchSet: true})
	if err != nil {
		t.Fatal(err)
	}
	entry := `{"a1b2c3d4":{"id":"a1b2c3d4","owner":"` + f.owner + `","kind":"` + approvalComposeActions + `","key":"shop\ndown-volumes","what":"Compose down with volumes in shop","createdAt":"2026-11-20T10:00:00Z","outcome":{"verdict":"failed","text":"no reachable Docker host"}}}`
	for range 2 {
		if err := os.WriteFile(filepath.Join(f.stateDir, "approvals.json"), []byte(entry), 0o600); err != nil {
			t.Fatal(err)
		}
		restarted := &Server{assistants: f.s.assistants, settings: f.s.settings}
		restarted.approvals = restarted.newApprovals(f.stateDir)
		restarted.RecoverApprovals()

		notes := f.waitApprovalNotes(t, f.owner, 1)
		if len(notes) != 1 || notes[0].Note.Verdict != approval.Failed || !strings.Contains(notes[0].Content, "Approved, and it failed: no reachable Docker host") {
			t.Fatalf("the thread holds %d notes, the first reads %+v\n%s", len(notes), notes[0].Note, notes[0].Content)
		}
		if got, _ := f.s.assistants.Events().Get(trigger.ID); len(got.Pending) != 1 || got.Pending[0].ID != "approval a1b2c3d4 "+assistant.ComposeKindFailed {
			t.Fatalf("want the end once in the window, got %+v", got.Pending)
		}
		var left map[string]any
		raw, _ := os.ReadFile(filepath.Join(f.stateDir, "approvals.json"))
		if err := json.Unmarshal(raw, &left); err != nil || len(left) != 0 {
			t.Fatalf("the restart left %s", raw)
		}
	}
}

// An approved compose command starts a run that reports its own end, named
// after its approval. A process that dies behind that start and before the
// approval's own end is written leaves the approval failed, how far it got
// unknown, and the run in the register. The next process finds the run from
// the approval and tells the approval the way it would have ended, approved
// with the run it started: no compose-failed fires, the run's own end is the
// one event of it, however often the start is told. Where the register holds
// no such run nobody knows how far it got, and the one end is the failed one
// that says so.
func TestAnApprovedRunCutByARestartEndsOnce(t *testing.T) {
	f := newComposeFixture(t)
	window := func(event string) func() []string {
		trigger, err := f.s.assistants.Events().Add(assistant.TriggerSpec{Owner: f.owner, Event: event, Task: "say so", Batch: time.Hour, BatchSet: true})
		if err != nil {
			t.Fatal(err)
		}
		return func() []string {
			got, _ := f.s.assistants.Events().Get(trigger.ID)
			var ids []string
			for _, ev := range got.Pending {
				ids = append(ids, ev.ID)
			}
			return ids
		}
	}
	failed, done := window("compose-failed"), window("compose-done")

	f.compose(t, "down-volumes", true)
	q := f.waitQuestion(t)
	live, _ := askpass.ApprovalID(q.Key)
	f.decide(t, q, true, false)
	f.waitApprovalNotes(t, f.owner, 1)
	if runs := f.composeRuns(); len(runs) != 1 || runs[0].ID != composeApprovalRun(live) {
		t.Fatalf("want the approved run named after its approval %s, got %+v", live, runs)
	}
	f.waitNoteOf(t, composeApprovalRun(live))
	approvalsLetGo(t, f.stateDir)

	entry := func(id string) string {
		return `{"` + id + `":{"id":"` + id + `","owner":"` + f.owner + `","kind":"` + approvalComposeActions + `","key":"shop\\n\\ndown-volumes","what":"Compose down with volumes in shop","title":"Compose down with volumes","createdAt":"2026-11-27T10:00:00Z","outcome":{"verdict":"failed","text":"the cockpit restarted while it ran, so how far it got is not known"}}}`
	}
	restart := func(id string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(f.stateDir, "approvals.json"), []byte(entry(id)), 0o600); err != nil {
			t.Fatal(err)
		}
		restarted := &Server{assistants: f.s.assistants, settings: f.s.settings, docker: f.s.docker}
		restarted.approvals = restarted.newApprovals(f.stateDir)
		restarted.RecoverApprovals()
		if raw, _ := os.ReadFile(filepath.Join(f.stateDir, "approvals.json")); strings.TrimSpace(string(raw)) != "{}" {
			t.Fatalf("the restart left %s", raw)
		}
	}

	p, _ := f.s.projects.FindByName("shop")
	stack, _ := f.s.findStack(p, "")
	action, _ := docker.ActionByID(f.s.composeActions(), "down-volumes")
	run, err := f.s.startCompose(p, stack, action, f.owner, composeApprovalRun("a1b2c3d4"))
	if err != nil || run != composeApprovalRun("a1b2c3d4") {
		t.Fatalf("the run of the approval started as %q: %v", run, err)
	}
	f.waitNoteOf(t, run)
	for range 2 {
		restart("a1b2c3d4")
		notes := f.waitApprovalNotes(t, f.owner, 2)
		if len(notes) != 2 || notes[1].Note.Verdict != approval.Done || notes[1].Note.Headline != "Approved: Compose down with volumes in shop" || !strings.Contains(notes[1].Content, "Run "+run+" started") {
			t.Fatalf("want the approval told as approved with the run it started, got %d notes, the last %+v\n%s", len(notes), notes[len(notes)-1].Note, notes[len(notes)-1].Content)
		}
		if got := failed(); len(got) != 0 {
			t.Fatalf("a run that went through fired compose-failed: %v", got)
		}
		if got, want := done(), []string{"compose " + composeApprovalRun(live) + " " + assistant.ComposeKindDone, "compose " + run + " " + assistant.ComposeKindDone}; !slices.Equal(got, want) {
			t.Fatalf("want the end of each run once, %v, got %v", want, got)
		}
	}

	restart("e5f6a7b8")
	notes := f.waitApprovalNotes(t, f.owner, 3)
	if notes[2].Note.Verdict != approval.Failed || !strings.Contains(notes[2].Content, "how far it got is not known") {
		t.Fatalf("want an approval whose run nobody finds told as failed, got %+v\n%s", notes[2].Note, notes[2].Content)
	}
	if got, want := failed(), []string{"approval e5f6a7b8 " + assistant.ComposeKindFailed}; !slices.Equal(got, want) || len(done()) != 2 {
		t.Fatalf("want the one end that says nobody knows, %v, got %v beside %v", want, got, done())
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
	want := []askpass.Detail{{Label: "Project", Value: "shop", As: askpass.AsProject}, {Label: "Stack", Value: "api", As: askpass.AsStack}}
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
