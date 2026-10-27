package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/tmux"
	"github.com/marein/dev-cockpit/internal/web/render"
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
	assistant.NewStore(stateDir).Save(assistant.Instance{Summary: assistant.Summary{ID: owner, Title: "One", CoderID: "claude"}})
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

	views, older := s.assistantJobViews(owner, false)
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
	views, older = s.assistantJobViews(owner, true)
	if len(views) != 1+closedJobsShown+3 || older != 0 {
		t.Fatalf("all has to list every job and hold none back, got %d and %d older", len(views), older)
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
	assistant.NewStore(stateDir).Save(assistant.Instance{Summary: assistant.Summary{ID: owner, Title: "One", CoderID: "claude"}})
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

// A standing trigger stops every coder whose job is done, and stopping releases
// the job. A job that is over keeps the state it ended in, and a release asked
// for it is refused with that state instead of rewriting it.
func TestStoppingACoderKeepsAClosedJobsState(t *testing.T) {
	stateDir := t.TempDir()
	conversations, _, err := assistant.New(stateDir, noCoders{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	owner := "11111111-1111-4111-8111-111111111111"
	assistant.NewStore(stateDir).Save(assistant.Instance{Summary: assistant.Summary{ID: owner, Title: "One", CoderID: "claude"}})
	jobs := assistant.NewJobs(assistant.NewStore(stateDir))
	store := jobs.Of(owner)
	s := &Server{assistants: conversations, watcher: assistant.NewWatcher(conversations, jobs, nil, nil)}
	for _, state := range []assistant.JobState{assistant.JobDone, assistant.JobBlocked, assistant.JobExpired} {
		terminal := "closed-" + string(state)
		store.Save(assistant.Job{Terminal: terminal, CoderID: "claude", DoneWhen: "x", State: state, CreatedAt: time.Now().UTC()})

		s.jobCalledOff(terminal)

		if job, _ := store.Get(terminal); job.State != state {
			t.Fatalf("a %s job reads %q after its coder stopped", state, job.State)
		}
		err := s.watcher.Release(terminal, "")
		if err == nil || !strings.Contains(err.Error(), string(state)) {
			t.Fatalf("a release of a %s job has to be refused with its state, got %v", state, err)
		}
		if job, _ := store.Get(terminal); job.State != state {
			t.Fatalf("a %s job reads %q after a release", state, job.State)
		}
	}
}

// A steered coder's row leads to it the way that works for it: a running one
// opens its page, a stopped one resumes it, which lands on the running coder.
// Both jobs are over, so the button follows the coder, not the job. A guest
// only looks, its row keeps the link either way.
func TestAStoppedCodersJobRowResumesIt(t *testing.T) {
	const running, stopped = "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"
	dir := t.TempDir()
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"[ \"$1\" = list-panes ] && printf '" + running + "\\t123\\t1700000000\\t0\\t0\\t\\t\\tclaude\\trunner\\t" + dir + "\\t\\t\\t\\t\\t\\n'\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	sessions := &storedSessions{list: []coder.Session{
		{SessionID: running, Name: "runner", CWD: dir},
		{SessionID: stopped, Name: "stopper", CWD: dir},
	}}

	stateDir := t.TempDir()
	conversations, _, err := assistant.New(stateDir, noCoders{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	owner := "11111111-1111-4111-8111-111111111111"
	assistant.NewStore(stateDir).Save(assistant.Instance{Summary: assistant.Summary{ID: owner, Title: "One", CoderID: "claude"}})
	jobs := assistant.NewJobs(assistant.NewStore(stateDir))
	for _, id := range []string{running, stopped} {
		jobs.Of(owner).Save(assistant.Job{Terminal: id, CoderID: "claude", DoneWhen: "x", State: assistant.JobDone, CreatedAt: time.Now().UTC()})
	}
	s := &Server{
		coders:     []*coder.Manager{coder.NewManager(config.Config{}, tmux.New(), storedCoder{sessions: sessions}, nil)},
		assistants: conversations,
		watcher:    assistant.NewWatcher(conversations, jobs, nil, nil),
	}

	views, _ := s.assistantJobViews(owner, false)
	tmpl := render.HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	row := func(access render.Access, id string) string {
		t.Helper()
		var out strings.Builder
		if err := tmpl.ExecuteTemplate(&out, "assistant_jobs_list.gohtml", render.AssistantData{Jobs: views, Page: render.Page{Access: access}}); err != nil {
			t.Fatalf("render: %v", err)
		}
		html := out.String()
		at := strings.Index(html, `data-assistant-job="`+id+`"`)
		if at < 0 {
			t.Fatalf("no row for %s", id)
		}
		rest := html[at+1:]
		if next := strings.Index(rest, `data-assistant-job="`); next >= 0 {
			rest = rest[:next]
		}
		return rest
	}

	if got := row(render.Access{}, stopped); !strings.Contains(got, `action="/coders/`+stopped+`/resume"`) ||
		!strings.Contains(got, "Resume coder") || strings.Contains(got, "data-assistant-job-open") {
		t.Fatalf("a stopped coder's row does not resume it: %s", got)
	}
	if got := row(render.Access{}, running); !strings.Contains(got, `href="/coders/`+running+`" class="btn btn-sm flex-fill" data-assistant-job-open`) ||
		strings.Contains(got, "/resume") {
		t.Fatalf("a running coder's row does not open it: %s", got)
	}
	if got := row(render.Access{Guest: true}, stopped); !strings.Contains(got, "data-assistant-job-open") || strings.Contains(got, "/resume") {
		t.Fatalf("a guest's row of a stopped coder changed: %s", got)
	}
}
