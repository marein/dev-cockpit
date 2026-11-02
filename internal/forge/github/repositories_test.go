package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

// reposAnswer is GET /user/repos as GitHub answers it, trimmed to a few of
// its fields: a private fork, an internal archived repository of an
// enterprise and a public one, the most recently pushed first.
const reposAnswer = `[
{"id":1,"full_name":"octo/app","ssh_url":"git@%[1]s:octo/app.git","clone_url":"%[2]s/octo/app.git","private":true,"visibility":"private","fork":true,"archived":false,"pushed_at":"2026-11-01T10:00:00Z"},
{"id":2,"full_name":"corp/tool","ssh_url":"git@%[1]s:corp/tool.git","clone_url":"%[2]s/corp/tool.git","private":true,"visibility":"internal","fork":false,"archived":true,"pushed_at":"2026-10-01T10:00:00Z"},
{"id":3,"full_name":"octo/Site","ssh_url":"git@%[1]s:octo/Site.git","clone_url":"%[2]s/octo/Site.git","private":false,"visibility":"public","fork":false,"archived":false,"pushed_at":"2026-09-01T10:00:00Z"}
]`

type githubSearch struct {
	mu        sync.Mutex
	asked     []string
	remaining int
}

// fakeSearches answers the repository lists and the search below /api/v3
// for the token, the search with GitHub's own limit of its requests.
func (g *githubSearch) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Accept") != "application/vnd.github+json" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"Bad credentials","status":"401"}`)
			return
		}
		g.mu.Lock()
		g.asked = append(g.asked, r.URL.RequestURI())
		g.mu.Unlock()
		switch r.URL.Path {
		case "/api/v3/user/repos":
			fmt.Fprintf(w, reposAnswer, r.Host, "http://"+r.Host)
		case "/api/v3/search/repositories":
			g.mu.Lock()
			g.remaining--
			left := g.remaining
			g.mu.Unlock()
			w.Header().Set("X-RateLimit-Resource", "search")
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(left))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
			fmt.Fprintf(w, `{"total_count":3,"incomplete_results":false,"items":`+reposAnswer+`}`, r.Host, "http://"+r.Host)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found","status":"404"}`)
		}
	})
}

func (g *githubSearch) requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.asked...)
}

// The account's own repositories come from its list of every repository it
// owns, collaborates on or reaches through an organization, the most
// recently pushed first, narrowed by every word here, with what GitHub
// says of each.
func TestTheOwnRepositoriesAreTheListNarrowedByEveryWord(t *testing.T) {
	g := &githubSearch{}
	srv := httptest.NewServer(g.handler())
	defer srv.Close()
	ctx := context.Background()
	repos, err := Provider{}.OwnRepositories(ctx, forge.NewClient(), host(srv.URL), token, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.requests(); len(got) != 1 || got[0] != "/api/v3/user/repos?affiliation=owner,collaborator,organization_member&sort=pushed&per_page=100" {
		t.Fatalf("asked %v", got)
	}
	if len(repos) != 2 {
		t.Fatalf("got %+v", repos)
	}
	app, tool := repos[0], repos[1]
	if app.Path != "octo/app" || app.Visibility != forge.VisibilityPrivate || !app.Fork || app.Archived {
		t.Fatalf("app %+v", app)
	}
	if tool.Visibility != forge.VisibilityInternal || !tool.Archived {
		t.Fatalf("tool %+v", tool)
	}
	repos, err = Provider{}.OwnRepositories(ctx, forge.NewClient(), host(srv.URL), token, "site OCTO/", 8)
	if err != nil || len(repos) != 1 || repos[0].Path != "octo/Site" || repos[0].Visibility != "" {
		t.Fatalf("narrowed to %+v, %v", repos, err)
	}
}

// A public search asks GitHub's search for public repositories by name, an
// owner as the account, and is limited apart: a search that used up its
// limit holds back the next search, never a read of the lists.
func TestThePublicSearchIsLimitedApartFromTheRest(t *testing.T) {
	g := &githubSearch{remaining: 2}
	srv := httptest.NewServer(g.handler())
	defer srv.Close()
	ctx := context.Background()
	c := forge.NewClient()
	h := host(srv.URL)
	repos, err := Provider{}.PublicRepositories(ctx, c, h, token, "Octo/app tool", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.requests(); len(got) != 1 || got[0] != "/api/v3/search/repositories?per_page=2&q=app+tool+in%3Aname+is%3Apublic+user%3Aocto" {
		t.Fatalf("asked %v", got)
	}
	if len(repos) != 2 || repos[0].Path != "octo/app" {
		t.Fatalf("got %+v", repos)
	}
	if _, err := (Provider{}).PublicRepositories(ctx, c, h, token, "dev", 2); err != nil {
		t.Fatal(err)
	}
	var limited *forge.RateLimitError
	_, err = Provider{}.PublicRepositories(ctx, c, h, token, "dev", 2)
	if err == nil || !errors.As(err, &limited) {
		t.Fatalf("a search past the limit got %v", err)
	}
	if _, err := (Provider{}).OwnRepositories(ctx, c, h, token, "", 2); err != nil {
		t.Fatalf("the list was held back with the search: %v", err)
	}
	if got := g.requests(); len(got) != 3 {
		t.Fatalf("asked %v", got)
	}
	if repos, err := (Provider{}).PublicRepositories(ctx, c, h, token, "octo/", 2); err != nil || len(repos) != 0 || len(g.requests()) != 3 {
		t.Fatalf("an owner alone got %+v, %v", repos, err)
	}
	if got := searchAPI(c, Builtin(), token).Base(); got != "https://api.github.com/search" {
		t.Fatalf("github.com searches at %s", got)
	}
}
