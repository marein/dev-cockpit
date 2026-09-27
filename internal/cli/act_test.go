package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/web"
	"github.com/spf13/cobra"
)

// cockpit stands in for a running server: it answers on the local socket of a
// state directory, which is how every one of these commands reaches the real
// one. Returns the state directory to point a command at.
func cockpit(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	dir := t.TempDir()
	listener, err := localapi.Listen(dir)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return dir
}

// refusing is a cockpit that answers everything with one sentence, the way a
// handler refuses a browser.
func refusing(t *testing.T, message string) string {
	t.Helper()
	return cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": message})
	})
}

// answerInput answers the way handleCoderInput really does. The fake used to
// invent a JSON answer while the real handler sent plain text, and that hid
// that every successful coder-send-prompt exited 1; replaying the handler's own value keeps
// this test honest.
func answerInput(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(web.CoderInputAnswer)
}

// coder-send-prompt goes through the running cockpit, so the test is about the request it
// makes: the right route and the payload the input handler expects.
func TestSendTypesIntoACoder(t *testing.T) {
	var gotPath, gotBody string
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.URL.Path, string(body)
		answerInput(w)
	})

	var out strings.Builder
	if err := runSend(&out, inspectOptions{stateDir: dir}, "abc", "hello there"); err != nil {
		t.Fatalf("coder-send-prompt: %v", err)
	}
	if gotPath != "/coders/abc/input" {
		t.Fatalf("want the coder input route, got %q", gotPath)
	}
	var payload struct {
		Items []map[string]string `json:"items"`
	}
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0]["prompt"] != "hello there" {
		t.Fatalf("unexpected payload %s", gotBody)
	}
	if !strings.Contains(out.String(), "sent to abc") {
		t.Fatalf("unexpected output %q", out.String())
	}
}

// An id that is not a coder is a refusal, and the caller reads the sentence the
// handler wrote instead of a status code.
func TestSendReportsTheRefusal(t *testing.T) {
	dir := refusing(t, "Refusing to interact with a tmux session that is not associated with a coder.")
	var out strings.Builder
	err := runSend(&out, inspectOptions{stateDir: dir}, "shell-id", "ls")
	if err == nil || !strings.Contains(err.Error(), "not associated with a coder") {
		t.Fatalf("want the handler's own sentence, got %v", err)
	}
	if out.String() != "" {
		t.Fatalf("a refused call must not report success, got %q", out.String())
	}
}

func TestSendWithoutARunningCockpit(t *testing.T) {
	// Dial waits a budget out for a cockpit that is restarting; without the
	// override this test would sleep the whole budget for its refusal.
	t.Setenv("DEV_COCKPIT_DIAL_BUDGET", "0s")
	var out strings.Builder
	if err := runSend(&out, inspectOptions{stateDir: t.TempDir()}, "abc", "hello"); err == nil {
		t.Fatal("want an error when no cockpit is running")
	}
}

// Starting a coder posts the same form the browser posts and reads the
// identifier out of the JSON a local caller gets. The task travels with that
// one request, into the CLI's argv, so nothing is typed into a pane afterwards.
func TestNewCoderStartsWithTheTaskInOneRequest(t *testing.T) {
	var createForm url.Values
	var paths []string
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		paths = append(paths, r.URL.Path)
		createForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cid-1","project":"demo","url":"/coders/cid-1","model":"haiku"}`))
	})

	var out strings.Builder
	opts := inspectOptions{stateDir: dir, projectsDir: "/projects"}
	if err := runNewCoder(&out, opts, "demo", "readme-task", "claude", "", "haiku", "Write the README.", "", ""); err != nil {
		t.Fatalf("coder-new: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/coders/new" {
		t.Fatalf("want one create request, got %v", paths)
	}
	if createForm.Get("project") != "/projects/demo" {
		t.Fatalf("want the project resolved against the projects root, got %q", createForm.Get("project"))
	}
	if createForm.Get("name") != "readme-task" || createForm.Get("coder") != "claude" {
		t.Fatalf("unexpected form %v", createForm)
	}
	if createForm.Get("automatic_approval") != "on" {
		t.Fatal("a coder the assistant starts has to run without asking for approvals")
	}
	if createForm.Get("prompt") != "Write the README." {
		t.Fatalf("the task did not travel with the create request: %q", createForm.Get("prompt"))
	}
	if createForm.Get("model") != "haiku" {
		t.Fatalf("the model did not travel with the create request as the dialog's field: %q", createForm.Get("model"))
	}
	if !strings.Contains(out.String(), "coder cid-1 started in demo on haiku, working on the task") {
		t.Fatalf("unexpected output %q", out.String())
	}
}

// The started line names the model only when the call passed one that made a
// difference, and then the one the cockpit answered, so what a turn reports
// to the user is what runs: a call without --model says nothing about models
// even where a stored default started the session, one that named the start
// default anyway reads as if it had passed nothing, and one whose answer
// carries no model falls back to the name it passed.
func TestTheStartedLineNamesAPassedModelAsAnswered(t *testing.T) {
	for _, tc := range []struct {
		model, prompt string
		created       map[string]any
		want          string
	}{
		{"", "", map[string]any{"model": "haiku", "modelDefault": "haiku"}, "coder cid-1 started in demo\n"},
		{"", "task", map[string]any{"model": "haiku"}, "coder cid-1 started in demo, working on the task\n"},
		{"haiku", "task", map[string]any{"model": "haiku", "modelDefault": ""}, "coder cid-1 started in demo on haiku, working on the task\n"},
		{"haiku", "task", map[string]any{"model": "haiku", "modelDefault": "opus"}, "coder cid-1 started in demo on haiku, working on the task\n"},
		{"haiku", "task", map[string]any{"model": "haiku", "modelDefault": "haiku"}, "coder cid-1 started in demo, working on the task\n"},
		{"haiku", "", map[string]any{}, "coder cid-1 started in demo on haiku\n"},
	} {
		if got := startedLine("cid-1", "demo", tc.model, tc.prompt, tc.created); got != tc.want {
			t.Fatalf("startedLine(model %q, prompt %q, %v) = %q, want %q", tc.model, tc.prompt, tc.created, got, tc.want)
		}
	}
}

// Starting and steering is one request: the criterion travels in the create
// form, the server checks it before the session exists, and the answer says
// that the job hangs on the coder. Two calls used to leave a running coder
// without its job whenever the second one was refused.
func TestNewCoderSteersInTheSameRequest(t *testing.T) {
	var paths []string
	var form url.Values
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		paths = append(paths, r.URL.Path)
		form, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cid-1","project":"demo","url":"/coders/cid-1","maxWakes":10}`))
	})

	var out strings.Builder
	opts := inspectOptions{stateDir: dir, projectsDir: "/projects"}
	if err := runNewCoder(&out, opts, "demo", "readme-task", "", "", "", "Write the README.", "README.md exists", ""); err != nil {
		t.Fatalf("coder-new: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/coders/new" {
		t.Fatalf("want one create request carrying the job, got %v", paths)
	}
	if form.Get("done_when") != "README.md exists" {
		t.Fatalf("the criterion did not travel with the create request: %q", form.Get("done_when"))
	}
	if !strings.Contains(out.String(), "steering it, 10 checks at most") {
		t.Fatalf("the output has to say the coder is steered, got %q", out.String())
	}
}

// The sequel is wired in the same request as the job it hangs on: nothing
// stands between the coder starting and the arrangement standing, so a job
// that is done in a minute cannot outrun it. The answer names the trigger,
// which is how a turn can drop it again.
func TestNewCoderWiresTheSequelInTheSameRequest(t *testing.T) {
	var paths []string
	var form url.Values
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		paths = append(paths, r.URL.Path)
		form, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cid-1","project":"demo","url":"/coders/cid-1","maxWakes":10,"trigger":"9f2c0a1b7d3e4f50"}`))
	})

	var out strings.Builder
	opts := inspectOptions{stateDir: dir, projectsDir: "/projects"}
	if err := runNewCoder(&out, opts, "demo", "readme-task", "", "", "", "Write the README.", "README.md exists", "start a reviewer on demo"); err != nil {
		t.Fatalf("coder-new: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/coders/new" {
		t.Fatalf("want one create request carrying job and sequel, got %v", paths)
	}
	if form.Get("then") != "start a reviewer on demo" {
		t.Fatalf("the sequel did not travel with the create request: %q", form.Get("then"))
	}
	if !strings.Contains(out.String(), "trigger 9f2c0a1b7d3e4f50 fires once when that job is done") {
		t.Fatalf("the output has to name the trigger, got %q", out.String())
	}
}

// A sequel the server refused is a failure of the command: the coder runs and
// the job stands, but nobody would carry the work on.
func TestNewCoderReportsASequelThatCouldNotBeWired(t *testing.T) {
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cid-1","project":"demo","url":"/coders/cid-1","maxWakes":10,"thenError":"This assistant already holds 50 standing triggers. Remove one first."}`))
	})

	var out strings.Builder
	err := runNewCoder(&out, inspectOptions{stateDir: dir, projectsDir: "/projects"}, "demo", "task", "", "", "", "", "README.md exists", "start a reviewer")
	if err == nil || !strings.Contains(err.Error(), "already holds 50") {
		t.Fatalf("want the server's own sentence as the error, got %v", err)
	}
	if !strings.Contains(out.String(), "steering it") {
		t.Fatalf("the output has to say the job stands either way, got %q", out.String())
	}
}

// A coder that started but could not be steered is a failure of the command,
// and the output still says what runs.
func TestNewCoderReportsAJobThatCouldNotBeAttached(t *testing.T) {
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cid-1","project":"demo","url":"/coders/cid-1","steerError":"That terminal is already steered."}`))
	})

	var out strings.Builder
	err := runNewCoder(&out, inspectOptions{stateDir: dir, projectsDir: "/projects"}, "demo", "task", "", "", "", "", "README.md exists", "")
	if err == nil || !strings.Contains(err.Error(), "already steered") {
		t.Fatalf("want the server's own sentence as the error, got %v", err)
	}
	if !strings.Contains(out.String(), "coder cid-1 started") {
		t.Fatalf("the output has to say the coder runs either way, got %q", out.String())
	}
}

// A refusal from the create handler is the sentence the page would have
// flashed, not an HTTP status the user has to interpret.
func TestNewCoderReportsTheRefusal(t *testing.T) {
	dir := refusing(t, "Selected project does not exist: /projects/nope")
	var out strings.Builder
	err := runNewCoder(&out, inspectOptions{stateDir: dir, projectsDir: "/projects"}, "nope", "task", "", "", "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("want the handler's own sentence, got %v", err)
	}
}

func TestProjectCommandsPostTheBrowserForms(t *testing.T) {
	var paths []string
	var forms []url.Values
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		paths = append(paths, r.URL.Path)
		forms = append(forms, form)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"demo","path":"/projects/demo","worktrees":["demo-feature"]}`))
	})

	var out strings.Builder
	if err := runNewProject(&out, inspectOptions{stateDir: dir}, "demo"); err != nil {
		t.Fatalf("project-new: %v", err)
	}
	if err := runDeleteProject(&out, inspectOptions{stateDir: dir}, "demo"); err != nil {
		t.Fatalf("project-delete: %v", err)
	}
	if paths[0] != "/projects" || forms[0].Get("project_name") != "demo" {
		t.Fatalf("unexpected create request %q %v", paths[0], forms[0])
	}
	if paths[1] != "/projects/delete" || forms[1].Get("project") != "demo" {
		t.Fatalf("unexpected delete request %q %v", paths[1], forms[1])
	}
	if !strings.Contains(out.String(), "created at /projects/demo") || !strings.Contains(out.String(), "deleted") {
		t.Fatalf("unexpected output %q", out.String())
	}
	if !strings.Contains(out.String(), "project demo deleted\n") || !strings.Contains(out.String(), "its worktree projects went with it: demo-feature") {
		t.Fatalf("the cascade is not named in the output %q", out.String())
	}
}

// A job belongs to the assistant, not to a conversation, so steering posts to
// the one jobs route with the criterion that decides done.
func TestSteerPostsToTheJobsRoute(t *testing.T) {
	var path string
	var form url.Values
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		path = r.URL.Path
		form, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"terminal":"term-1","name":"readme-task","maxWakes":10}`))
	})

	var out strings.Builder
	if err := runSteer(&out, inspectOptions{stateDir: dir}, "term-1", "Write the README", "README.md exists"); err != nil {
		t.Fatalf("coder-steer: %v", err)
	}
	if path != assistantJobsPath {
		t.Fatalf("want the jobs route, got %q", path)
	}
	if form.Get("form") != "steer" || form.Get("terminal") != "term-1" || form.Get("done_when") != "README.md exists" {
		t.Fatalf("unexpected form %v", form)
	}
	if !strings.Contains(out.String(), "steering readme-task") {
		t.Fatalf("unexpected output %q", out.String())
	}
}

// A task over its bound was stored cut, the answer says so in a notice, and
// both commands that can carry a task, coder-steer and coder-new, hand that
// sentence on. Swallowed here, the cut would stay invisible.
func TestSteerAndNewCoderHandOnTheTaskCutNotice(t *testing.T) {
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cid-1","terminal":"term-1","name":"readme-task","url":"/coders/cid-1","maxWakes":10,"notice":"task was cut at 16000 runes"}`))
	})

	var out strings.Builder
	if err := runSteer(&out, inspectOptions{stateDir: dir}, "term-1", "a long briefing", "README.md exists"); err != nil {
		t.Fatalf("coder-steer: %v", err)
	}
	if !strings.Contains(out.String(), "task was cut at 16000 runes") {
		t.Fatalf("the coder-steer output has to carry the notice, got %q", out.String())
	}

	out.Reset()
	opts := inspectOptions{stateDir: dir, projectsDir: "/projects"}
	if err := runNewCoder(&out, opts, "demo", "readme-task", "", "", "", "a long briefing", "README.md exists", ""); err != nil {
		t.Fatalf("coder-new: %v", err)
	}
	if !strings.Contains(out.String(), "task was cut at 16000 runes") {
		t.Fatalf("the coder-new output has to carry the notice, got %q", out.String())
	}
}

// A dialog takes keys. The command has to put them all into one request, in the
// order they were given, so nothing of the person's own typing can land between
// two of them and change what is chosen.
func TestKeysPressesEachKeyInOneRequest(t *testing.T) {
	var gotPath, gotBody string
	calls := 0
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.URL.Path, string(body)
		calls++
		answerInput(w)
	})

	var out strings.Builder
	if err := runKeys(&out, inspectOptions{stateDir: dir}, "abc", []string{"arrow-down", "arrow-down", "enter"}); err != nil {
		t.Fatalf("coder-send-control-keys: %v", err)
	}
	if calls != 1 {
		t.Fatalf("want one request for the whole sequence, got %d", calls)
	}
	if gotPath != "/coders/abc/input" {
		t.Fatalf("want the coder input route, got %q", gotPath)
	}
	var payload struct {
		Items []map[string]string `json:"items"`
	}
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if len(payload.Items) != 3 {
		t.Fatalf("want three keys, got %s", gotBody)
	}
	for i, want := range []string{"arrow-down", "arrow-down", "enter"} {
		if payload.Items[i]["control"] != want {
			t.Fatalf("key %d is %s, want %q", i, gotBody, want)
		}
		if payload.Items[i]["prompt"] != "" {
			t.Fatalf("a key must not travel as a prompt: %s", gotBody)
		}
	}
	if !strings.Contains(out.String(), "arrow-down arrow-down enter") {
		t.Fatalf("the output has to say what was pressed, got %q", out.String())
	}
}

// Without a key there is nothing to press, and an empty batch would reach the
// cockpit as an input the handler cannot read.
func TestKeysWithoutAKeyIsRefused(t *testing.T) {
	var out strings.Builder
	if err := runKeys(&out, inspectOptions{stateDir: t.TempDir()}, "abc", []string{" "}); err == nil {
		t.Fatal("want an error when no key was named")
	}
}

// Resuming, stopping and deleting go through the running server like every
// other action: one request to the route the button uses, and the identifier
// comes back out of the JSON instead of out of a redirect.
func TestSessionCommandsPostOneRequestEach(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		body map[string]any
		run  func(out *strings.Builder, dir string) error
		want string
	}{
		{
			name: "resume",
			path: "/coders/abc/resume",
			body: map[string]any{"id": "abc", "name": "readme-task", "project": "demo"},
			run: func(out *strings.Builder, dir string) error {
				return runResumeCoder(out, inspectOptions{stateDir: dir}, "abc")
			},
			want: "coder abc resumed in demo",
		},
		{
			name: "stop",
			path: "/coders/abc/stop",
			body: map[string]any{"id": "abc", "name": "readme-task", "project": "demo"},
			run: func(out *strings.Builder, dir string) error {
				return runStopCoder(out, inspectOptions{stateDir: dir}, "abc")
			},
			want: "coder readme-task stopped, it can be resumed",
		},
		{
			name: "delete",
			path: "/coders/abc/delete",
			body: map[string]any{"id": "abc", "name": "readme-task", "project": "demo"},
			run: func(out *strings.Builder, dir string) error {
				return runDeleteCoder(out, inspectOptions{stateDir: dir}, "abc")
			},
			want: "coder readme-task deleted",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tc.body)
			})
			var out strings.Builder
			if err := tc.run(&out, dir); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if len(paths) != 1 || paths[0] != "POST "+tc.path {
				t.Fatalf("want one POST to %s, got %v", tc.path, paths)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("want %q in the output, got %q", tc.want, out.String())
			}
		})
	}
}

// The dropped note is the server's and stands only where something went: a
// plain deletion ends at "deleted". The answer's field was read through
// text(), which spells an absent value as a question mark, and every plain
// deletion printed "deleted, ?".
func TestDeleteOutputCarriesTheDroppedNoteOnlyWhereOneIs(t *testing.T) {
	for _, tc := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"id": "abc", "name": "readme-task", "project": "demo"}, "coder readme-task deleted\n"},
		{map[string]any{"id": "abc", "name": "readme-task", "project": "demo", "dropped": "1 trigger dropped"}, "coder readme-task deleted, 1 trigger dropped\n"},
	} {
		dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(tc.body)
		})
		var out strings.Builder
		if err := runDeleteCoder(&out, inspectOptions{stateDir: dir}, "abc"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if out.String() != tc.want {
			t.Fatalf("want %q, got %q", tc.want, out.String())
		}
	}
}

// The projects root of the flag spells the home as `~`, which the server
// expands for its own flag and never for a path it is handed. A name is
// therefore joined onto the expanded root, or it travels as `~/projects/<name>`
// and the server answers that no such project exists. An absolute path and an
// empty one pass as they are.
func TestProjectPathExpandsTheHomeOfTheRoot(t *testing.T) {
	t.Setenv("HOME", "/home/probe")
	for _, tc := range []struct{ root, project, want string }{
		{"~/projects", "demo", "/home/probe/projects/demo"},
		{"/srv/projects", " demo ", "/srv/projects/demo"},
		{"~/projects", "/elsewhere/demo", "/elsewhere/demo"},
		{"~/projects", "", ""},
	} {
		if got := projectPath(tc.root, tc.project); got != tc.want {
			t.Fatalf("projectPath(%q, %q) = %q, want %q", tc.root, tc.project, got, tc.want)
		}
	}
}

// A refusal is the cockpit's sentence, not a status code: the caller has to be
// able to tell the user what is wrong with that session.
func TestSessionCommandsPassOnTheRefusal(t *testing.T) {
	dir := refusing(t, "That coder session is not resumable.")
	for name, run := range map[string]func(io.Writer) error{
		"resume": func(out io.Writer) error { return runResumeCoder(out, inspectOptions{stateDir: dir}, "abc") },
		"stop":   func(out io.Writer) error { return runStopCoder(out, inspectOptions{stateDir: dir}, "abc") },
		"delete": func(out io.Writer) error { return runDeleteCoder(out, inspectOptions{stateDir: dir}, "abc") },
	} {
		var out strings.Builder
		err := run(&out)
		if err == nil {
			t.Fatalf("%s: want the refusal as an error", name)
		}
		if !strings.Contains(err.Error(), "not resumable") {
			t.Fatalf("%s: want the cockpit's sentence, got %q", name, err.Error())
		}
		if out.String() != "" {
			t.Fatalf("%s: a refused call must not report success, got %q", name, out.String())
		}
	}
}

// activity asks the running cockpit, because which coder owns a session and
// how its record is read live there. The test is about the request and about
// the reading being presented for what it is.
func TestActivityReadsTheSessionsOwnRecord(t *testing.T) {
	var gotPath, gotQuery string
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"user: write the README\ncoder: The README is written.","finished":true,"screen":false}`))
	})

	var out strings.Builder
	if err := runActivity(&out, inspectOptions{stateDir: dir}, "abc", 5, false); err != nil {
		t.Fatalf("coder-activity: %v", err)
	}
	if gotPath != "/coders/abc/activity" {
		t.Fatalf("want the activity route, got %q", gotPath)
	}
	if gotQuery != "entries=5" {
		t.Fatalf("want the entries in the query, got %q", gotQuery)
	}
	if err := runActivity(&out, inspectOptions{stateDir: dir}, "abc", 5, true); err != nil {
		t.Fatalf("coder-activity --full: %v", err)
	}
	if gotQuery != "entries=5&full=1" {
		t.Fatalf("want the full flag in the query, got %q", gotQuery)
	}
	for _, want := range []string{"its turn is over", "coder: The README is written."} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("coder-activity output is missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "screen") {
		t.Fatalf("a recorded reading must not warn about the screen:\n%s", out.String())
	}
}

// A reading that fell back to the screen says so: the text carries the coder's
// input line, and reading the draft there as a message is the mistake the
// whole command exists to avoid.
func TestActivitySaysWhenTheReadingIsTheScreen(t *testing.T) {
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"the whole terminal picture","finished":false,"screen":true}`))
	})

	var out strings.Builder
	if err := runActivity(&out, inspectOptions{stateDir: dir}, "abc", 0, false); err != nil {
		t.Fatalf("coder-activity: %v", err)
	}
	for _, want := range []string{"this is its screen", "coder's own draft", "the whole terminal picture"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("coder-activity output is missing %q:\n%s", want, out.String())
		}
	}
}

// Deleting a project waits for the user's approval instead of a flag the
// caller sets itself: the command posts at once and a delete that waits says
// so.
func TestDeletingAProjectWaitsForTheApproval(t *testing.T) {
	calls := 0
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pending":true,"what":"Delete project demo"}`))
	})

	var out strings.Builder
	if err := runDeleteProject(&out, inspectOptions{stateDir: dir}, "demo"); err != nil {
		t.Fatalf("project-delete: %v", err)
	}
	if calls != 1 {
		t.Fatalf("want one request, got %d", calls)
	}
	for _, want := range []string{"Delete project demo waits for the user's approval", "do not run it again"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("the output misses %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "deleted\n") {
		t.Fatalf("a waiting delete reads as done:\n%s", out.String())
	}
}

// Deleting a coder or an assistant waits for the user's approval the way a
// project delete does: the command posts at once, and a delete that waits says
// so instead of reading as done.
func TestDeletingACoderOrAnAssistantWaitsForTheApproval(t *testing.T) {
	for _, tc := range []struct {
		name, path, what string
		run              func(io.Writer, inspectOptions) error
	}{
		{"coder-delete", "/coders/abc/delete", "Delete coder readme-task",
			func(out io.Writer, opts inspectOptions) error { return runDeleteCoder(out, opts, "abc") }},
		{"assistant-delete", "/assistants/abc", "Delete assistant Helper",
			func(out io.Writer, opts inspectOptions) error { return runDeleteAssistant(out, opts, "abc") }},
	} {
		var paths []string
		dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"pending": true, "what": tc.what})
		})
		var out strings.Builder
		if err := tc.run(&out, inspectOptions{stateDir: dir}); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(paths) != 1 || paths[0] != tc.path {
			t.Fatalf("%s: want one request to %s, got %v", tc.name, tc.path, paths)
		}
		if out.String() != tc.what+" waits for the user's approval. A note lands in your thread once they decide; do not run it again.\n" {
			t.Fatalf("%s: the output reads %q", tc.name, out.String())
		}
	}
}

// The --yes the delete commands took before the approval stays parseable, so a
// call written for them still runs: it is ignored, the request and the output
// are the ones without it, the deprecation notice goes to stderr alone, and no
// help offers it. The command writes to the process streams the way the binary
// does, so the test reads those rather than setting a writer of its own, which
// cobra would hand the notice to as well.
func TestTheRetiredYesIsAcceptedAndIgnored(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		cmd        func(*inspectOptions) *cobra.Command
	}{
		{"coder-delete", "/coders/abc/delete", newDeleteCoderCommand},
		{"assistant-delete", "/assistants/abc", newDeleteAssistantCommand},
		{"project-delete", "/projects/delete", newDeleteProjectCommand},
	} {
		type call struct{ path, body string }
		run := func(args ...string) ([]call, string, string) {
			var calls []call
			dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				calls = append(calls, call{r.URL.Path, r.PostForm.Encode()})
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"pending":true,"what":"Delete it"}`))
			})
			cmd := tc.cmd(&inspectOptions{stateDir: dir})
			cmd.SetArgs(args)
			stdout, stderr := captureStreams(t, func() {
				if err := cmd.Execute(); err != nil {
					t.Fatalf("%s %v: %v", tc.name, args, err)
				}
			})
			return calls, stdout, stderr
		}
		plainCalls, plainOut, plainErr := run("abc")
		if plainErr != "" {
			t.Fatalf("%s: without --yes stderr reads %q", tc.name, plainErr)
		}
		for _, args := range [][]string{{"abc", "--yes"}, {"--yes", "abc"}, {"abc", "--yes=false"}} {
			calls, out, errOut := run(args...)
			if len(calls) != 1 || calls[0].path != tc.path || calls[0] != plainCalls[0] {
				t.Fatalf("%s %v: want the request %v, got %v", tc.name, args, plainCalls, calls)
			}
			if out != plainOut {
				t.Fatalf("%s %v: the output reads %q, without --yes %q", tc.name, args, out, plainOut)
			}
			if errOut != "Flag --yes has been deprecated, the user's approval replaced it\n" {
				t.Fatalf("%s %v: stderr reads %q", tc.name, args, errOut)
			}
		}

		cmd := tc.cmd(&inspectOptions{})
		flag := cmd.Flags().Lookup("yes")
		if flag == nil || !flag.Hidden || flag.Deprecated != "the user's approval replaced it" {
			t.Fatalf("%s: want --yes hidden and deprecated, got %+v", tc.name, flag)
		}
		var help strings.Builder
		cmd.SetOut(&help)
		cmd.SetArgs([]string{"--help"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s --help: %v", tc.name, err)
		}
		if strings.Contains(help.String(), "--yes") {
			t.Fatalf("%s: the help offers --yes:\n%s", tc.name, help.String())
		}
	}
}

// captureStreams runs fn with the process's stdout and stderr pointed at
// files of its own and answers what each received.
func captureStreams(t *testing.T, fn func()) (string, string) {
	t.Helper()
	dir := t.TempDir()
	open := func(name string) *os.File {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	outFile, errFile := open("stdout"), open("stderr")
	defer outFile.Close()
	defer errFile.Close()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outFile, errFile
	func() {
		defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
		fn()
	}()
	read := func(f *os.File) string {
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return read(outFile), read(errFile)
}
