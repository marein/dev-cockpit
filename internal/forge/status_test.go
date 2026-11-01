package forge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

type statusStub struct {
	gitlab.Provider
	tok, branch, head string
}

func (p *statusStub) BranchStatus(_ context.Context, _ *forge.Client, _ forge.Binding, token, branch, head string) (forge.BranchStatus, error) {
	p.tok, p.branch, p.head = token, branch, head
	return forge.BranchStatus{Change: &forge.Change{Number: 4}}, nil
}

// plainProvider can test a token and reads nothing else.
type plainProvider struct{}

func (plainProvider) Kind() forge.Kind {
	return forge.Kind{ID: "plain", Label: "Plain"}
}

func (plainProvider) Test(context.Context, *forge.Client, forge.Host, string) (forge.Connection, error) {
	return forge.Connection{}, nil
}

// A read reaches the provider with the host's token, the branch and the
// head, a host without a token asks nothing, and a provider that cannot read
// a status says so.
func TestABranchStatusAsksTheProviderWithTheHostsToken(t *testing.T) {
	stub := &statusStub{}
	s := forge.New(t.TempDir(), []forge.Provider{github.Provider{}, stub, plainProvider{}}, github.Builtin(), gitlab.Builtin())
	w := forge.Watch{Project: "app", Binding: forge.Binding{Host: gitlab.Builtin(), Path: "group/app"}, Branch: "feature", Remote: "feature", Head: "abc"}
	if _, err := s.BranchStatus(context.Background(), w); !errors.Is(err, forge.ErrNoToken) || stub.tok != "" {
		t.Fatalf("a host without a token: %v, the provider got %q", err, stub.tok)
	}
	if err := s.ChangeToken("gitlab.com", token, false); err != nil {
		t.Fatal(err)
	}
	status, err := s.BranchStatus(context.Background(), w)
	if err != nil || status.Change == nil || status.Change.Number != 4 || stub.tok != token || stub.branch != "feature" || stub.head != "abc" {
		t.Fatalf("got %+v, %v, the provider got %+v", status, err, stub)
	}
	plain := addedWithToken(t, s, forge.Input{Kind: "plain", WebURL: "https://plain.example.com"}, token)
	w.Binding = forge.Binding{Host: plain, Path: "group/app"}
	if _, err := s.BranchStatus(context.Background(), w); !errors.Is(err, forge.ErrNoStatus) {
		t.Fatalf("a provider without the reading: %v", err)
	}
}

// An open change request wins whatever its head, one that is over only at
// the pushed head, and the newest of each.
func TestPickChangePrefersTheOpenOneThenTheOneAtTheHead(t *testing.T) {
	changes := []forge.Change{
		{Number: 5, State: forge.Merged, Head: "old"},
		{Number: 4, State: forge.Open, Head: "older"},
		{Number: 3, State: forge.Open, Head: "head"},
		{Number: 2, State: forge.Closed, Head: "head"},
	}
	if got := forge.PickChange(changes, "head"); got == nil || got.Number != 4 {
		t.Fatalf("picked %+v", got)
	}
	over := changes[:1:1]
	over = append(over, changes[3])
	for head, want := range map[string]int{"head": 2, "old": 5, "other": 0, "": 0} {
		got := forge.PickChange(over, head)
		if (want == 0) != (got == nil) || (got != nil && got.Number != want) {
			t.Errorf("at %q picked %+v, want %d", head, got, want)
		}
	}
}

func TestRollupCombinesTheJobs(t *testing.T) {
	job := func(s forge.Status) forge.Job { return forge.Job{Status: s} }
	allowed := forge.Job{Status: forge.Failed, AllowFailure: true}
	for _, c := range []struct {
		jobs []forge.Job
		want forge.Status
	}{
		{[]forge.Job{job(forge.Success), job(forge.Failed), job(forge.Running)}, forge.Running},
		{[]forge.Job{job(forge.Pending), job(forge.Failed)}, forge.Pending},
		{[]forge.Job{job(forge.Running), job(forge.Pending)}, forge.Running},
		{[]forge.Job{job(forge.Canceled), job(forge.Failed), job(forge.Manual)}, forge.Failed},
		{[]forge.Job{job(forge.Success), job(forge.Canceled), job(forge.Manual)}, forge.Canceled},
		{[]forge.Job{job(forge.Success), job(forge.Manual)}, forge.Manual},
		{[]forge.Job{job(forge.Success), allowed}, forge.Success},
		{[]forge.Job{allowed}, forge.Success},
		{[]forge.Job{job(forge.Skipped), job(forge.Skipped)}, forge.Skipped},
		{[]forge.Job{job(forge.Skipped), job(forge.Success)}, forge.Success},
	} {
		if got := forge.Rollup(c.jobs); got != c.want {
			t.Errorf("%+v rolled up to %s, want %s", c.jobs, got, c.want)
		}
	}
}

func TestAKindWritesItsChangeRequests(t *testing.T) {
	if got := gitlab.Kind.ChangeRef(12); got != "!12" || gitlab.Kind.ChangeNoun != "merge request" {
		t.Fatalf("GitLab writes %s, a %s", got, gitlab.Kind.ChangeNoun)
	}
	if got := github.Kind.ChangeRef(12); got != "#12" || github.Kind.ChangeNoun != "pull request" {
		t.Fatalf("GitHub writes %s, a %s", got, github.Kind.ChangeNoun)
	}
}
