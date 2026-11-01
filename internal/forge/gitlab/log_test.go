package gitlab_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

func jobLog(t *testing.T, webURL, tok string, job forge.Job) ([]byte, bool, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b := forge.Binding{Host: host(webURL), Path: "group/sub/app"}
	raw, cut, err := gitlab.Provider{}.JobLog(ctx, forge.NewClient(), b, tok, job, 1024)
	if err != nil && strings.Contains(err.Error(), tok) {
		t.Fatalf("the error names the token: %v", err)
	}
	return raw, cut, err
}

// A job's trace is read below the project, its path one encoded segment,
// as the runner wrote it, colours and section marks and all.
func TestAJobLogIsItsTrace(t *testing.T) {
	trace := "section_start:1730000002:step_script\r\x1b[0K\x1b[32;1m$ go test\x1b[0;m\nFAIL\n"
	f := &fakeProject{traces: map[string]string{"32": trace}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	raw, cut, err := jobLog(t, srv.URL, token, forge.Job{ID: "32", Name: "unit"})
	if err != nil || string(raw) != trace || cut {
		t.Fatalf("got %q, %v, %v", raw, cut, err)
	}
	if asked := f.asked(); len(asked) != 1 || asked[0] != projectAt+"/jobs/32/trace?" {
		t.Fatalf("asked %v", asked)
	}
}

// A trace GitLab does not answer is said with the job's name, a refused
// token and a token without the API scope as for every read.
func TestAJobLogGitLabRefusesIsSaid(t *testing.T) {
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"403 Forbidden"}`)
	}))
	defer forbidden.Close()
	scope := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, scopeAnswer)
	}))
	defer scope.Close()
	gone := httptest.NewServer(&fakeProject{})
	defer gone.Close()
	job := forge.Job{ID: "32", Name: "unit\ntest"}
	for _, c := range []struct {
		name, webURL, tok, says string
	}{
		{"no trace", gone.URL, token, `GitLab has no log of "unit\ntest" in ` + strings.TrimPrefix(gone.URL, "http://") + "/group/sub/app, or the token may not read it."},
		{"forbidden", forbidden.URL, token, `The token may not read the log of "unit\ntest" on GitLab.`},
		{"missing scope", scope.URL, token, "The token may not read the API: give it the scope read_api, or api to also write."},
		{"bad token", gone.URL, "glpat-wrong-wrong-wrong", "GitLab refused the token: it is wrong, expired or revoked."},
	} {
		if _, _, err := jobLog(t, c.webURL, c.tok, job); err == nil || err.Error() != c.says {
			t.Errorf("%s: %v, want %q", c.name, err, c.says)
		}
	}
}

// A project whose old path the origin still names is redirected by GitLab,
// which says the move as much as the web address.
func TestAMovedProjectIsSaidSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/v4/projects/5/merge_requests?private_token=x", http.StatusMovedPermanently)
	}))
	defer srv.Close()
	address := strings.TrimPrefix(srv.URL, "http://") + "/group/sub/app"
	want := "The forge redirects the read of " + address + " to " + srv.URL + "/api/v4/projects/5/merge_requests, which is not followed: if the repository was renamed or moved, give the origin its new path or set it under the project's Forge, else check the host's web address."
	if _, err := branchStatus(t, srv.URL, token, "feature/login", head); err == nil || err.Error() != want {
		t.Fatalf("a status read: %v", err)
	}
	if _, _, err := jobLog(t, srv.URL, token, forge.Job{ID: "32", Name: "unit"}); err == nil || !strings.HasPrefix(err.Error(), "The forge redirects the read of "+address+" to ") {
		t.Fatalf("a log read: %v", err)
	}
}
