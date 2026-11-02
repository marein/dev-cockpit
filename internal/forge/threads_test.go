package forge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

type threadStub struct {
	gitlab.Provider
	tok    string
	number int
}

func (p *threadStub) Threads(_ context.Context, _ *forge.Client, _ forge.Binding, token string, number int) (forge.Review, error) {
	p.tok, p.number = token, number
	return forge.Review{General: []forge.Thread{{ID: "1"}}}, nil
}

// A read of review threads reaches the provider with the host's token and
// the change request's number, a host without a token asks nothing, and a
// provider that cannot read threads says so.
func TestThreadsAskTheProviderWithTheHostsToken(t *testing.T) {
	stub := &threadStub{}
	s := forge.New(t.TempDir(), []forge.Provider{github.Provider{}, stub, plainProvider{}}, github.Builtin(), gitlab.Builtin())
	b := forge.Binding{Host: gitlab.Builtin(), Path: "group/app"}
	if _, err := s.Threads(context.Background(), b, 4); !errors.Is(err, forge.ErrNoToken) || stub.tok != "" {
		t.Fatalf("a host without a token: %v, the provider got %q", err, stub.tok)
	}
	if err := s.ChangeToken("gitlab.com", token, false); err != nil {
		t.Fatal(err)
	}
	review, err := s.Threads(context.Background(), b, 4)
	if err != nil || len(review.General) != 1 || stub.tok != token || stub.number != 4 {
		t.Fatalf("got %+v, %v, the provider got %+v", review, err, stub)
	}
	plain := addedWithToken(t, s, forge.Input{Kind: "plain", WebURL: "https://plain.example.com"}, token)
	if _, err := s.Threads(context.Background(), forge.Binding{Host: plain, Path: "group/app"}, 4); !errors.Is(err, forge.ErrNoThreads) {
		t.Fatalf("a provider without the reading: %v", err)
	}
}

// A note the forge keeps alone takes no write, a thread takes a reply, and a
// resolve only where the forge resolves it.
func TestAThreadTakesTheWritesItsForgeAllows(t *testing.T) {
	for _, c := range []struct {
		thread         forge.Thread
		reply, resolve bool
	}{
		{forge.Thread{ID: "d1", Resolvable: true}, true, true},
		{forge.Thread{ID: "d6"}, true, false},
		{forge.Thread{ID: "IC_1", Single: true}, false, false},
		{forge.Thread{ID: "IC_2", Single: true, Resolvable: true}, false, false},
	} {
		if reply, resolve := c.thread.Takes(); reply != c.reply || resolve != c.resolve {
			t.Fatalf("%+v takes a reply %v and a resolve %v", c.thread, reply, resolve)
		}
	}
}
