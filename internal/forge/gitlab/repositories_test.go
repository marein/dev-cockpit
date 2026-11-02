package gitlab_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

// projectsAnswer is GET /projects as GitLab 17.5 answers it, trimmed to a
// few of its fields: a private fork, an internal archived project, a public
// one and two projects named like their last path segment in a nested
// group, one of them with a two character name, the most recently active
// first.
const projectsAnswer = `[
{"id":3,"name":"app","name_with_namespace":"Team / Sub / app","path":"app","path_with_namespace":"team/sub/app","description":"","ssh_url_to_repo":"git@%[1]s:team/sub/app.git","http_url_to_repo":"%[2]s/team/sub/app.git","visibility":"private","archived":false,"last_activity_at":"2026-11-01T10:00:00.000Z","forked_from_project":{"id":1,"path_with_namespace":"upstream/app"}},
{"id":4,"name":"tool","name_with_namespace":"Team / tool","path":"tool","path_with_namespace":"team/tool","description":"Builds the garden","ssh_url_to_repo":"git@%[1]s:team/tool.git","http_url_to_repo":"%[2]s/team/tool.git","visibility":"internal","archived":true,"last_activity_at":"2026-10-01T10:00:00.000Z"},
{"id":5,"name":"site","name_with_namespace":"marein / site","path":"site","path_with_namespace":"marein/site","description":null,"ssh_url_to_repo":"git@%[1]s:marein/site.git","http_url_to_repo":"%[2]s/marein/site.git","visibility":"public","archived":false,"last_activity_at":"not a time","forked_from_project":null},
{"id":6,"name":"app","name_with_namespace":"Orbit / Garden Portal / app","path":"app","path_with_namespace":"orbit/garden-portal/app","description":"","ssh_url_to_repo":"git@%[1]s:orbit/garden-portal/app.git","http_url_to_repo":"%[2]s/orbit/garden-portal/app.git","visibility":"private","archived":false,"last_activity_at":"2026-09-01T10:00:00.000Z"},
{"id":7,"name":"ui","name_with_namespace":"Orbit / Garden Portal / ui","path":"ui","path_with_namespace":"orbit/garden-portal/ui","description":"","ssh_url_to_repo":"git@%[1]s:orbit/garden-portal/ui.git","http_url_to_repo":"%[2]s/orbit/garden-portal/ui.git","visibility":"private","archived":false,"last_activity_at":"2026-08-01T10:00:00.000Z"}
]`

// gitlabMatches is GitLab's search of projects (Gitlab::SQL::Pattern): a
// project matches when one of its fields carries every word of at least
// three characters, the shorter words beside them dropped, or equals a term
// of shorter words alone. The fields are the path, the name and the
// description, the path and the name with every group only under
// search_namespaces, so a short name in a group is found only without it.
func gitlabMatches(p map[string]any, q url.Values) bool {
	words := strings.Fields(strings.ToLower(q.Get("search")))
	if len(words) == 0 {
		return true
	}
	var long []string
	for _, w := range words {
		if utf8.RuneCountInString(w) >= 3 {
			long = append(long, w)
		}
	}
	fields := []string{"path", "name", "description"}
	if q.Get("search_namespaces") == "true" {
		fields = []string{"path_with_namespace", "name_with_namespace", "description"}
	}
	for _, f := range fields {
		value, _ := p[f].(string)
		value = strings.ToLower(value)
		if len(long) == 0 && value == strings.Join(words, " ") {
			return true
		}
		if len(long) > 0 && forge.Carries(value, long) {
			return true
		}
	}
	return false
}

// fakeProjects answers GET /projects for the token the way GitLab matches a
// search, at most per_page, and keeps each query.
func fakeProjects(asked *[]url.Values, mu *sync.Mutex) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
			return
		}
		if r.URL.Path != "/gl/api/v4/projects" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"404 Not Found"}`)
			return
		}
		q := r.URL.Query()
		mu.Lock()
		*asked = append(*asked, q)
		mu.Unlock()
		var all []map[string]any
		if err := json.Unmarshal([]byte(fmt.Sprintf(projectsAnswer, r.Host, "http://"+r.Host+"/gl")), &all); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		perPage, err := strconv.Atoi(q.Get("per_page"))
		if err != nil {
			perPage = 20
		}
		list := []map[string]any{}
		for _, p := range all {
			if len(list) < perPage && gitlabMatches(p, q) {
				list = append(list, p)
			}
		}
		json.NewEncoder(w).Encode(list)
	})
}

func searchProjects(t *testing.T, public bool, term string, tok string) ([]forge.Repository, []url.Values, error) {
	t.Helper()
	var (
		asked []url.Values
		mu    sync.Mutex
	)
	srv := httptest.NewServer(fakeProjects(&asked, &mu))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	search := gitlab.Provider{}.OwnRepositories
	if public {
		search = gitlab.Provider{}.PublicRepositories
	}
	repos, err := search(ctx, forge.NewClient(), host(srv.URL+"/gl"), tok, term, 2)
	if err != nil && strings.Contains(err.Error(), tok) {
		t.Fatalf("the error names the token: %v", err)
	}
	return repos, asked, err
}

// The account's own projects are those it is a member of, the most
// recently active first, with what GitLab says of each, at most the limit.
func TestTheOwnProjectsAreTheMembershipsByActivity(t *testing.T) {
	repos, asked, err := searchProjects(t, false, "", token)
	if err != nil {
		t.Fatal(err)
	}
	q := asked[0]
	if q.Get("membership") != "true" || q.Get("order_by") != "last_activity_at" || q.Get("sort") != "desc" || q.Get("per_page") != "2" || q.Has("search") || q.Has("search_namespaces") {
		t.Fatalf("asked %v", q)
	}
	if len(repos) != 2 {
		t.Fatalf("got %+v", repos)
	}
	app, tool := repos[0], repos[1]
	if app.Path != "team/sub/app" || !strings.HasPrefix(app.SSHURL, "git@127.0.0.1") || !strings.HasSuffix(app.HTTPURL, "/gl/team/sub/app.git") || app.Visibility != "private" || !app.Fork || app.Archived {
		t.Fatalf("app %+v", app)
	}
	if tool.Visibility != "internal" || tool.Fork || !tool.Archived {
		t.Fatalf("tool %+v", tool)
	}
}

// GitLab is asked to match the words of three characters and more over
// the full path with its groups, lowercased with the whitespace collapsed,
// so a word that stands only in a group finds the project, in any order and
// with a slash too, and the page stays at the limit. A shorter word, which
// GitLab drops from such a search, narrows a longer page by the path here.
// A term of short words alone is asked for the project's own path and name,
// which a short name equals and the full path never does.
func TestTheOwnSearchMatchesWordsAnywhereInTheFullPath(t *testing.T) {
	for _, c := range []struct {
		term, search string
		namespaces   bool
		perPage      string
		want         []string
	}{
		{"  Garden   Portal  app ", "garden portal app", true, "2", []string{"orbit/garden-portal/app"}},
		{"app portal garden", "app portal garden", true, "2", []string{"orbit/garden-portal/app"}},
		{"orbit", "orbit", true, "2", []string{"orbit/garden-portal/app", "orbit/garden-portal/ui"}},
		{"team app", "team app", true, "2", []string{"team/sub/app"}},
		{"garden-portal/app", "garden-portal/app", true, "2", []string{"orbit/garden-portal/app"}},
		{"Marein/Si", "marein/si", true, "2", []string{"marein/site"}},
		{"garden", "garden", true, "2", []string{"team/tool", "orbit/garden-portal/app"}},
		{"app tool", "app tool", true, "2", nil},
		{"ui", "ui", false, "2", []string{"orbit/garden-portal/ui"}},
		{" UI ", "ui", false, "2", []string{"orbit/garden-portal/ui"}},
		{"ui garden", "garden", true, "100", []string{"orbit/garden-portal/ui"}},
		{"garden UI", "garden", true, "100", []string{"orbit/garden-portal/ui"}},
		{"ui ap", "ui ap", false, "2", nil},
	} {
		repos, asked, err := searchProjects(t, false, c.term, token)
		if err != nil {
			t.Fatal(err)
		}
		q := asked[0]
		if q.Get("membership") != "true" || q.Get("search") != c.search || q.Has("search_namespaces") != c.namespaces || (c.namespaces && q.Get("search_namespaces") != "true") || q.Get("per_page") != c.perPage || q.Has("archived") || q.Has("simple") {
			t.Fatalf("%q asked %v", c.term, q)
		}
		var got []string
		for _, r := range repos {
			got = append(got, r.Path)
		}
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Fatalf("%q found %v", c.term, got)
		}
	}
}

// A public search asks for public projects by their stars, never for the
// namespaces, which gitlab.com fails, and asks nothing for an owner alone.
func TestThePublicSearchAsksForPublicProjectsByStars(t *testing.T) {
	repos, asked, err := searchProjects(t, true, "marein/app", token)
	if err != nil {
		t.Fatal(err)
	}
	q := asked[0]
	if q.Get("visibility") != "public" || q.Get("search") != "app" || q.Get("order_by") != "star_count" || q.Get("sort") != "desc" || q.Get("per_page") != "2" || q.Has("membership") || q.Has("search_namespaces") {
		t.Fatalf("asked %v", q)
	}
	if len(repos) != 0 {
		t.Fatalf("an owner none of the answers carries kept %+v", repos)
	}
	if repos, asked, err := searchProjects(t, true, "marein/", token); err != nil || len(repos) != 0 || len(asked) != 0 {
		t.Fatalf("an owner alone asked %v and got %+v, %v", asked, repos, err)
	}
}

// A refused token is said in GitLab's words, without the token.
func TestASearchWithAWrongTokenSaysSo(t *testing.T) {
	if _, _, err := searchProjects(t, false, "", "glpat-wrong0000000000"); err == nil || err.Error() != "GitLab refused the token: it is wrong, expired or revoked." {
		t.Fatalf("got %v", err)
	}
}
