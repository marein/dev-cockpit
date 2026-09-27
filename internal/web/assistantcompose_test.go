package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	s.approvals = s.newApprovals(stateDir)
	s.approvals.SetBroker(broker)
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
// about: looking at an assistant's run, which its note links at, leaves the
// user's own unread news standing.
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

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

// The assistant's reading carries each command's intent and each stack's
// addresses, routes first; the end of an owned run names those addresses
// only for a start or a restart that went through, read once as the cache
// stands.
func TestComposeReadingsCarryIntentAndAddresses(t *testing.T) {
	f := newComposeFixture(t)
	f.s.docker.SetStateForTest(docker.State{Available: true, Host: "tcp://fake:1", Containers: []docker.Container{
		{Name: "db", Service: "db", State: "running", WorkingDir: f.dir, Ports: []docker.Port{{Public: 5432, Private: 5432}}},
		{Name: "web", Service: "web", State: "running", WorkingDir: f.dir,
			Labels: map[string]string{"traefik.http.routers.web.rule": "Host(`shop.example.com`)"}},
	}})
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/shop/docker", nil))
	list := decodeJSON(t, rec)
	stacks, _ := list["stacks"].([]any)
	if len(stacks) != 1 {
		t.Fatalf("the list reads %v", list)
	}
	if links, _ := stacks[0].(map[string]any)["links"].([]any); len(links) != 2 || links[0] != "shop.example.com" || links[1] != ":5432" {
		t.Fatalf("the stack's addresses read %v", stacks[0])
	}
	actions, _ := list["actions"].([]any)
	var intents []string
	for _, raw := range actions {
		intents = append(intents, raw.(map[string]any)["intent"].(string))
	}
	if strings.Join(intents, " ") != "start stop build purge" {
		t.Fatalf("the intents read %v", intents)
	}

	run := docker.ComposeRun{ID: "r", Dir: f.dir, Label: "shop", Action: "Compose up", Owner: f.owner, Intent: "start"}
	want := []assistant.ComposeLink{{Address: "shop.example.com", URL: "//shop.example.com"}, {Address: ":5432", URL: "http://:5432"}}
	if got := f.s.composeReport(run, nil, "").Links; !reflect.DeepEqual(got, want) {
		t.Fatalf("a start that went through names %v", got)
	}
	run.Intent = "restart"
	if got := f.s.composeReport(run, nil, "").Links; len(got) != 2 {
		t.Fatalf("a restart that went through names %v", got)
	}
	for _, other := range []docker.ComposeRun{
		{ID: "r", Dir: f.dir, Label: "shop", Action: "Compose down", Owner: f.owner, Intent: "stop"},
		{ID: "r", Dir: f.dir, Label: "shop", Action: "Compose build", Owner: f.owner, Intent: "build"},
		{ID: "r", Dir: f.dir, Label: "shop", Action: "Compose up", Owner: f.owner, Intent: "start", Failed: true},
	} {
		if got := f.s.composeReport(other, nil, "").Links; len(got) != 0 {
			t.Fatalf("%+v names %v", other, got)
		}
	}
	f.s.docker.SetStateForTest(docker.State{Available: true, Host: "tcp://fake:1"})
	if got := f.s.composeReport(run, nil, "").Links; len(got) != 0 {
		t.Fatalf("a stack without addresses names %v", got)
	}
}

// The containers of an up -d reach the cache only with the reading after the
// event burst settled, a moment after the run ended: the note waits for that
// reading and names what it brought, routes first and ports after.
func TestAStartNamesTheAddressesOfTheReadingAfterItsEnd(t *testing.T) {
	f := newComposeFixture(t)
	f.s.docker.SetStateForTest(docker.State{Available: true, Host: "tcp://fake:1"})
	run := docker.ComposeRun{ID: "r", Dir: f.dir, Label: "shop", Action: "Compose up", Owner: f.owner, Intent: "start", Reloads: f.s.docker.Reloads()}
	go func() {
		time.Sleep(300 * time.Millisecond)
		f.s.docker.SetStateForTest(docker.State{Available: true, Host: "tcp://fake:1", Containers: []docker.Container{
			{Name: "web", Service: "web", State: "running", WorkingDir: f.dir, Ports: []docker.Port{{Public: 8080, Private: 80}},
				Labels: map[string]string{"traefik.http.routers.web.rule": "Host(`shop.example.com`)"}},
		}})
	}()
	started := time.Now()
	f.s.composeDone(run, nil, "")
	current, err := f.s.assistants.Get(f.owner)
	if err != nil || len(current.Messages) == 0 {
		t.Fatalf("no note in the thread: %v", err)
	}
	note := current.Messages[len(current.Messages)-1].Content
	if want := "Answers on: [shop.example.com](<//shop.example.com>), [:8080](<http://:8080>)"; !strings.HasSuffix(note, want) {
		t.Fatalf("the note reads:\n%s\nwant it to end in:\n%s", note, want)
	}
	// The route is a real link, the port one the page completes with its host.
	html := string(f.s.assistantMarkdown(f.owner, note))
	if !strings.Contains(html, `<a href="//shop.example.com">shop.example.com</a>`) || !strings.Contains(html, `<a href="http://:8080">:8080</a>`) {
		t.Fatalf("the note renders:\n%s", html)
	}
	if waited := time.Since(started); waited >= composeLinksWait {
		t.Fatalf("the note waited %s, the whole bound, for a reading that came", waited)
	}
}

// A reading that does not come within the bound leaves the note without
// addresses, and the wait ends at the bound.
func TestAStartWithoutAReadingNamesNothingAfterTheBound(t *testing.T) {
	f := newComposeFixture(t)
	f.s.docker.SetStateForTest(docker.State{Available: true, Host: "tcp://fake:1"})
	run := docker.ComposeRun{ID: "r", Dir: f.dir, Label: "shop", Action: "Compose up", Owner: f.owner, Intent: "start", Reloads: f.s.docker.Reloads()}
	started := time.Now()
	if got := f.s.composeReport(run, nil, "").Links; len(got) != 0 {
		t.Fatalf("the start names %v", got)
	}
	if waited := time.Since(started); waited < composeLinksWait || waited > composeLinksWait+time.Second {
		t.Fatalf("the note waited %s, the bound is %s", waited, composeLinksWait)
	}
}

// An assistant stops its own runs and nobody else's: the user's run and
// another assistant's are refused with a sentence naming whose they are, a
// call without an id is refused, and the browser stops any of them.
func TestAnAssistantStopsOnlyItsOwnRuns(t *testing.T) {
	f := newComposeFixture(t)
	gate := gatedDockerCLI(t)
	other, err := f.s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	_, started := f.compose(t, "up", true)
	id := started["run"].(string)
	f.waitRun(t, id, func(v docker.RunView) bool { return v.Running })
	stop := "/projects/shop/docker/runs/" + id + "/stop"

	rec := f.localPost(t, stop, other.ID, nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(decodeJSON(t, rec)["error"].(string), "belongs to "+f.s.assistantName(f.owner)) {
		t.Fatalf("another assistant's stop answered %d: %s", rec.Code, rec.Body.String())
	}
	rec = f.localPost(t, stop, "", nil)
	if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != assistantCallerRefusal {
		t.Fatalf("a stop without an id answered %d: %s", rec.Code, rec.Body.String())
	}
	if view := f.run(t, id); !view.Running || view.Cancelled {
		t.Fatalf("a refused stop moved the run: %+v", view)
	}
	rec = f.localPost(t, stop, f.owner, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the owner's stop answered %d: %s", rec.Code, rec.Body.String())
	}
	f.waitNoteOf(t, id)

	_, mine := f.compose(t, "up", false)
	userRun := mine["run"].(string)
	rec = f.localPost(t, "/projects/shop/docker/runs/"+userRun+"/stop", f.owner, nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(decodeJSON(t, rec)["error"].(string), "belongs to the user") {
		t.Fatalf("the assistant's stop of the user's run answered %d: %s", rec.Code, rec.Body.String())
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	f.waitRun(t, userRun, func(v docker.RunView) bool { return !v.Running })
}
