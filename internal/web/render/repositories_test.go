package render

import (
	"errors"
	"slices"
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

// A built in host is offered by its forge's name, an added one by its
// address without the scheme, each under its own choice.
func TestTheForgesAreOfferedByTheirNames(t *testing.T) {
	got := NewRepositoryForges([]forge.Host{github.Builtin(), {ID: "x1", Kind: gitlab.Kind, WebURL: "https://git.example.com/gitlab"}, {ID: "x2", Kind: gitlab.Kind, WebURL: "http://127.0.0.1:3180"}})
	want := []RepositoryForge{
		{Choice: "forge:github.com", ID: "github.com", Label: "GitHub"},
		{Choice: "forge:x1", ID: "x1", Label: "git.example.com/gitlab"},
		{Choice: "forge:x2", ID: "x2", Label: "127.0.0.1:3180"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v", got)
	}
}

// A row wears its forge's mark and what the forge says of it, a failed
// search its sentence, and no answer holds null.
func TestTheRepositorySearchIsWordedPerRow(t *testing.T) {
	self := forge.Host{ID: "x", Kind: gitlab.Kind, WebURL: "https://git.example.com/gitlab"}
	got := NewRepositoryChoices([]forge.Found{
		{Host: self, Path: "team/site", CloneURL: "git@git.example.com:team/site.git"},
		{Host: self, Path: "team/app", CloneURL: "git@git.example.com:team/app.git", Visibility: forge.VisibilityPrivate, Fork: true, Archived: true},
	})
	want := []RepositoryChoice{
		{Icon: "ti-brand-gitlab", Path: "team/site", URL: "git@git.example.com:team/site.git"},
		{Icon: "ti-brand-gitlab", Path: "team/app", URL: "git@git.example.com:team/app.git", Hint: "private · fork · archived"},
	}
	if !slices.Equal(got.Repositories, want) || got.Failure != "" {
		t.Fatalf("got %+v", got)
	}
	failed := RepositorySearchFailed(errors.New("The server did not answer in time."))
	if failed.Repositories == nil || len(failed.Repositories) != 0 || failed.Failure != "The server did not answer in time." {
		t.Fatalf("failed %+v", failed)
	}
	if empty := NewRepositoryChoices(nil); empty.Repositories == nil {
		t.Fatalf("an empty answer holds null: %+v", empty)
	}
}
