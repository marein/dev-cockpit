package cli

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/notify"
)

// A pipeline end reaches the assistants in the words of its forge, under the
// kind its final status is, with the jobs that failed it and none allowed
// to; a status that is no end reaches nobody.
func TestAPipelineEndIsReportedInTheForgesWords(t *testing.T) {
	kind := forge.Kind{ID: "gitlab", Label: "GitLab", ChangeNoun: "merge request", ChangeMark: "!"}
	end := forge.PipelineEnd{
		Watch: forge.Watch{Project: "shop", Binding: forge.Binding{Host: forge.BuiltIn(kind, "https://gitlab.example.com"), Path: "team/shop"}, Branch: "feature", Remote: "feature", Head: "a1"},
		Status: forge.BranchStatus{
			Change: &forge.Change{Number: 4, Title: "Fix the login", Draft: true, State: forge.Open, URL: "https://gitlab.example.com/team/shop/-/merge_requests/4", Target: "main"},
			Pipeline: &forge.Pipeline{ID: "12", Status: forge.Failed, URL: "https://gitlab.example.com/team/shop/-/pipelines/12", Jobs: []forge.Job{
				{Name: "unit", Status: forge.Failed}, {Name: "flaky", Status: forge.Failed, AllowFailure: true}, {Name: "build", Status: forge.Success},
			}},
		},
	}
	got, ok := pipelineReport(end)
	want := assistant.PipelineReport{
		Kind: assistant.PipelineKindFailed, Project: "shop", Branch: "feature", Head: "a1", Forge: "GitLab", Address: "gitlab.example.com/team/shop",
		ID: "12", URL: "https://gitlab.example.com/team/shop/-/pipelines/12",
		Change:  &assistant.ChangeRequest{Noun: "merge request", Ref: "!4", Title: "Fix the login", State: "open", Draft: true, Target: "main", URL: "https://gitlab.example.com/team/shop/-/merge_requests/4"},
		Failing: []string{"unit"},
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("reported %+v %v, want %+v", got, ok, want)
	}
	for status, kind := range map[forge.Status]string{forge.Success: assistant.PipelineKindSucceeded, forge.Canceled: assistant.PipelineKindCanceled, forge.Skipped: assistant.PipelineKindSkipped} {
		end.Status.Pipeline.Status = status
		if got, ok := pipelineReport(end); !ok || got.Kind != kind {
			t.Fatalf("%s reported %q %v", status, got.Kind, ok)
		}
	}
	end.Status.Pipeline.Status = forge.Manual
	if _, ok := pipelineReport(end); ok {
		t.Fatal("a pipeline waiting for a person was reported")
	}
}

// A new note reaches the assistants in the words of its forge, with where
// it stands, a reply with whose thread it answers, and one on the diff with
// the id of its thread.
func TestANewNoteIsReportedInTheForgesWords(t *testing.T) {
	kind := forge.Kind{ID: "gitlab", Label: "GitLab", ChangeNoun: "merge request", ChangeMark: "!"}
	thread := forge.Thread{ID: "d1", Path: "src/login.go", Line: 4, Side: forge.NewSide, Outdated: true, Resolvable: true, Resolved: true, Notes: []forge.Note{
		{ID: "29", Author: "root", Body: "New name?"},
		{ID: "33", Author: "alice", Body: "Yes, bb.", URL: "https://gitlab.example.com/team/shop/-/merge_requests/4#note_33"},
	}}
	n := forge.ReviewNote{
		Watch:  forge.Watch{Project: "shop", Binding: forge.Binding{Host: forge.BuiltIn(kind, "https://gitlab.example.com"), Path: "team/shop"}, Branch: "feature", Remote: "feature", Head: "a1"},
		Change: forge.Change{Number: 4, Title: "Fix the login", Draft: true, State: forge.Open, URL: "https://gitlab.example.com/team/shop/-/merge_requests/4", Target: "main"},
		Thread: thread,
		Note:   thread.Notes[1],
	}
	want := assistant.ReviewReport{
		Project: "shop", Branch: "feature", Forge: "GitLab", Address: "gitlab.example.com/team/shop",
		Change: assistant.ChangeRequest{Noun: "merge request", Ref: "!4", Title: "Fix the login", State: "open", Draft: true, Target: "main", URL: "https://gitlab.example.com/team/shop/-/merge_requests/4"},
		Author: "alice", Body: "Yes, bb.", URL: "https://gitlab.example.com/team/shop/-/merge_requests/4#note_33",
		Path: "src/login.go", Line: 4, Side: "new", Outdated: true, Reply: true, Opener: "root", Resolved: true, Thread: "d1",
	}
	if got := reviewReport(n); !reflect.DeepEqual(got, want) {
		t.Fatalf("reported %+v, want %+v", got, want)
	}
	n.Note = thread.Notes[0]
	if got := reviewReport(n); got.Reply || got.Opener != "" || got.Author != "root" {
		t.Fatalf("the thread's first note reported %+v", got)
	}
	n.Thread.Path = ""
	if got := reviewReport(n); got.Thread != "" {
		t.Fatalf("a general thread named the id %q a write on a thread of the diff takes", got.Thread)
	}
}

// Every end of a pipeline reaches the assistants, and a failed one alone is
// the project's news, leading to the editor opened on the pipeline.
func TestAFailedPipelineAloneIsNews(t *testing.T) {
	kind := forge.Kind{ID: "gitlab", Label: "GitLab", ChangeNoun: "merge request", ChangeMark: "!"}
	end := forge.PipelineEnd{
		Watch: forge.Watch{Project: "my shop", Binding: forge.Binding{Host: forge.BuiltIn(kind, "https://gitlab.example.com"), Path: "team/shop"}, Branch: "feature", Remote: "feature", Head: "a1"},
		Status: forge.BranchStatus{
			Change:   &forge.Change{Number: 4, Title: "Fix the login", State: forge.Open},
			Pipeline: &forge.Pipeline{ID: "12", Status: forge.Success, Jobs: []forge.Job{{Name: "unit", Status: forge.Success}}},
		},
	}
	notifier := notify.NewService(filepath.Join(t.TempDir(), "notifications.json"), nil)
	var published []string
	ended := pipelineEnded(func(r assistant.PipelineReport) { published = append(published, r.Kind) }, notifier)
	for _, status := range []forge.Status{forge.Success, forge.Canceled, forge.Skipped, forge.Manual} {
		end.Status.Pipeline.Status = status
		ended(end)
	}
	if list := notifier.List(0); len(list) != 0 {
		t.Fatalf("an end that did not fail is news: %+v", list)
	}
	end.Status.Pipeline.Status = forge.Failed
	end.Status.Pipeline.Jobs = []forge.Job{{Name: "unit", Status: forge.Failed}, {Name: "flaky", Status: forge.Failed, AllowFailure: true}}
	ended(end)
	want := []string{assistant.PipelineKindSucceeded, assistant.PipelineKindCanceled, assistant.PipelineKindSkipped, assistant.PipelineKindFailed}
	if !reflect.DeepEqual(published, want) {
		t.Fatalf("the assistants heard %v, want %v", published, want)
	}
	list := notifier.List(0)
	if len(list) != 1 {
		t.Fatalf("the failure made %d entries: %+v", len(list), list)
	}
	n := list[0]
	if n.TargetID != notify.PipelineTarget("my shop") || n.Read || n.Project != "my shop" || n.URL != "/projects/my%20shop/editor?view=pipeline" {
		t.Fatalf("the news is %+v", n)
	}
	if n.Title != "Pipeline failed." || n.Detail != "feature in my shop, merge request !4: unit" {
		t.Fatalf("the news reads %q and %q", n.Title, n.Detail)
	}
}
