package assistant

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/statefile"
)

// fakeForge is a forge the way the poller reads it, and it lives outside
// every process: the working copy of each project with the branch it stands
// on, the pipeline of every branch and the notes on its change request. It is
// the provider of the forge service a test builds over a state directory, so
// what a test sees handed on came through that service, its poller and the
// records it keeps of what it handed on.
type fakeForge struct {
	mu        sync.Mutex
	projects  []string
	checkouts map[string]string
	pipelines map[string]forge.Pipeline
	notes     map[string][]forge.Note
	// pushes counts the rounds asked so far: every one of them finds a new
	// commit pushed, so it reads every branch, whatever pace the poller keeps.
	pushes int
	// fail is what every read of a branch answers while the forge is down or
	// limits its requests, nil while it answers.
	fail error
}

// fakeForgeAccount is who the token of the fake forge belongs to, whose own
// notes are nobody's news.
const fakeForgeAccount = "me"

// fakeForgeChanges are the change requests of the branches a project has.
var fakeForgeChanges = map[string]int{"main": 3, "feature": 4}

var fakeForgeKinds = map[forge.Status]string{
	forge.Success:  PipelineKindSucceeded,
	forge.Failed:   PipelineKindFailed,
	forge.Canceled: PipelineKindCanceled,
	forge.Skipped:  PipelineKindSkipped,
}

func newFakeForge() *fakeForge {
	return &fakeForge{checkouts: map[string]string{}, pipelines: map[string]forge.Pipeline{}, notes: map[string][]forge.Note{}}
}

func (f *fakeForge) Kind() forge.Kind {
	return forge.Kind{ID: "fake", Label: "GitLab", ChangeNoun: "merge request", ChangeMark: "!"}
}

func (f *fakeForge) host() forge.Host { return forge.BuiltIn(f.Kind(), "https://forge.example.com") }

func (f *fakeForge) Test(context.Context, *forge.Client, forge.Host, string) (forge.Connection, error) {
	return forge.Connection{Account: fakeForgeAccount}, nil
}

func (f *fakeForge) BranchStatus(_ context.Context, _ *forge.Client, b forge.Binding, _, branch, _ string) (forge.BranchStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return forge.BranchStatus{}, f.fail
	}
	status := forge.BranchStatus{Change: &forge.Change{Number: fakeForgeChanges[branch], State: forge.Open}}
	if pl, ok := f.pipelines[path.Base(b.Path)+"/"+branch]; ok {
		status.Pipeline = &pl
	}
	return status, nil
}

func (f *fakeForge) Threads(_ context.Context, _ *forge.Client, b forge.Binding, _ string, number int) (forge.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var review forge.Review
	for branch, change := range fakeForgeChanges {
		if change != number {
			continue
		}
		for _, n := range f.notes[path.Base(b.Path)+"/"+branch] {
			review.General = append(review.General, forge.Thread{ID: "thread-" + n.ID, Notes: []forge.Note{n}})
		}
	}
	return review, nil
}

// checkout has the working copy of a project stand on a branch.
func (f *fakeForge) checkout(project, branch string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Contains(f.projects, project) {
		f.projects = append(f.projects, project)
		slices.Sort(f.projects)
	}
	f.checkouts[project] = branch
}

// branch is the branch the working copy of a project stands on.
func (f *fakeForge) branch(project string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkouts[project]
}

// failing has every read of a branch fail with err, nil for none.
func (f *fakeForge) failing(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = err
}

// pipeline has a branch stand at the pipeline with that id in that status.
func (f *fakeForge) pipeline(project, branch, id string, status forge.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pipelines[project+"/"+branch] = forge.Pipeline{ID: id, Status: status}
}

// note writes a note on the change request of a branch.
func (f *fakeForge) note(project, branch, id, author string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	at := project + "/" + branch
	f.notes[at] = append(f.notes[at], forge.Note{ID: id, Author: author, Body: "a note"})
}

// watches is what the poller is told to keep: the branch every working copy
// stands on.
func (f *fakeForge) watches(context.Context, func(forge.Binding) bool) ([]forge.Watch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes++
	var list []forge.Watch
	for _, project := range f.projects {
		branch := f.checkouts[project]
		list = append(list, forge.Watch{Project: project, Binding: forge.Binding{Host: f.host(), Path: "team/" + project}, Branch: branch, Remote: branch, Head: "commit-" + strconv.Itoa(f.pushes)})
	}
	return list, nil
}

// address is where a project lives on the fake forge, the way an event names
// it.
func (f *fakeForge) address(project string) string { return "forge.example.com/team/" + project }

// pipelineID is the name the end of a pipeline is handed under.
func (f *fakeForge) pipelineID(project, id string, status forge.Status) string {
	return "pipeline " + fakeForgeKinds[status] + " #" + id + " " + f.address(project) + " " + project
}

// noteID is the name a note on the change request of a branch is handed
// under.
func (f *fakeForge) noteID(project, branch, id string) string {
	return fmt.Sprintf("review %s !%d %s %s", id, fakeForgeChanges[branch], f.address(project), project)
}

// service is the forge service of one process over a state directory, with
// the token its host is read with, wired the way runServe wires it, which
// words an event with more than its name needs. heard is handed a pipeline's
// end and a review note before the service's record of it moves, with the
// name the reactor takes it under and the call that hands it to the reactor;
// told hears of a pipeline's end after, where the notification of a failed
// one comes from.
func (f *fakeForge) service(t testing.TB, dir string, reactor *Reactor, heard func(id string, hand func()), told func(id, kind string)) *forge.Service {
	t.Helper()
	svc := forge.New(dir, []forge.Provider{f}, f.host())
	if err := svc.ChangeToken(f.host().ID, "glpat-fake-0123456789abcdef", false); err != nil {
		t.Fatalf("the token of the fake forge: %v", err)
	}
	kind := f.Kind()
	svc.OnPipelineEnd(func(end forge.PipelineEnd) {
		w, pl := end.Watch, end.Status.Pipeline
		heard(f.pipelineID(w.Project, pl.ID, pl.Status), func() {
			reactor.Pipeline(PipelineReport{
				Kind: fakeForgeKinds[pl.Status], Project: w.Project, Branch: w.Branch, Head: w.Head,
				Forge: kind.Label, Address: w.Binding.Address(), ID: pl.ID,
			})
		})
	}, func(end forge.PipelineEnd) {
		pl := end.Status.Pipeline
		told(f.pipelineID(end.Watch.Project, pl.ID, pl.Status), fakeForgeKinds[pl.Status])
	})
	svc.OnReviewNote(func(n forge.ReviewNote) {
		w := n.Watch
		heard(f.noteID(w.Project, w.Branch, n.Note.ID), func() {
			reactor.ReviewComment(ReviewReport{
				Note: n.Note.ID, Project: w.Project, Branch: w.Branch, Forge: kind.Label, Address: w.Binding.Address(),
				Change: ChangeRequest{Noun: kind.ChangeNoun, Ref: kind.ChangeRef(n.Change.Number)}, Author: n.Note.Author, Body: n.Note.Body,
			})
		})
	})
	return svc
}

// fakeForgeEnds and fakeForgeNotes are the records the forge service keeps of
// what it handed on, as far as a test reads them.
type fakeForgeEnds struct {
	Projects map[string]struct {
		Branch   string         `json:"branch"`
		Pipeline string         `json:"pipeline"`
		Ended    []forge.Status `json:"ended"`
	} `json:"projects"`
}

type fakeForgeNotes struct {
	Projects map[string][]struct {
		Change int      `json:"change"`
		Notes  []string `json:"notes"`
	} `json:"projects"`
}

// handed reports what the records under a state directory hold as handed on:
// the end of a project's pipeline with that id in that status, and a note on
// the change request of a branch.
func (f *fakeForge) handed(dir string) (end func(project, id string, status forge.Status) bool, note func(project, branch, id string) bool) {
	var ends fakeForgeEnds
	var notes fakeForgeNotes
	statefile.Read(filepath.Join(dir, "forge", "pipelines.json"), &ends)
	statefile.Read(filepath.Join(dir, "forge", "notes.json"), &notes)
	end = func(project, id string, status forge.Status) bool {
		r := ends.Projects[project]
		return r.Pipeline == "#"+id && slices.Contains(r.Ended, status)
	}
	note = func(project, branch, id string) bool {
		for _, r := range notes.Projects[project] {
			if r.Change == fakeForgeChanges[branch] && slices.Contains(r.Notes, id) {
				return true
			}
		}
		return false
	}
	return end, note
}

// owed names what the forge shows of the branches the working copies stand
// on that the records under a state directory do not hold, "" when nothing
// is left to hand on.
func (f *fakeForge) owed(dir string) string {
	end, note := f.handed(dir)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, project := range f.projects {
		branch := f.checkouts[project]
		if pl, ok := f.pipelines[project+"/"+branch]; ok && pl.Status.Final() && !end(project, pl.ID, pl.Status) {
			return fmt.Sprintf("the end of pipeline %s of %s, %s", pl.ID, project, pl.Status)
		}
		for _, n := range f.notes[project+"/"+branch] {
			if !note(project, branch, n.ID) {
				return fmt.Sprintf("the note %s on %s of %s", n.ID, branch, project)
			}
		}
	}
	return ""
}

// pipelineLeft is a pipeline the last process read running that ended while
// no process ran.
func pipelineLeft(status forge.Status) func(t *testing.T, dir, owner string, world *fakeForge) string {
	return func(t *testing.T, dir, owner string, world *fakeForge) string {
		world.checkout("shop", "feature")
		world.pipeline("shop", "feature", "12", forge.Running)
		gone := newHandoverProcess(t, dir, owner, "", "", new(int), world)
		gone.forge.PollOnce(context.Background(), world.watches)
		world.pipeline("shop", "feature", "12", status)
		return world.pipelineID("shop", "12", status)
	}
}

// noteLeft is a note somebody else wrote on a change request the last process
// had read, while no process ran, between two of the token's own account.
func noteLeft(t *testing.T, dir, owner string, world *fakeForge) string {
	world.checkout("shop", "feature")
	world.note("shop", "feature", "31", "alice")
	gone := newHandoverProcess(t, dir, owner, "", "", new(int), world)
	gone.forge.PollOnce(context.Background(), world.watches)
	world.note("shop", "feature", "32", fakeForgeAccount)
	world.note("shop", "feature", "33", "bob")
	world.note("shop", "feature", "34", "Me")
	return world.noteID("shop", "feature", "33")
}
