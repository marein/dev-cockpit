package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// The create form offers each host with a token as a choice of its own,
// whose search asks that host alone: the account's own repositories, the
// public ones apart and quietly, a term no search takes refused with 400, a
// host without a token with 404, and no guest reaches it. A host that lost
// its token leaves the address field with what was filled in.
func TestTheCreateFormSearchesOneForgeHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+forgeStatusToken || r.URL.Path != "/api/v4/projects" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
			return
		}
		if r.URL.Query().Get("visibility") == "public" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"500 Internal Server Error"}`)
			return
		}
		fmt.Fprintf(w, `[{"path_with_namespace":"team/app","ssh_url_to_repo":"git@%s:team/app.git","http_url_to_repo":"%s/team/app.git","visibility":"private"}]`, strings.Split(r.Host, ":")[0], "http://"+r.Host)
	}))
	defer srv.Close()
	s, raw := ownerPages(t, worktreeProjects(t, t.TempDir()))
	f := forge.New(t.TempDir(), []forge.Provider{github.Provider{}, gitlab.Provider{}}, github.Builtin(), gitlab.Builtin())
	s.SetForge(f)
	if form := raw(http.MethodGet, "/projects/new?create=clone", "", "").Body.String(); strings.Contains(form, `value="forge:`) || strings.Contains(form, "<dc-repo-search") {
		t.Fatal("the form offers a host without a token")
	}
	h := addForgeHost(t, f, forge.Input{Kind: "gitlab", WebURL: srv.URL}, forgeStatusToken)
	label := strings.TrimPrefix(srv.URL, "http://")
	form := raw(http.MethodGet, "/projects/new?create=clone", "", "").Body.String()
	if !strings.Contains(form, `<option value="forge:`+h.ID+`">From `+label+`</option>`) || strings.Contains(form, "From GitHub") || strings.Contains(form, "<dc-repo-search") {
		t.Fatalf("the form does not offer the host with its token:\n%s", form)
	}
	refill := url.Values{"create": {"forge:" + h.ID}, "clone_url": {"git@127.0.0.1:team/app.git"}}.Encode()
	form = raw(http.MethodGet, "/projects/new?"+refill, "", "").Body.String()
	for _, want := range []string{`<option value="forge:` + h.ID + `" selected>`, `data-repo-host="` + h.ID + `"`, `value="git@127.0.0.1:team/app.git" data-clone-url data-repo-value data-repo-path="team/app"`, `id="repository" value="team/app"`} {
		if !strings.Contains(form, want) {
			t.Fatalf("the search shape lacks %s:\n%s", want, form)
		}
	}
	read := func(path string) (int, render.RepositoryChoices) {
		rec := raw(http.MethodGet, path, "", "")
		var got render.RepositoryChoices
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("%s answered %s", path, rec.Body.String())
			}
		}
		return rec.Code, got
	}
	code, own := read("/projects/new/repositories?host=" + h.ID + "&q=app")
	if code != http.StatusOK || len(own.Repositories) != 1 || own.Repositories[0].URL != "git@127.0.0.1:team/app.git" || own.Repositories[0].Hint != "private" || own.Failure != "" {
		t.Fatalf("own answered %d %+v", code, own)
	}
	if code, public := read("/projects/new/repositories?host=" + h.ID + "&q=app&scope=public"); code != http.StatusOK || len(public.Repositories) != 0 || public.Failure != "" {
		t.Fatalf("a failed public search answered %d %+v", code, public)
	}
	rec := raw(http.MethodGet, "/projects/new/repositories?host="+h.ID+"&q=git%40host%3Aa%2Fb", "", "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "holds no colon") {
		t.Fatalf("an address answered %d %s", rec.Code, rec.Body.String())
	}
	for _, host := range []string{"github.com", "nope", ""} {
		if rec := raw(http.MethodGet, "/projects/new/repositories?host="+host+"&q=app", "", ""); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no token") {
			t.Fatalf("host %q answered %d %s", host, rec.Code, rec.Body.String())
		}
	}
	post := url.Values{"create": {"forge:" + h.ID}, "project_name": {"picked"}}.Encode()
	if rec := raw(http.MethodPost, "/projects", "application/x-www-form-urlencoded", post); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "create=forge%3A"+h.ID) {
		t.Fatalf("a create of the search answered %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if _, err := s.projects.FindByName("picked"); err == nil {
		t.Fatal("a create of the search without a pick made a project")
	}
	if err := f.ChangeToken(h.ID, "", true); err != nil {
		t.Fatal(err)
	}
	form = raw(http.MethodGet, "/projects/new?"+refill, "", "").Body.String()
	if strings.Contains(form, "<dc-repo-search") || !strings.Contains(form, `<option value="clone" selected>`) || !strings.Contains(form, `id="clone_url" name="clone_url" value="git@127.0.0.1:team/app.git"`) {
		t.Fatalf("a host without its token kept its search:\n%s", form)
	}
	if !neverForGuest("/projects/new/repositories") {
		t.Fatal("a guest reaches the search")
	}
}
