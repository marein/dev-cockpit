package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/project"
)

// The command posts the create form's own fields, so a worktree made by an
// assistant is the one the page makes, name, branch and post script alike.
func TestProjectWorktreeNewPostsTheCreateForm(t *testing.T) {
	var forms []url.Values
	answer := map[string]any{"name": "app-feature", "path": "/p/app-feature", "worktree_of": "app", "branch": "feature"}
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		forms = append(forms, form)
		if r.URL.Path != "/projects" {
			t.Errorf("posted to %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer)
	})

	var out strings.Builder
	if err := runProjectWorktreeNew(&out, inspectOptions{stateDir: dir}, "app", "origin/feature", "", "", true); err != nil {
		t.Fatalf("existing branch: %v", err)
	}
	got := forms[0]
	if got.Get("create") != "worktree:app" || got.Get("branch_mode") != "existing" || got.Get("branch") != "origin/feature" || got.Get("fast_forward") != "on" || got.Get("project_name") != "" {
		t.Fatalf("unexpected form %v", got)
	}
	if !strings.Contains(out.String(), "project app-feature created at /p/app-feature, a worktree of app on branch feature\n") {
		t.Fatalf("unexpected output %q", out.String())
	}

	answer["post_script_output"] = "installing\nboom"
	answer["post_script_error"] = "exit status 2"
	out.Reset()
	err := runProjectWorktreeNew(&out, inspectOptions{stateDir: dir}, "app", "feature", "master", "own", false)
	if err == nil || !strings.Contains(err.Error(), "the worktree project stands") {
		t.Fatalf("a failed script must fail the command, got %v", err)
	}
	got = forms[1]
	if got.Get("branch_mode") != "new" || got.Get("new_branch") != "feature" || got.Get("start") != "master" || got.Get("project_name") != "own" || got.Has("fast_forward") {
		t.Fatalf("unexpected form %v", got)
	}
	for _, want := range []string{"post script failed: exit status 2\n", "--- output\ninstalling\nboom\n"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output misses %q:\n%s", want, out.String())
		}
	}

	answer["post_script_error"] = ""
	out.Reset()
	if err := runProjectWorktreeNew(&out, inspectOptions{stateDir: dir}, "app", "feature", "", "", false); err != nil {
		t.Fatalf("a script that ran must not fail: %v", err)
	}
	if !strings.Contains(out.String(), "post script ran\n") {
		t.Fatalf("unexpected output %q", out.String())
	}
}

func TestWorktreeListNamesProjectsBranchesAndStrays(t *testing.T) {
	projects := []project.Project{
		{Name: "app", GitRepo: true, GitBranch: "master", GitWorktrees: []project.WorktreeRef{
			{Path: "/p/app-feature", Project: "app-feature"},
			{Path: "/elsewhere/wt"},
		}},
		{Name: "app-feature", GitRepo: true, GitBranch: "feature", GitWorktree: true, GitWorktreeOf: "app"},
		{Name: "lone", GitRepo: true, GitBranch: "main"},
		{Name: "notes"},
	}
	out := worktreeList(projects, "app")
	for _, want := range []string{"Worktrees of app (2)\n", "  app-feature (feature) /p/app-feature\n", "  /elsewhere/wt (no project)\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if out := worktreeList(projects, "app-feature"); !strings.HasPrefix(out, "app-feature is a worktree of app\nWorktrees of app (2)\n") {
		t.Fatalf("a worktree must answer with its main's list:\n%s", out)
	}
	for name, want := range map[string]string{"lone": "lone has no worktrees\n", "notes": "notes is no git repository\n", "gone": "there is no project gone\n"} {
		if got := worktreeList(projects, name); got != want {
			t.Fatalf("%s answered %q", name, got)
		}
	}
}

// Setting the post script posts the content once and says when it waits for
// the user, or what was saved when nobody is asked.
func TestSettingThePostScriptWaitsOrSaves(t *testing.T) {
	for _, tc := range []struct{ answer, want string }{
		{`{"pending":true,"what":"Set the worktree post script"}`, "Set the worktree post script waits for the user's approval. A note lands in your thread once they decide; do not run it again.\n"},
		{`{"ok":true,"saved":"The worktree post script is saved."}`, "The worktree post script is saved.\n"},
	} {
		var posted string
		dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
			posted = r.FormValue("post_script")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tc.answer))
		})
		var out strings.Builder
		if err := runProjectWorktreeScriptSet(&out, inspectOptions{stateDir: dir}, "#!/bin/sh\n"); err != nil {
			t.Fatal(err)
		}
		if posted != "#!/bin/sh\n" || out.String() != tc.want {
			t.Fatalf("posted %q, printed %q", posted, out.String())
		}
	}
}
