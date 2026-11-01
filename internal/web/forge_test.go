package web

import (
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
	"github.com/marein/dev-cockpit/internal/project"
)

// The repositories bind by their origins and their overrides, a worktree by
// its main project's, a directory without git to nothing.
func TestForgeByProjectBindsWhatResolves(t *testing.T) {
	projects := []project.Project{
		{Name: "app", GitRepo: true, GitOriginRaw: "git@github.com:marein/app.git"},
		{Name: "lab", GitRepo: true, GitOriginRaw: "git@gitlab.example.com:team/lab.git"},
		{Name: "plain", GitOriginRaw: "git@github.com:marein/plain.git"},
	}
	s := &Server{}
	if got := s.forgeByProject(projects); got != nil {
		t.Fatalf("a server without the forge service bound %v", got)
	}
	f := forge.New(t.TempDir(), []forge.Provider{github.Provider{}, gitlab.Provider{}}, github.Builtin())
	s.SetForge(f)
	got := s.forgeByProject(projects)
	if len(got) != 1 || got["app"].Path != "marein/app" || got["app"].Host.Kind != github.Kind {
		t.Fatalf("bound %+v, want app on github.com alone", got)
	}
	if _, err := f.Add(forge.Input{Kind: "gitlab", WebURL: "https://gitlab.example.com"}); err != nil {
		t.Fatal(err)
	}
	got = s.forgeByProject(projects)
	if len(got) != 2 || got["lab"].Path != "team/lab" || got["lab"].Host.Kind != gitlab.Kind {
		t.Fatalf("bound %+v, want lab on the added host too", got)
	}
	if err := f.SetOverride("app", forge.Override{How: forge.ByHand, Host: "github.com", Path: "marein/fork"}); err != nil {
		t.Fatal(err)
	}
	if err := f.SetOverride("lab", forge.Override{How: forge.Off}); err != nil {
		t.Fatal(err)
	}
	projects = append(projects,
		project.Project{Name: "app-wt", GitRepo: true, GitWorktree: true, GitWorktreeOf: "app", GitOriginRaw: "git@github.com:marein/app.git"},
		project.Project{Name: "stray-wt", GitRepo: true, GitWorktree: true, GitOriginRaw: "git@github.com:marein/stray.git"})
	got = s.forgeByProject(projects)
	if len(got) != 3 || got["app"].Path != "marein/fork" || got["app-wt"].Path != "marein/fork" || got["stray-wt"].Path != "marein/stray" {
		t.Fatalf("bound %+v, want app and its worktree by hand, lab off and the stray worktree by its origin", got)
	}
}
