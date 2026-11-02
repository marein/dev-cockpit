package forge_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

type searchCall struct {
	host, token, term string
	limit             int
}

// searchStub is a provider whose searches answer what the test hands it,
// keeping every call.
type searchStub struct {
	forge.Provider
	mu     *sync.Mutex
	calls  *[]searchCall
	own    func(h forge.Host, term string) ([]forge.Repository, error)
	public func(ctx context.Context, h forge.Host, term string) ([]forge.Repository, error)
}

func (p searchStub) record(h forge.Host, token, term string, limit int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	*p.calls = append(*p.calls, searchCall{host: h.ID, token: token, term: term, limit: limit})
}

func (p searchStub) OwnRepositories(_ context.Context, _ *forge.Client, h forge.Host, token, term string, limit int) ([]forge.Repository, error) {
	p.record(h, token, term, limit)
	return p.own(h, term)
}

func (p searchStub) PublicRepositories(ctx context.Context, _ *forge.Client, h forge.Host, token, term string, limit int) ([]forge.Repository, error) {
	p.record(h, token, term, limit)
	return p.public(ctx, h, term)
}

// searchService is github.com, gitlab.com and a self hosted GitLab, the
// first and the last with a token, both kinds answering through stubs.
func searchService(t *testing.T, own func(h forge.Host, term string) ([]forge.Repository, error), public func(ctx context.Context, h forge.Host, term string) ([]forge.Repository, error)) (*forge.Service, forge.Host, *[]searchCall, *sync.Mutex) {
	t.Helper()
	var (
		calls []searchCall
		mu    sync.Mutex
	)
	stub := func(p forge.Provider) forge.Provider {
		return searchStub{Provider: p, mu: &mu, calls: &calls, own: own, public: public}
	}
	s := forge.New(t.TempDir(), []forge.Provider{stub(github.Provider{}), stub(gitlab.Provider{})}, github.Builtin(), gitlab.Builtin())
	self, err := s.Add(forge.Input{Kind: "gitlab", WebURL: "https://git.example.com", Aliases: []string{"ssh.git.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	for id, tok := range map[string]string{"github.com": "ghp_one", self.ID: "glpat_two"} {
		if err := s.ChangeToken(id, tok, false); err != nil {
			t.Fatal(err)
		}
	}
	return s, self, &calls, &mu
}

func paths(found []forge.Found) []string {
	var out []string
	for _, f := range found {
		out = append(out, f.Path)
	}
	return out
}

// The hosts a search asks are those with a token, and a search asks the one
// it names alone, with its own token and the term as typed, at most a page.
// A host without a token, an unknown one and one that fails answer an error.
func TestTheOwnSearchAsksTheOneHostItNames(t *testing.T) {
	s, self, calls, _ := searchService(t, func(h forge.Host, term string) ([]forge.Repository, error) {
		if h.ID == "github.com" {
			return nil, errors.New("GitHub refused the token: it is wrong, expired or revoked.")
		}
		var repos []forge.Repository
		for i := range 10 {
			repos = append(repos, forge.Repository{Path: "team/app" + string(rune('a'+i)), SSHURL: "git@git.example.com:team/app" + string(rune('a'+i)) + ".git"})
		}
		return repos, nil
	}, nil)
	hosts, err := s.SearchHosts()
	if err != nil || len(hosts) != 2 || hosts[0].ID != "github.com" || hosts[1].ID != self.ID {
		t.Fatalf("search hosts %+v, %v", hosts, err)
	}
	found, err := s.OwnRepositories(context.Background(), self.ID, "  app  ")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"team/appa", "team/appb", "team/appc", "team/appd", "team/appe", "team/appf", "team/appg", "team/apph"}
	if got := paths(found); !slices.Equal(got, want) || found[0].Host.ID != self.ID {
		t.Fatalf("found %v, want %v", got, want)
	}
	if got := *calls; !slices.Equal(got, []searchCall{{self.ID, "glpat_two", "app", forge.RepositoryPage}}) {
		t.Fatalf("asked %+v", got)
	}
	if _, err := s.OwnRepositories(context.Background(), "github.com", ""); err == nil || !strings.Contains(err.Error(), "refused the token") {
		t.Fatalf("a failing host answered %v", err)
	}
	for _, id := range []string{"gitlab.com", "nope", ""} {
		if _, err := s.OwnRepositories(context.Background(), id, ""); !errors.Is(err, forge.ErrNoSearch) {
			t.Fatalf("%q answered %v", id, err)
		}
	}
	if len(*calls) != 2 {
		t.Fatalf("asked %+v", *calls)
	}
}

// The forge writes the addresses, so only one that binds to the host itself
// at the row's own path is filled in: SSH first, its https one where the SSH
// address is unusable, a repository without either left out, each once.
func TestOnlyAnAddressOnTheHostAtTheRowsPathIsFilledIn(t *testing.T) {
	long := "git@git.example.com:team/" + strings.Repeat("a", 500) + ".git"
	s, self, _, _ := searchService(t, func(h forge.Host, term string) ([]forge.Repository, error) {
		return []forge.Repository{
			{Path: "team/ssh", SSHURL: "git@git.example.com:team/ssh.git", HTTPURL: "https://git.example.com/team/ssh.git", Visibility: "private"},
			{Path: "team/alias", SSHURL: "ssh://git@ssh.git.example.com:2222/team/alias.git", Visibility: "internal", Fork: true, Archived: true},
			{Path: "team/elsewhere", SSHURL: "git@evil.example.com:team/elsewhere.git", HTTPURL: "https://git.example.com/team/elsewhere.git", Visibility: "public"},
			{Path: "team/other", SSHURL: "git@git.example.com:team/another.git"},
			{Path: "team/option", SSHURL: "-oProxyCommand=x:team/option.git"},
			{Path: "team/line", SSHURL: "git@git.example.com:team/line.git\n", HTTPURL: "https://user:pw@git.example.com/team/line.git"},
			{Path: "team/plain", SSHURL: "", HTTPURL: "http://git.example.com/team/plain.git"},
			{Path: "team/" + strings.Repeat("a", 500), SSHURL: long},
			{Path: "TEAM/SSH", SSHURL: "git@git.example.com:TEAM/SSH.git"},
			{Path: "team/ext", SSHURL: "ext::sh -c touch% /tmp/pwned"},
		}, nil
	}, nil)
	found, err := s.OwnRepositories(context.Background(), self.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]forge.Found{}
	for _, f := range found {
		got[f.Path] = f
	}
	if len(found) != 3 {
		t.Fatalf("offered %v", paths(found))
	}
	if f := got["team/ssh"]; f.CloneURL != "git@git.example.com:team/ssh.git" || f.Visibility != forge.VisibilityPrivate || f.Fork || f.Archived {
		t.Fatalf("team/ssh %+v", f)
	}
	if f := got["team/alias"]; f.CloneURL != "ssh://git@ssh.git.example.com:2222/team/alias.git" || f.Visibility != forge.VisibilityInternal || !f.Fork || !f.Archived {
		t.Fatalf("team/alias %+v", f)
	}
	if f := got["team/elsewhere"]; f.CloneURL != "https://git.example.com/team/elsewhere.git" || f.Visibility != "" {
		t.Fatalf("team/elsewhere %+v", f)
	}
}

// A term that a forge's query would read as its syntax, or that hides a
// character, is refused before any host is asked.
func TestASearchTermIsCheckedBeforeAnyHostIsAsked(t *testing.T) {
	s, _, calls, _ := searchService(t, func(forge.Host, string) ([]forge.Repository, error) { return nil, nil }, func(context.Context, forge.Host, string) ([]forge.Repository, error) { return nil, nil })
	for _, term := range []string{"git@github.com:a/b", `say "dev"`, "dev:x", strings.Repeat("a", forge.MaxRepositorySearch+1), "dev\x00", "dev\u200bcockpit", "dev\tcockpit", "dev\u00a0cockpit", "\xff\xfe"} {
		for name, search := range map[string]func(context.Context, string, string) ([]forge.Found, error){"own": s.OwnRepositories, "public": s.PublicRepositories} {
			var refused *forge.SearchRefused
			if _, err := search(context.Background(), "github.com", term); !errors.As(err, &refused) {
				t.Fatalf("the %s search took %q: %v", name, term, err)
			}
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("asked %+v", *calls)
	}
	for _, term := range []string{"", "marein/dev cockpit", "app 🚀", strings.Repeat("ä", forge.MaxRepositorySearch), "my_app.v2+x"} {
		if reason := forge.SearchRefusal(term); reason != "" {
			t.Fatalf("%q refused: %s", term, reason)
		}
	}
	if got := forge.SearchRefusal("ab\u200bc"); got != "A search term holds no control characters and no invisible characters but the space, its character 3 is U+200B." {
		t.Fatalf("got %q", got)
	}
	if got := forge.SearchRefusal("ab cd\u200b"); !strings.HasSuffix(got, "its character 6 is U+200B.") {
		t.Fatalf("got %q", got)
	}
}

// A public search starts at three characters and waits for its host no
// longer than its bound, and a host that hangs holds no other's search.
func TestThePublicSearchStartsAtThreeCharactersAndWaitsBounded(t *testing.T) {
	s, self, calls, mu := searchService(t, nil, func(ctx context.Context, h forge.Host, term string) ([]forge.Repository, error) {
		if h.ID == "github.com" {
			return []forge.Repository{
				{Path: "a/dev1", SSHURL: "git@github.com:a/dev1.git"},
				{Path: "a/dev2", SSHURL: "git@github.com:a/dev2.git"},
			}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	for _, term := range []string{"", "de", " de ", "äö"} {
		found, err := s.PublicRepositories(context.Background(), "github.com", term)
		if err != nil || len(found) != 0 {
			t.Fatalf("%q answered %+v, %v", term, found, err)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("a short term asked %+v", *calls)
	}
	start := time.Now()
	slow := make(chan error, 1)
	go func() {
		_, err := s.PublicRepositories(context.Background(), self.ID, "dev")
		slow <- err
	}()
	found, err := s.PublicRepositories(context.Background(), "github.com", "dev")
	if err != nil || !slices.Equal(paths(found), []string{"a/dev1", "a/dev2"}) || time.Since(start) > time.Second {
		t.Fatalf("the hanging host held the other: %v after %s, %v", paths(found), time.Since(start), err)
	}
	err = <-slow
	took := time.Since(start)
	if err == nil || took > 7*time.Second || took < 4*time.Second {
		t.Fatalf("the slow search took %s: %v", took, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 2 || (*calls)[0].limit != forge.RepositoryPage {
		t.Fatalf("asked %+v", *calls)
	}
}

// A term's words: lowercased, and for a forge's search of names what
// follows a word's last slash, the owner before it apart.
func TestATermsWordsSplitOwnerAndName(t *testing.T) {
	names, owners := forge.NameWords(" Marein/Dev  cockpit marein/ /app group/sub/ ")
	if !slices.Equal(names, []string{"dev", "cockpit", "app"}) || !slices.Equal(owners, []string{"marein", "marein", "group/sub"}) {
		t.Fatalf("names %v, owners %v", names, owners)
	}
	if !forge.Carries("Marein/Dev-Cockpit", forge.SearchWords("cock MAREIN/dev")) || forge.Carries("marein/dev-cockpit", forge.SearchWords("cockpit other")) || !forge.Carries("x/y", nil) {
		t.Fatal("Carries")
	}
}
