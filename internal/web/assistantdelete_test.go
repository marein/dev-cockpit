package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/settings"
)

// sessionTurnCoder runs a turn the way a coder runs a command: in a session of
// its own, out of reach of the turn's kill, holding the inherited run lock
// until the gate file appears.
type sessionTurnCoder struct{ dir string }

func (c sessionTurnCoder) Available() []assistant.CoderInfo {
	return []assistant.CoderInfo{{ID: "claude", Label: "Claude", Runner: sessionTurnRunner(c)}}
}

type sessionTurnRunner struct{ dir string }

func (r sessionTurnRunner) Command(assistant.TurnRequest) (assistant.Command, error) {
	started, gate := filepath.Join(r.dir, "started"), filepath.Join(r.dir, "gate")
	script := "setsid sh -c 'touch " + started + "; until [ -e " + gate + " ]; do sleep 0.02; done' & wait"
	return assistant.Command{Name: "sh", Args: []string{"-c", script}}, nil
}

func (sessionTurnRunner) Parse(string, chan<- assistant.Event) assistant.Parser {
	return quietParser{}
}
func (sessionTurnRunner) SessionExists(string) bool  { return false }
func (sessionTurnRunner) DeleteSession(string) error { return nil }

type quietParser struct{}

func (quietParser) Line([]byte) error                  { return nil }
func (quietParser) Finish() error                      { return nil }
func (quietParser) Diagnose(err error, _ string) error { return err }

// An assistant deleting itself asks from a command of its own turn, and the
// delete stops that turn and waits for it. The coder runs the command in a
// session of its own, out of reach of the kill and holding the run's lock,
// waiting for this very answer. With the Assistant delete approval off the
// delete must still go through at once: a run this server started ends with
// its hold process, not with its lock.
func TestASelfDeleteDoesNotWaitForTheCommandThatAsked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir, stateDir := t.TempDir(), t.TempDir()
	gate := filepath.Join(dir, "gate")
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0o644) })
	assistants, _, err := assistant.New(stateDir, sessionTurnCoder{dir: dir}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	own, err := assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		projects:   project.NewRepository(t.TempDir(), nil),
		assistants: assistants,
		docker:     docker.NewService(stateDir, func() string { return "" }),
		watcher:    assistant.NewWatcher(assistants, assistant.NewJobs(assistant.NewStore(stateDir)), nil, nil),
		notifier:   notify.NewService(filepath.Join(stateDir, "notifications.json"), nil),
		bus:        eventbus.New(),
	}
	s.settings = settings.New(filepath.Join(stateDir, "settings.json"))
	setApprovalAsks(s.settings, approvalAssistantDelete, false)
	s.approvals = s.newApprovals(stateDir)
	if _, err := assistants.Send(own.ID, "delete yourself", nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for _, err := os.Stat(filepath.Join(dir, "started")); err != nil; _, err = os.Stat(filepath.Join(dir, "started")) {
		if time.Now().After(deadline) {
			t.Fatal("the turn never started its command")
		}
		time.Sleep(20 * time.Millisecond)
	}

	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/assistants/"+own.ID, strings.NewReader("form=delete"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.Header.Set(localapi.AssistantHeader, own.ID)
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = req
		s.assistantDelete(c, own.ID)
		answered <- rec
	}()
	select {
	case rec := <-answered:
		if rec.Code != http.StatusOK {
			t.Fatalf("the self delete answered %d: %s", rec.Code, rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the self delete waited for the command that is waiting for its answer")
	}
	if _, err := assistants.Get(own.ID); err == nil {
		t.Fatal("the assistant is still there after it deleted itself")
	}
}
