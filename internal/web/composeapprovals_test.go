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
	"strings"
	"sync"
	"testing"
	"time"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/settings"
)

// composeFixture is a server with the pieces an owned compose run touches: a
// project with a compose file, a docker service that believes in a daemon and
// runs a fake CLI, an assistant service with one assistant, a notifier, a
// broker and the settings store the compose actions come from.
type composeFixture struct {
	s        *Server
	project  string
	dir      string
	stateDir string
	owner    string
	broker   *askpass.Broker
	router   *gin.Engine
	notifier *notify.Service
}

func newComposeFixture(t *testing.T) *composeFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	dir := filepath.Join(root, "shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeDockerCLI(t)
	stateDir := t.TempDir()
	dockerService := docker.NewService(stateDir, func() string { return "" })
	dockerService.SetStateForTest(docker.State{Available: true, Host: "tcp://fake:1"})
	assistants, _, err := assistant.New(stateDir, composeCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	broker := askpass.New(stateDir)
	store := settings.New(filepath.Join(stateDir, "settings.json"))
	notifier := notify.NewService(filepath.Join(stateDir, "notifications.json"), nil)
	s := &Server{
		projects:      project.NewRepository(root, nil),
		docker:        dockerService,
		assistants:    assistants,
		watcher:       assistant.NewWatcher(assistants, assistant.NewJobs(assistant.NewStore(stateDir)), nil, nil),
		notifier:      notifier,
		bus:           eventbus.New(),
		settings:      store,
		askpassBroker: broker,
	}
	s.gitPromptNoticed = map[string]bool{}
	broker.OnChange = func() { s.reconcileGitPromptNews(broker) }
	dockerService.OnComposeDone(s.composeDone)
	r := gin.New()
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.POST("/projects/:name/docker/compose", s.handleDockerCompose)
	r.GET("/projects/:name/docker", s.handleProjectDocker)
	r.GET("/projects/:name/docker/runs/:id/output", s.handleDockerRunOutput)
	r.POST("/projects/:name/docker/runs/:id/stop", s.handleDockerRunStop)
	r.GET("/git/prompt", s.handleGitPromptList)
	r.POST("/git/prompt", s.handleGitPromptAnswer)
	r.POST("/settings/assistant/approvals", s.handleSettingsAssistantApprovalsSave)
	r.POST("/settings/docker", s.handleSettingsDockerSave)
	r.POST("/docker/actions/restore", s.handleDockerActionsRestore)
	r.POST("/assistants/:id/delete", func(c *gin.Context) { s.assistantDelete(c, c.Param("id")) })
	return &composeFixture{s: s, project: "shop", dir: dir, stateDir: stateDir, owner: owner.ID, broker: broker, router: r, notifier: notifier}
}

// composeCoder is the one coder of the fixture, with a runner that holds no
// provider sessions, so an assistant can be deleted without a CLI to ask.
type composeCoder struct{}

func (composeCoder) Available() []assistant.CoderInfo {
	return []assistant.CoderInfo{{ID: "claude", Label: "Claude", Runner: noSessions{}}}
}

type noSessions struct{ assistant.Runner }

func (noSessions) SessionExists(string) bool { return false }

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var answer map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the answer is no JSON: %v\n%s", err, rec.Body.String())
	}
	return answer
}

// fakeDockerCLI puts a docker on the PATH that records nothing and exits 0.
func fakeDockerCLI(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\necho compose says hi\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// gatedDockerCLI puts a docker on the PATH that runs until the file it
// answers is removed, so a test can act on a run while it is going.
func gatedDockerCLI(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	gate := filepath.Join(bin, "gate")
	if err := os.WriteFile(gate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nwhile [ -e '" + gate + "' ]; do sleep 0.02; done\necho compose says hi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return gate
}

// deleteOwner deletes the fixture's assistant the way the page does.
func (f *composeFixture) deleteOwner(t *testing.T) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/assistants/"+f.owner+"/delete", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the delete answered %d: %s", rec.Code, rec.Body.String())
	}
}

// compose posts one action, as the page (no caller) or as the assistant (over
// the local socket, which the request context and the header say).
func (f *composeFixture) compose(t *testing.T, action string, asAssistant bool) (int, map[string]any) {
	t.Helper()
	form := url.Values{"stack": {""}, "action": {action}}
	req := httptest.NewRequest(http.MethodPost, "/projects/"+f.project+"/docker/compose", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if asAssistant {
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		req.Header.Set(localapi.AssistantHeader, f.owner)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec.Code, decodeJSON(t, rec)
}

func (f *composeFixture) waitNote(t *testing.T) assistant.Message {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, _ := f.s.assistants.Get(f.owner)
		for _, m := range c.Messages {
			if m.IsNote() && m.Note.Source == assistant.NoteCompose {
				return m
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no compose note reached the owner's thread")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitNoteOf waits for the note of one run, the last thing its end writes.
func (f *composeFixture) waitNoteOf(t *testing.T, run string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, _ := f.s.assistants.Get(f.owner)
		for _, m := range c.Messages {
			if m.IsNote() && m.Note.Run == run {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no note for run %s", run)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *composeFixture) waitQuestion(t *testing.T) askpass.Question {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if list := f.broker.Questions(); len(list) == 1 {
			return list[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("the approval question never stood")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *composeFixture) run(t *testing.T, id string) docker.RunView {
	t.Helper()
	view, ok := f.s.docker.ComposeRunByID(id)
	if !ok {
		t.Fatalf("run %s is unknown", id)
	}
	return view
}

func (f *composeFixture) waitRun(t *testing.T, id string, done func(docker.RunView) bool) docker.RunView {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		view := f.run(t, id)
		if done(view) {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s never got there: %+v", id, view)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A run the page starts is nobody's and rings the project's docker target; a
// run the assistant starts carries the assistant, rings nothing and reports
// into that assistant's thread as a note with a link to its page, and the
// project's news stays the user's own run.
func TestAnAssistantsRunIsSilentAndReportsIntoItsThread(t *testing.T) {
	f := newComposeFixture(t)
	code, mine := f.compose(t, "up", false)
	if code != http.StatusOK || mine["run"] == "" {
		t.Fatalf("the user's run answered %d %v", code, mine)
	}
	userRun := mine["run"].(string)
	f.waitRun(t, userRun, func(v docker.RunView) bool { return !v.Running })
	if !f.notifier.UnreadTargets()[notify.DockerTarget("shop")] {
		t.Fatal("the user's run rang nothing")
	}
	f.notifier.MarkTargetRead(notify.DockerTarget("shop"))

	code, theirs := f.compose(t, "up", true)
	if code != http.StatusOK || theirs["pending"] == true {
		t.Fatalf("the assistant's run answered %d %v", code, theirs)
	}
	id := theirs["run"].(string)
	if view := f.run(t, id); view.Owner != f.owner {
		t.Fatalf("the run does not carry its owner: %+v", view)
	}
	note := f.waitNote(t)
	if note.Note.Run != id || note.Note.Verdict != assistant.ComposeKindDone || note.Note.Project != "shop" || note.Note.Name != "Compose up" {
		t.Fatalf("the note reads %+v", note.Note)
	}
	if !strings.Contains(note.Content, "[Open the run](/projects/shop/docker/runs/"+id+")") || !strings.Contains(note.Content, "compose says hi") {
		t.Fatalf("the note says:\n%s", note.Content)
	}
	if f.notifier.UnreadTargets()[notify.DockerTarget("shop")] {
		t.Fatalf("the assistant's run rang the project's docker target: %v", f.notifier.UnreadTargets())
	}
	if last, ok := f.s.docker.LastComposeRun("shop"); !ok || last.ID != userRun {
		t.Fatalf("the project's news names %+v, want the user's run %s", last, userRun)
	}
}

// The run page reads the project's compose news only for the run that news is
// about: looking at an assistant's run, which the note and the approval both
// link at, leaves the user's own unread news standing.
func TestTheRunPageReadsTheNewsOnlyOfItsOwnRun(t *testing.T) {
	f := newComposeFixture(t)
	_, mine := f.compose(t, "up", false)
	userRun := mine["run"].(string)
	f.waitRun(t, userRun, func(v docker.RunView) bool { return !v.Running })
	_, theirs := f.compose(t, "up", true)
	theirRun := theirs["run"].(string)
	f.waitNote(t)
	if !f.notifier.UnreadTargets()[notify.DockerTarget("shop")] {
		t.Fatal("the user's run rang nothing")
	}

	f.s.readComposeNews("shop", theirRun)
	if !f.notifier.UnreadTargets()[notify.DockerTarget("shop")] {
		t.Fatal("reading the assistant's run cleared the user's news")
	}
	f.s.readComposeNews("shop", userRun)
	if f.notifier.UnreadTargets()[notify.DockerTarget("shop")] {
		t.Fatal("reading the user's own run left its news unread")
	}
}

// A confirm action the assistant starts is parked: the answer carries the id
// and says it waits, the question stands in the broker keyed by the run with
// the assistant, the project, the stack and the command, it is news of its
// own, and approving it starts the run, which then reports done.
func TestAConfirmActionOfAnAssistantWaitsForTheApproval(t *testing.T) {
	f := newComposeFixture(t)
	code, answer := f.compose(t, "down-volumes", true)
	if code != http.StatusOK || answer["pending"] != true || answer["run"] == "" {
		t.Fatalf("the parked run answered %d %v", code, answer)
	}
	id := answer["run"].(string)
	if view := f.run(t, id); !view.Pending || view.Running || view.Owner != f.owner {
		t.Fatalf("the run reads %+v", view)
	}
	q := f.waitQuestion(t)
	if q.Kind != askpass.KindApproval || q.Key != askpass.ApprovalKey(id) || q.Project != "shop" || q.Action != "Compose down with volumes" || q.Assistant != assistant.Name || q.Stack != "" || q.URL != "/projects/shop/docker/runs/"+id {
		t.Fatalf("the question reads %+v", q)
	}
	if !strings.Contains(q.Command, "compose down -v") || q.Dir != f.dir {
		t.Fatalf("the question does not say what runs where: %+v", q)
	}
	if !f.notifier.UnreadTargets()[notify.ApprovalTarget(id)] {
		t.Fatalf("the question is no news: %v", f.notifier.UnreadTargets())
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/git/prompt", nil))
	if body := rec.Body.String(); !strings.Contains(body, `"kind":"approval"`) || !strings.Contains(body, `"target":"`+notify.ApprovalTarget(id)+`"`) {
		t.Fatalf("the dialog's list reads %s", body)
	}

	rec = httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(`{"key":"`+q.Key+`","id":"`+q.ID+`","approve":true}`)))
	if got := decodeJSON(t, rec); got["ok"] != true {
		t.Fatalf("the approval was refused: %v", got)
	}
	f.waitRun(t, id, func(v docker.RunView) bool { return !v.Pending && !v.Running })
	note := f.waitNote(t)
	if note.Note.Verdict != assistant.ComposeKindDone || note.Note.Run != id {
		t.Fatalf("the approved run's note reads %+v", note.Note)
	}
	if f.notifier.UnreadTargets()[notify.ApprovalTarget(id)] {
		t.Fatal("the decided question is still news")
	}
	if !ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("an approval without the box turned the approval off")
	}
}

// Denying ends the run declined with a note saying so; the same for a
// question whose action ends before anybody decided. That end is what
// composeApprovalTimeout calls when it passes, so this covers what the expiry
// does to the run, not the timeout itself, which is not waited out here.
func TestADeniedOrEndedApprovalDeclinesTheRun(t *testing.T) {
	f := newComposeFixture(t)
	_, first := f.compose(t, "down-volumes", true)
	id := first["run"].(string)
	q := f.waitQuestion(t)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(`{"key":"`+q.Key+`","id":"`+q.ID+`","approve":false}`)))
	if got := decodeJSON(t, rec); got["ok"] != true {
		t.Fatalf("the denial was refused: %v", got)
	}
	view := f.waitRun(t, id, func(v docker.RunView) bool { return !v.Pending })
	if !view.Declined || view.Failure != "declined by the user" {
		t.Fatalf("the denied run reads %+v", view)
	}
	note := f.waitNote(t)
	if note.Note.Verdict != assistant.ComposeKindDeclined || !strings.Contains(note.Content, "never ran, declined by the user") {
		t.Fatalf("the denied run's note reads %+v\n%s", note.Note, note.Content)
	}

	_, second := f.compose(t, "down-volumes", true)
	ended := second["run"].(string)
	q = f.waitQuestion(t)
	f.broker.Find(q.Key).End()
	view = f.waitRun(t, ended, func(v docker.RunView) bool { return !v.Pending })
	f.waitNoteOf(t, ended)
	if !view.Declined || !strings.Contains(view.Failure, "expired unanswered") {
		t.Fatalf("the ended run reads %+v", view)
	}
	if f.notifier.UnreadTargets()[notify.ApprovalTarget(id)] || f.notifier.UnreadTargets()[notify.ApprovalTarget(ended)] {
		t.Fatal("a decided question is still news")
	}
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
	_, answer := f.compose(t, "down-volumes", true)
	id := answer["run"].(string)

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
	if gitQ.Key != f.project || gitQ.Project != f.project || approvalQ.Key != askpass.ApprovalKey(id) || approvalQ.Project != f.project {
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
	if len(f.broker.Questions()) != 2 || !f.run(t, id).Pending {
		t.Fatal("a crossed answer moved a question")
	}

	if !post(`{"key":"` + gitQ.Key + `","id":"` + gitQ.ID + `","answer":"secret"}`) {
		t.Fatal("the git question refused its own key")
	}
	if text := <-helper; text != "secret" {
		t.Fatalf("the helper read %q", text)
	}
	if !f.run(t, id).Pending || f.broker.Find(approvalQ.Key) == nil {
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
	if view := f.waitRun(t, id, func(v docker.RunView) bool { return !v.Pending }); !view.Declined {
		t.Fatalf("the denied run reads %+v", view)
	}
	f.waitNoteOf(t, id)
	if f.broker.Find(gitQ.Key) != push {
		t.Fatal("deciding the approval ended the git action")
	}
}

// The assistant's readings: compose-list's JSON names the stacks with their
// newest run and the commands, and a run's output reading carries what
// compose-show prints.
func TestTheAssistantReadsStacksAndRuns(t *testing.T) {
	f := newComposeFixture(t)
	_, started := f.compose(t, "up", true)
	id := started["run"].(string)
	f.waitRun(t, id, func(v docker.RunView) bool { return !v.Running })
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/shop/docker", nil))
	list := decodeJSON(t, rec)
	stacks, _ := list["stacks"].([]any)
	if len(stacks) != 1 || list["project"] != "shop" {
		t.Fatalf("the list reads %v", list)
	}
	stack := stacks[0].(map[string]any)
	if run, _ := stack["run"].(map[string]any); run["id"] != id || run["action"] != "Compose up" {
		t.Fatalf("the stack's newest run reads %v", stack)
	}
	actions, _ := list["actions"].([]any)
	if len(actions) != 4 {
		t.Fatalf("the commands read %v", actions)
	}
	rec = httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/shop/docker/runs/"+id+"/output", nil))
	show := decodeJSON(t, rec)
	if show["id"] != id || show["exited"] != true || show["owner"] != f.owner || show["running"] != false || !strings.Contains(show["output"].(string), "compose says hi") {
		t.Fatalf("the run's reading is %v", show)
	}
}

// Calling a parked run off takes its question along: the run is over, and a
// question left standing would sit on every page until its bound ran out.
func TestCancellingAParkedRunTakesItsQuestionAlong(t *testing.T) {
	f := newComposeFixture(t)
	_, answer := f.compose(t, "down-volumes", true)
	id := answer["run"].(string)
	f.waitQuestion(t)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/shop/docker/runs/"+id+"/stop", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the stop answered %d: %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(f.broker.Questions()) != 0 || f.notifier.UnreadTargets()[notify.ApprovalTarget(id)] {
		if time.Now().After(deadline) {
			t.Fatal("the question of the cancelled run still stands")
		}
		time.Sleep(10 * time.Millisecond)
	}
	view := f.run(t, id)
	if !view.Declined || !view.Cancelled || view.Failure != "the run was cancelled" {
		t.Fatalf("the cancelled run reads %+v", view)
	}
	note := f.waitNote(t)
	if note.Note.Verdict != assistant.ComposeKindDeclined {
		t.Fatalf("the note reads %+v", note.Note)
	}
	time.Sleep(100 * time.Millisecond)
	c, _ := f.s.assistants.Get(f.owner)
	notes := 0
	for _, m := range c.Messages {
		if m.IsNote() {
			notes++
		}
	}
	if notes != 1 {
		t.Fatalf("the cancelled run reported %d times", notes)
	}
}

// waitNoQuestion waits until no question stands and the run's approval is no
// news any more.
func (f *composeFixture) waitNoQuestion(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(f.broker.Questions()) != 0 || f.broker.Find(askpass.ApprovalKey(id)) != nil || f.notifier.UnreadTargets()[notify.ApprovalTarget(id)] {
		if time.Now().After(deadline) {
			t.Fatalf("the question of run %s still stands", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The question is up before the start is answered, so a stop the assistant
// sends right after it finds the bridge and takes it down: no question is
// left standing for a run that is over.
func TestAStopRightAfterTheStartLeavesNoQuestion(t *testing.T) {
	f := newComposeFixture(t)
	_, answer := f.compose(t, "down-volumes", true)
	id := answer["run"].(string)
	if f.broker.Find(askpass.ApprovalKey(id)) == nil {
		t.Fatal("the start was answered before its question was up")
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/shop/docker/runs/"+id+"/stop", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the stop answered %d: %s", rec.Code, rec.Body.String())
	}
	f.waitNoQuestion(t, id)
	time.Sleep(200 * time.Millisecond)
	if len(f.broker.Questions()) != 0 {
		t.Fatal("a question came up after the stop")
	}
	if view := f.run(t, id); !view.Declined || !view.Cancelled {
		t.Fatalf("the stopped run reads %+v", view)
	}
}

// One owner parks one run per stack: a second confirm action while the first
// still waits is refused instead of failing at its approval.
func TestASecondParkedRunOnOneStackIsRefused(t *testing.T) {
	f := newComposeFixture(t)
	_, first := f.compose(t, "down-volumes", true)
	f.waitQuestion(t)
	code, second := f.compose(t, "down-volumes", true)
	if code != http.StatusConflict || second["error"] == nil {
		t.Fatalf("the second park answered %d %v", code, second)
	}
	if len(f.broker.Questions()) != 1 {
		t.Fatalf("the refused park left questions: %v", f.broker.Questions())
	}
	f.broker.Find(askpass.ApprovalKey(first["run"].(string))).End()
	f.waitNoteOf(t, first["run"].(string))
}

// Deleting an assistant declines the runs that wait for it and takes their
// questions down: nobody is left to hear how one approved later went.
func TestDeletingTheAssistantDeclinesItsParkedRuns(t *testing.T) {
	f := newComposeFixture(t)
	_, answer := f.compose(t, "down-volumes", true)
	id := answer["run"].(string)
	f.waitQuestion(t)
	f.deleteOwner(t)
	f.waitNoQuestion(t, id)
	if view := f.run(t, id); view.Pending || !view.Declined || view.Failure != "the assistant was deleted" {
		t.Fatalf("the run reads %+v", view)
	}
}

// A run that is going when its assistant is deleted becomes the user's: its
// end rings the project's docker target and is the run that news is about,
// instead of a report into a thread that is gone.
func TestDeletingTheAssistantHandsItsRunningRunsToTheUser(t *testing.T) {
	f := newComposeFixture(t)
	gate := gatedDockerCLI(t)
	code, answer := f.compose(t, "up", true)
	if code != http.StatusOK {
		t.Fatalf("the start answered %d %v", code, answer)
	}
	id := answer["run"].(string)
	f.waitRun(t, id, func(v docker.RunView) bool { return v.Running })
	f.deleteOwner(t)
	if view := f.run(t, id); view.Owner != "" || !view.Running {
		t.Fatalf("the running run was not handed over: %+v", view)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	f.waitRun(t, id, func(v docker.RunView) bool { return !v.Running })
	deadline := time.Now().Add(5 * time.Second)
	for !f.notifier.UnreadTargets()[notify.DockerTarget("shop")] {
		if time.Now().After(deadline) {
			t.Fatal("the end of the handed over run rang nobody")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if last, ok := f.s.docker.LastComposeRun("shop"); !ok || last.ID != id || last.Failure == "" {
		t.Fatalf("the project's news names %+v, want the failed run %s", last, id)
	}
}

// Deleting a project declines the runs parked in it and nothing outside it:
// a directory whose name only starts with the project's is another project.
func TestDeletingAProjectDeclinesItsParkedRuns(t *testing.T) {
	f := newComposeFixture(t)
	_, answer := f.compose(t, "down-volumes", true)
	id := answer["run"].(string)
	f.waitQuestion(t)
	f.s.declineProjectApprovals(strings.TrimSuffix(f.dir, "p"))
	if view := f.run(t, id); !view.Pending {
		t.Fatalf("a run of another project was declined: %+v", view)
	}
	f.s.declineProjectApprovals(f.dir)
	f.waitNoQuestion(t, id)
	if view := f.run(t, id); view.Pending || !view.Declined || view.Failure != "the project was deleted" {
		t.Fatalf("the run reads %+v", view)
	}
	f.waitNoteOf(t, id)
}

// localPost sends one request over the local socket with the id in the
// header, empty for a call that names no assistant.
func (f *composeFixture) localPost(t *testing.T, path, id string, body url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
	if id != "" {
		req.Header.Set(localapi.AssistantHeader, id)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// A local call is an assistant's, so one that names no live assistant is
// refused instead of running as the user: no id at all, and the id of an
// assistant that is gone. Nothing runs, nothing parks, nothing rings.
func TestALocalComposeWithoutALiveAssistantIsRefused(t *testing.T) {
	f := newComposeFixture(t)
	stale, err := f.s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.assistants.Delete(stale.ID); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"missing": "", "stale": stale.ID} {
		for _, action := range []string{"up", "down-volumes"} {
			rec := f.localPost(t, "/projects/shop/docker/compose", id, url.Values{"stack": {""}, "action": {action}})
			if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != assistantCallerRefusal {
				t.Fatalf("%s id, %s answered %d: %s", name, action, rec.Code, rec.Body.String())
			}
		}
	}
	if _, ok := f.s.docker.LastComposeRun("shop"); ok {
		t.Fatal("a refused call ran anyway")
	}
	if len(f.broker.Questions()) != 0 || f.notifier.UnreadTargets()[notify.DockerTarget("shop")] {
		t.Fatal("a refused call asked or rang")
	}
}

// The approval is the user's word: the assistant that asked cannot answer its
// own question over the socket, neither with nor without the don't ask again
// box, and cannot flip the approval on the Approvals tab. The question stays
// up and the browser still answers it.
func TestAnAssistantCannotApproveItself(t *testing.T) {
	f := newComposeFixture(t)
	_, answer := f.compose(t, "down-volumes", true)
	id := answer["run"].(string)
	q := f.waitQuestion(t)
	req := httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(`{"key":"`+q.Key+`","id":"`+q.ID+`","approve":true,"remember":true}`))
	req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
	req.Header.Set(localapi.AssistantHeader, f.owner)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != approvalLocalRefusal {
		t.Fatalf("a local approval answered %d: %s", rec.Code, rec.Body.String())
	}
	if view := f.run(t, id); !view.Pending || !ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatalf("the local approval moved something: %+v", view)
	}
	rec = f.localPost(t, "/settings/assistant/approvals", f.owner, url.Values{"approval-" + approvalComposeActions: {"0"}})
	if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != approvalLocalRefusal || !ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatalf("a local settings save answered %d and moved the approval", rec.Code)
	}
	rec = httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(`{"key":"`+q.Key+`","id":"`+q.ID+`","approve":false}`)))
	if got := decodeJSON(t, rec); got["ok"] != true {
		t.Fatalf("the browser's denial was refused: %v", got)
	}
	f.waitNoteOf(t, id)
}

// An assistant stops its own runs and nobody else's: the user's run and
// another assistant's are refused with a sentence naming whose they are, a
// call without an id is refused, and the browser stops any of them.
func TestAnAssistantStopsOnlyItsOwnRuns(t *testing.T) {
	f := newComposeFixture(t)
	other, err := f.s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	_, parked := f.compose(t, "down-volumes", true)
	id := parked["run"].(string)
	f.waitQuestion(t)
	stop := "/projects/shop/docker/runs/" + id + "/stop"

	rec := f.localPost(t, stop, other.ID, nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(decodeJSON(t, rec)["error"].(string), "belongs to "+f.s.assistantName(f.owner)) {
		t.Fatalf("another assistant's stop answered %d: %s", rec.Code, rec.Body.String())
	}
	rec = f.localPost(t, stop, "", nil)
	if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != assistantCallerRefusal {
		t.Fatalf("a stop without an id answered %d: %s", rec.Code, rec.Body.String())
	}
	if view := f.run(t, id); !view.Pending {
		t.Fatalf("a refused stop moved the run: %+v", view)
	}
	rec = f.localPost(t, stop, f.owner, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the owner's stop answered %d: %s", rec.Code, rec.Body.String())
	}
	f.waitNoQuestion(t, id)
	f.waitNoteOf(t, id)

	_, mine := f.compose(t, "up", false)
	userRun := mine["run"].(string)
	rec = f.localPost(t, "/projects/shop/docker/runs/"+userRun+"/stop", f.owner, nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(decodeJSON(t, rec)["error"].(string), "belongs to the user") {
		t.Fatalf("the assistant's stop of the user's run answered %d: %s", rec.Code, rec.Body.String())
	}
	f.waitRun(t, userRun, func(v docker.RunView) bool { return !v.Running })
}

// A compose trigger reads as what it listens to, never as any coder.
func TestAComposeTriggerIsNoAnyCoder(t *testing.T) {
	f := newComposeFixture(t)
	trigger, err := f.s.assistants.Events().Add(assistant.TriggerSpec{Owner: f.owner, Event: "compose-ended", Task: "say so"})
	if err != nil {
		t.Fatal(err)
	}
	view := f.s.assistantTriggerView(trigger, false)
	if view.Where != "any compose run of mine" {
		t.Fatalf("a compose trigger reads %q", view.Where)
	}
}

// An assistant cannot change the compose commands over the socket: the ask
// first flag and the command line are what the approval stands on, so the
// settings save and the restore are refused and the stored list stays. The
// browser still restores.
func TestAnAssistantCannotRewriteTheComposeCommands(t *testing.T) {
	f := newComposeFixture(t)
	storeComposeActions(f.s.settings, docker.DefaultActions()[:2])
	stored, _ := f.s.settings.Lookup(docker.ActionsSettingKey)
	for _, path := range []string{"/settings/docker", "/docker/actions/restore"} {
		rec := f.localPost(t, path, f.owner, url.Values{"docker_host": {""}})
		if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != composeActionsLocalRefusal {
			t.Fatalf("a local post to %s answered %d: %s", path, rec.Code, rec.Body.String())
		}
		if now, ok := f.s.settings.Lookup(docker.ActionsSettingKey); !ok || now != stored {
			t.Fatalf("a local post to %s moved the commands to %q", path, now)
		}
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/docker/actions/restore", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the browser's restore answered %d", rec.Code)
	}
	if _, ok := f.s.settings.Lookup(docker.ActionsSettingKey); ok {
		t.Fatal("the browser's restore left the stored list")
	}
}

// Don't ask again is about the kind: ticked on an approval whose run a cancel
// took away in the meantime, it still turns the approval off.
func TestDontAskAgainHoldsWhenTheRunMovedFirst(t *testing.T) {
	f := newComposeFixture(t)
	_, answer := f.compose(t, "down-volumes", true)
	id := answer["run"].(string)
	q := f.waitQuestion(t)
	if err := f.s.docker.CancelCompose(id, true); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(`{"key":"`+q.Key+`","id":"`+q.ID+`","approve":true,"remember":true}`)))
	if got := decodeJSON(t, rec); got["ok"] != true {
		t.Fatalf("the approval was refused: %v", got)
	}
	f.waitNoQuestion(t, id)
	deadline := time.Now().Add(5 * time.Second)
	for ApprovalAsks(f.s.settings, approvalComposeActions) {
		if time.Now().After(deadline) {
			t.Fatal("the box was dropped because the run had moved")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if view := f.run(t, id); !view.Cancelled || !view.Declined {
		t.Fatalf("the cancelled run reads %+v", view)
	}
}

// A question that cannot be asked is said once: the start answers the
// refusal and the run ends declined, with no note about it in the thread.
func TestAnApprovalThatCannotBeAskedIsNoNote(t *testing.T) {
	f := newComposeFixture(t)
	f.s.askpassBroker = nil
	code, answer := f.compose(t, "down-volumes", true)
	if code != http.StatusConflict || answer["error"] != "the approval could not be asked" {
		t.Fatalf("the start answered %d %v", code, answer)
	}
	runs := f.s.docker.ComposeRunsForDir(f.dir)
	if len(runs) != 1 || !runs[0].Declined || runs[0].Pending || runs[0].Failure != "the approval could not be asked" {
		t.Fatalf("the run reads %+v", runs)
	}
	time.Sleep(200 * time.Millisecond)
	c, _ := f.s.assistants.Get(f.owner)
	for _, m := range c.Messages {
		if m.IsNote() {
			t.Fatalf("the refusal was reported a second time: %+v", m.Note)
		}
	}
}

// The windows around Disown: an owned run whose end lands after its owner is
// gone and before Disown reached it, or that was registered under an owner
// deleted right before. The report finds no thread, so the run is handed to
// the user where it ends and rings the project like the user's own.
func TestAnOwnedRunEndingAfterItsOwnerIsGoneRingsTheProject(t *testing.T) {
	f := newComposeFixture(t)
	gate := gatedDockerCLI(t)
	_, answer := f.compose(t, "up", true)
	id := answer["run"].(string)
	f.waitRun(t, id, func(v docker.RunView) bool { return v.Running })
	// The delete went through and Disown has not run yet.
	if err := f.s.assistants.Delete(f.owner); err != nil {
		t.Fatal(err)
	}
	if view := f.run(t, id); view.Owner != f.owner {
		t.Fatalf("the run lost its owner before its end: %+v", view)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	f.waitRun(t, id, func(v docker.RunView) bool { return !v.Running })
	deadline := time.Now().Add(5 * time.Second)
	for !f.notifier.UnreadTargets()[notify.DockerTarget("shop")] {
		if time.Now().After(deadline) {
			t.Fatal("the end of the orphaned run rang nobody")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if last, ok := f.s.docker.LastComposeRun("shop"); !ok || last.ID != id || last.Owner != "" {
		t.Fatalf("the project's news names %+v, want run %s", last, id)
	}
}

// A decline or a cancel of a parked run the user gave in the browser is
// written into the owner's thread and fires the compose-declined event, but
// rings nobody: it is news about the user's own click. An expiry, a decline
// nobody gave, still rings.
func TestTheUsersOwnDeclineRingsNobody(t *testing.T) {
	f := newComposeFixture(t)
	trigger, err := f.s.assistants.Events().Add(assistant.TriggerSpec{Owner: f.owner, Event: "compose-declined", Task: "say so"})
	if err != nil {
		t.Fatal(err)
	}
	rang := make(chan string, 8)
	f.s.assistants.SetHooks(func() {}, func(id string) { rang <- id })

	_, answer := f.compose(t, "down-volumes", true)
	denied := answer["run"].(string)
	q := f.waitQuestion(t)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(`{"key":"`+q.Key+`","id":"`+q.ID+`","approve":false}`)))
	if got := decodeJSON(t, rec); got["ok"] != true {
		t.Fatalf("the denial was refused: %v", got)
	}
	f.waitNoteOf(t, denied)
	f.waitNoQuestion(t, denied)

	_, answer = f.compose(t, "down-volumes", true)
	cancelled := answer["run"].(string)
	f.waitQuestion(t)
	rec = httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/shop/docker/runs/"+cancelled+"/stop", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the browser's cancel answered %d: %s", rec.Code, rec.Body.String())
	}
	f.waitNoteOf(t, cancelled)
	f.waitNoQuestion(t, cancelled)

	// The ring would come before the event, so both events standing means
	// every ring there was to be has been.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got, _ := f.s.assistants.Events().Get(trigger.ID); len(got.Pending) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the compose-declined events did not fire")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(rang) != 0 {
		t.Fatalf("the user's own decline rang %d times", len(rang))
	}

	_, answer = f.compose(t, "down-volumes", true)
	expired := answer["run"].(string)
	f.waitQuestion(t)
	f.s.declineParked(expired, "the approval expired unanswered", false)
	f.waitNoteOf(t, expired)
	select {
	case id := <-rang:
		if id != f.owner {
			t.Fatalf("the expiry rang %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an expired approval rang nobody")
	}
}

// Restoring a backup writes the settings the approvals and the compose
// commands stand on, so an import and a merge are the user's act and refused
// over the local socket like every other settings save.
func TestAnAssistantCannotRestoreABackup(t *testing.T) {
	f := newComposeFixture(t)
	f.router.POST("/settings/backup", f.s.handleSettingsBackupSave)
	f.router.POST("/settings/backup/merge", f.s.handleSettingsBackupMergeSave)
	for _, post := range []struct{ path, form string }{
		{"/settings/backup", "inspect"},
		{"/settings/backup", "apply"},
		{"/settings/backup", "review-keep"},
		{"/settings/backup/merge", "save"},
		{"/settings/backup/merge", "restore"},
	} {
		rec := f.localPost(t, post.path, f.owner, url.Values{"form": {post.form}, "id": {"x"}, "content": {"assistant-approval-compose-actions: off"}})
		if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != backupLocalRefusal {
			t.Fatalf("a local %s to %s answered %d: %s", post.form, post.path, rec.Code, rec.Body.String())
		}
	}
}

// A confirm action on a stack that already runs a command is refused at the
// start with the direct start's sentence, and no question comes up: the
// user's yes could only have ended in that same refusal.
func TestAParkOnABusyStackAsksNobody(t *testing.T) {
	f := newComposeFixture(t)
	gate := gatedDockerCLI(t)
	_, mine := f.compose(t, "up", false)
	userRun := mine["run"].(string)
	f.waitRun(t, userRun, func(v docker.RunView) bool { return v.Running })
	code, answer := f.compose(t, "down-volumes", true)
	if code != http.StatusConflict || answer["error"] != "a compose run is already under way here" {
		t.Fatalf("the park on a busy stack answered %d %v", code, answer)
	}
	time.Sleep(200 * time.Millisecond)
	if len(f.broker.Questions()) != 0 {
		t.Fatalf("a question came up for a stack that is busy: %v", f.broker.Questions())
	}
	for target := range f.notifier.UnreadTargets() {
		if strings.HasPrefix(target, "approval:") {
			t.Fatalf("the refused park left news %q", target)
		}
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	f.waitRun(t, userRun, func(v docker.RunView) bool { return !v.Running })
}

// A Cancel in the browser of an assistant's running run writes the note into
// the owner's thread and fires the event, but rings nobody: the user who
// clicked it knows. The same cancel given by the assistant still rings.
func TestTheUsersCancelOfARunningRunRingsNobody(t *testing.T) {
	f := newComposeFixture(t)
	gatedDockerCLI(t)
	trigger, err := f.s.assistants.Events().Add(assistant.TriggerSpec{Owner: f.owner, Event: "compose-failed", Task: "say so"})
	if err != nil {
		t.Fatal(err)
	}
	rang := make(chan string, 8)
	f.s.assistants.SetHooks(func() {}, func(id string) { rang <- id })

	_, answer := f.compose(t, "up", true)
	byUser := answer["run"].(string)
	f.waitRun(t, byUser, func(v docker.RunView) bool { return v.Running })
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/shop/docker/runs/"+byUser+"/stop", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the browser's cancel answered %d: %s", rec.Code, rec.Body.String())
	}
	f.waitNoteOf(t, byUser)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got, _ := f.s.assistants.Events().Get(trigger.ID); len(got.Pending) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the compose-failed event did not fire")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(rang) != 0 {
		t.Fatalf("the user's own cancel rang %d times", len(rang))
	}
	if view := f.run(t, byUser); !view.Cancelled || view.Running {
		t.Fatalf("the cancelled run reads %+v", view)
	}

	_, answer = f.compose(t, "up", true)
	byOwner := answer["run"].(string)
	f.waitRun(t, byOwner, func(v docker.RunView) bool { return v.Running })
	if rec := f.localPost(t, "/projects/shop/docker/runs/"+byOwner+"/stop", f.owner, nil); rec.Code != http.StatusOK {
		t.Fatalf("the owner's cancel answered %d: %s", rec.Code, rec.Body.String())
	}
	f.waitNoteOf(t, byOwner)
	select {
	case id := <-rang:
		if id != f.owner {
			t.Fatalf("the owner's cancel rang %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the owner's own cancel rang nobody")
	}
}

// Deleting an assistant declines its parked runs without one log line per
// run about a report that has no thread to go to: nobody waits for the word
// of a run that never started.
func TestDeletingTheAssistantDropsItsDeclinedReportsQuietly(t *testing.T) {
	f := newComposeFixture(t)
	_, answer := f.compose(t, "down-volumes", true)
	id := answer["run"].(string)
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
	f.waitNoQuestion(t, id)
	f.waitRun(t, id, func(v docker.RunView) bool { return v.Declined })
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(logged.String(), "dropping its report") {
		t.Fatalf("the declined run logged its dropped report: %s", logged.String())
	}
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }
