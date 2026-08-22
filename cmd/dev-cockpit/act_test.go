package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/web"
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
		_, _ = w.Write([]byte(`{"id":"cid-1","project":"demo","url":"/coders/cid-1"}`))
	})

	var out strings.Builder
	opts := inspectOptions{stateDir: dir, projectsDir: "/projects"}
	if err := runNewCoder(&out, opts, "demo", "readme-task", "claude", "", "Write the README.", ""); err != nil {
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
	if !strings.Contains(out.String(), "coder cid-1 started in demo, working on the task") {
		t.Fatalf("unexpected output %q", out.String())
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
	if err := runNewCoder(&out, opts, "demo", "readme-task", "", "", "Write the README.", "README.md exists"); err != nil {
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

// A coder that started but could not be steered is a failure of the command,
// and the output still says what runs.
func TestNewCoderReportsAJobThatCouldNotBeAttached(t *testing.T) {
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cid-1","project":"demo","url":"/coders/cid-1","steerError":"That terminal is already steered."}`))
	})

	var out strings.Builder
	err := runNewCoder(&out, inspectOptions{stateDir: dir, projectsDir: "/projects"}, "demo", "task", "", "", "", "README.md exists")
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
	err := runNewCoder(&out, inspectOptions{stateDir: dir, projectsDir: "/projects"}, "nope", "task", "", "", "", "")
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
		_, _ = w.Write([]byte(`{"name":"demo","path":"/projects/demo"}`))
	})

	var out strings.Builder
	if err := runNewProject(&out, inspectOptions{stateDir: dir}, "demo"); err != nil {
		t.Fatalf("project-new: %v", err)
	}
	if err := runDeleteProject(&out, inspectOptions{stateDir: dir}, "demo", true); err != nil {
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
	if err := runNewCoder(&out, opts, "demo", "readme-task", "", "", "a long briefing", "README.md exists"); err != nil {
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
				return runDeleteCoder(out, inspectOptions{stateDir: dir}, "abc", true)
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

// A refusal is the cockpit's sentence, not a status code: the caller has to be
// able to tell the user what is wrong with that session.
func TestSessionCommandsPassOnTheRefusal(t *testing.T) {
	dir := refusing(t, "That coder session is not resumable.")
	for name, run := range map[string]func(io.Writer) error{
		"resume": func(out io.Writer) error { return runResumeCoder(out, inspectOptions{stateDir: dir}, "abc") },
		"stop":   func(out io.Writer) error { return runStopCoder(out, inspectOptions{stateDir: dir}, "abc") },
		"delete": func(out io.Writer) error { return runDeleteCoder(out, inspectOptions{stateDir: dir}, "abc", true) },
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

// Deleting a project removes terminals and the whole directory, so it carries
// the same lock as coder-delete. Without --yes nothing leaves the machine.
func TestDeletingAProjectNeedsTheConfirmation(t *testing.T) {
	calls := 0
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"demo"}`))
	})

	var out strings.Builder
	err := runDeleteProject(&out, inspectOptions{stateDir: dir}, "demo", false)
	if err == nil {
		t.Fatal("want an error without the confirmation")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("the error has to name what is missing, got %q", err.Error())
	}
	if calls != 0 {
		t.Fatalf("want no request at all, got %d", calls)
	}
}

// Deleting cannot be undone, so the call has to say so. Without --yes nothing
// leaves the machine at all.
func TestDeletingACoderNeedsTheConfirmation(t *testing.T) {
	calls := 0
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	var out strings.Builder
	err := runDeleteCoder(&out, inspectOptions{stateDir: dir}, "abc", false)
	if err == nil {
		t.Fatal("want an error without the confirmation")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("the error has to name what is missing, got %q", err.Error())
	}
	if calls != 0 {
		t.Fatalf("want no request at all, got %d", calls)
	}
}
