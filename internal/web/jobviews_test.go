package web

import (
	"fmt"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
)

// noCoders is an assistant with nothing installed: these tests never run a
// turn, they only look at the job views.
type noCoders struct{}

func (noCoders) Available() []assistant.CoderInfo { return nil }

// The jobs of one assistant, and a host collects them for weeks, so the list
// caps its closed tail the way the `job-list` command does: every open job renders,
// the newest closed ones follow, and what is held back is a count, never
// silence.
func TestJobViewsCapTheClosedTail(t *testing.T) {
	stateDir := t.TempDir()
	conversations, _, err := assistant.New(stateDir, noCoders{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	owner := "11111111-1111-4111-8111-111111111111"
	assistant.NewStore(stateDir).Save(assistant.Instance{Summary: assistant.Summary{ID: owner, Title: "One", CoderID: "claude", Status: assistant.StatusActive}})
	jobs := assistant.NewJobs(assistant.NewStore(stateDir))
	store := jobs.Of(owner)
	now := time.Now().UTC()
	for i := 0; i < closedJobsShown+3; i++ {
		store.Save(assistant.Job{
			Terminal: fmt.Sprintf("closed-%d", i), CoderID: "claude", DoneWhen: "x",
			State: assistant.JobDone, CreatedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	store.Save(assistant.Job{
		Terminal: "open-1", CoderID: "claude", DoneWhen: "x",
		State: assistant.JobSteering, CreatedAt: now.Add(-time.Hour),
	})
	s := &Server{assistants: conversations, watcher: assistant.NewWatcher(conversations, jobs, nil, nil)}

	views, older := s.assistantJobViews(owner)
	if len(views) != 1+closedJobsShown {
		t.Fatalf("want the open job plus %d closed, got %d", closedJobsShown, len(views))
	}
	if views[0].Terminal != "open-1" {
		t.Fatalf("the open job has to render first, got %q", views[0].Terminal)
	}
	if views[1].Terminal != "closed-0" {
		t.Fatalf("the newest closed job has to survive the cap, got %q", views[1].Terminal)
	}
	if older != 3 {
		t.Fatalf("want the dropped tail counted, got %d", older)
	}
}

// Stopping a coder and deleting one are different endings for its job. A
// stopped session can be resumed, so its job stays readable next to it; a
// deleted one leaves nothing to read it next to, and the entry would be parsed
// on every look at the store from then on.
func TestDeletingACoderRemovesItsJobAndStoppingKeepsIt(t *testing.T) {
	stateDir := t.TempDir()
	conversations, _, err := assistant.New(stateDir, noCoders{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	owner := "11111111-1111-4111-8111-111111111111"
	assistant.NewStore(stateDir).Save(assistant.Instance{Summary: assistant.Summary{ID: owner, Title: "One", CoderID: "claude", Status: assistant.StatusActive}})
	jobs := assistant.NewJobs(assistant.NewStore(stateDir))
	store := jobs.Of(owner)
	s := &Server{assistants: conversations, watcher: assistant.NewWatcher(conversations, jobs, nil, nil)}
	for _, terminal := range []string{"stopped-1", "deleted-1"} {
		if _, err := s.watcher.Steer(assistant.Job{Owner: owner, Terminal: terminal, CoderID: "claude", DoneWhen: "x"}); err != nil {
			t.Fatalf("steer: %v", err)
		}
	}

	s.jobCalledOff("stopped-1")
	s.jobDeleted("deleted-1")

	job, ok := store.Get("stopped-1")
	if !ok {
		t.Fatal("the job of a stopped coder has to stay visible")
	}
	if job.State.Open() {
		t.Fatalf("the job of a stopped coder has to be closed, got %q", job.State)
	}
	if _, ok := store.Get("deleted-1"); ok {
		t.Fatal("the job of a deleted coder is still in the store")
	}
}
