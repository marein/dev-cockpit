package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	coderclaude "github.com/marein/dev-cockpit/internal/coder/claude"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/internal/restore"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// steerCreateServer is a server that can really start a coder: tmux is a stub
// that takes every call, so the create runs its whole way through to the job
// instead of stopping at the session. HOME is a scratch directory, the trust
// flag a start writes goes there and nowhere near the one running this.
func steerCreateServer(t *testing.T) (*Server, *assistant.Jobs, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("stub tmux: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	work := filepath.Join(root, "app")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("project: %v", err)
	}
	projects := project.NewRepository(root, recent.New(filepath.Join(t.TempDir(), "recent.json")))
	shells := shell.NewShells(config.Config{}, tmux.New(), projects, nil)

	stateDir := t.TempDir()
	conversations, _, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	jobs := assistant.NewJobs(assistant.NewStore(stateDir))
	s := &Server{
		coders:     []*coder.Manager{coder.NewManager(config.Config{}, tmux.New(), coderclaude.New("", nil), projects)},
		projects:   projects,
		shells:     shells,
		assistants: conversations,
		watcher:    assistant.NewWatcher(conversations, jobs, nil, nil),
		bus:        eventbus.New(),
		restorer: restore.New(filepath.Join(t.TempDir(), "terminals.json"),
			func() bool { return false }, nil, shells, tmux.New(), nil, nil, nil),
	}
	return s, jobs, work, stateDir
}

// createCoderAs posts the create form the way a caller reaches it: local says
// the call came over the local API socket, and the header is what a turn's
// environment puts on the wire.
func createCoderAs(t *testing.T, s *Server, form url.Values, local bool, header string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/coders/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if header != "" {
		req.Header.Set(localapi.AssistantHeader, header)
	}
	if local {
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
	}
	c.Request = req
	s.handleCoderCreate(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var answer map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("create answer: %v: %s", err, rec.Body.String())
	}
	return answer
}

// A create that carries a criterion steers the coder it starts, and that job
// belongs to somebody: the assistant whose turn ran the command, or the one a
// browser named. The create path used to build the job without an owner, so
// the watcher refused it and `dev-cockpit assistant coder-new --done-when`
// left a running coder nobody watched, while the same criterion posted to the
// jobs route went through.
func TestCoderCreateGivesTheJobTheAssistantThatAskedForIt(t *testing.T) {
	s, jobs, work, _ := steerCreateServer(t)
	mine, err := s.assistants.Create("claude")
	if err != nil {
		t.Fatalf("create assistant: %v", err)
	}
	theirs, err := s.assistants.Create("claude")
	if err != nil {
		t.Fatalf("create assistant: %v", err)
	}

	// The assistant's own command: the caller names itself, so the job is its
	// own even with another assistant standing beside it.
	answer := createCoderAs(t, s, url.Values{
		"project":   {work},
		"prompt":    {"write the README"},
		"done_when": {"README.md exists"},
	}, true, mine.ID)
	if steerErr, ok := answer["steerError"]; ok {
		t.Fatalf("the create did not steer the coder it started: %v", steerErr)
	}
	job, ok := jobs.Find(answer["id"].(string))
	if !ok {
		t.Fatalf("the create started a coder without a job: %#v", answer)
	}
	if job.Owner != mine.ID {
		t.Fatalf("the job belongs to %q, want the calling assistant %q", job.Owner, mine.ID)
	}
	if job.DoneWhen != "README.md exists" {
		t.Fatalf("the criterion did not reach the job: %q", job.DoneWhen)
	}

	// A browser says which assistant the job reports to, the same field the
	// jobs route reads, so a form that grows the field steers the right one.
	answer = createCoderAs(t, s, url.Values{
		"project":   {work},
		"done_when": {"the tests pass"},
		"assistant": {theirs.ID},
	}, false, "")
	if steerErr, ok := answer["steerError"]; ok {
		t.Fatalf("the browser create did not steer the coder it started: %v", steerErr)
	}
	job, ok = jobs.Find(answer["id"].(string))
	if !ok {
		t.Fatalf("the browser create started a coder without a job: %#v", answer)
	}
	if job.Owner != theirs.ID {
		t.Fatalf("the job belongs to %q, want the named assistant %q", job.Owner, theirs.ID)
	}
}

// Nobody to charge the job to is not a silent outcome: the coder is running by
// the time this is known, so the answer says so and the caller can steer it
// itself. Ending the request instead would hide a session that already exists.
func TestCoderCreateSaysSoWhenTheJobHasNobodyToBelongTo(t *testing.T) {
	s, jobs, work, _ := steerCreateServer(t)
	if _, err := s.assistants.Create("claude"); err != nil {
		t.Fatalf("create assistant: %v", err)
	}
	if _, err := s.assistants.Create("claude"); err != nil {
		t.Fatalf("create assistant: %v", err)
	}

	// Two assistants and a browser that names none: the create cannot guess
	// whose job it would be.
	answer := createCoderAs(t, s, url.Values{
		"project":   {work},
		"done_when": {"the tests pass"},
	}, false, "")
	steerErr, ok := answer["steerError"].(string)
	if !ok || steerErr == "" {
		t.Fatalf("a job nobody owns was swallowed: %#v", answer)
	}
	if _, running := jobs.Find(answer["id"].(string)); running {
		t.Fatal("a job was stored although the answer said it was not")
	}
	if answer["id"] == nil {
		t.Fatal("the coder that is running has to be in the answer")
	}
}
