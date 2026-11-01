package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

// fakeReruns is an Enterprise Server that lists the workflow runs of head
// the way GitHub answers them and re-runs what it is asked, 201 with an
// empty object, or answers what a refusal names for its path.
type fakeReruns struct {
	runs     []string
	refusals map[string]int

	mu    sync.Mutex
	asked []string
}

func runAnswer(id int, name, sha, status, conclusion string) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"node_id":"WFR_kwLOe2e%d","head_branch":"feature/login","head_sha":%q,"path":".github/workflows/ci.yml","run_number":%d,"event":"push","status":%q,"conclusion":%q,"workflow_id":159038,"check_suite_id":%d,"html_url":"https://github.example.com/octo/app/actions/runs/%d","run_attempt":1}`,
		id, name, id, sha, id, status, conclusion, id+100, id)
}

func (f *fakeReruns) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.asked = append(f.asked, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
	code := f.refusals[r.URL.EscapedPath()]
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch {
	case r.Header.Get("Authorization") != "Bearer "+token:
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials","documentation_url":"https://docs.github.com/rest","status":"401"}`)
	case code == http.StatusForbidden:
		w.WriteHeader(code)
		fmt.Fprint(w, `{"message":"Resource not accessible by personal access token","documentation_url":"https://docs.github.com/rest/actions/workflow-runs#re-run-failed-jobs-from-a-workflow-run","status":"403"}`)
	case code == http.StatusConflict:
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Unable to re-run this workflow run because it was created over a month ago","status":"403"}`)
	case code != 0:
		w.WriteHeader(code)
		fmt.Fprintf(w, `{"message":%q,"status":"%d"}`, http.StatusText(code), code)
	case r.Method == http.MethodGet && r.URL.Path == repoAt+"/actions/runs":
		fmt.Fprintf(w, `{"total_count":%d,"workflow_runs":[%s]}`, len(f.runs), strings.Join(f.runs, ","))
	case r.Method == http.MethodPost:
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeReruns) posts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, a := range f.asked {
		if strings.HasPrefix(a, "POST ") {
			out = append(out, strings.TrimSuffix(a, "?"))
		}
	}
	return out
}

func rerun(t *testing.T, webURL, tok string, job *forge.Job) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b := forge.Binding{Host: host(webURL), Path: "octo/app"}
	var err error
	if job != nil {
		err = Provider{}.RetryJob(ctx, forge.NewClient(), b, tok, *job)
	} else {
		err = Provider{}.RetryPipeline(ctx, forge.NewClient(), b, tok, head, forge.Pipeline{Status: forge.Failed})
	}
	if err != nil && strings.Contains(err.Error(), tok) {
		t.Fatalf("the error names the token: %v", err)
	}
	return err
}

// The pipeline's failed jobs run again by workflow run: each run of head
// that ended failed or timed out re-runs its failed jobs, one that passed,
// still runs or ran on another commit is left alone. One job re-runs by its
// id, its check run's.
func TestARerunRunsTheFailedJobsOfEveryFailedWorkflowRun(t *testing.T) {
	f := &fakeReruns{runs: []string{
		runAnswer(11, "CI", head, "completed", "failure"),
		runAnswer(12, "Lint", head, "completed", "success"),
		runAnswer(13, "E2E", head, "completed", "timed_out"),
		runAnswer(14, "Deploy", head, "in_progress", ""),
		runAnswer(15, "Old", older, "completed", "failure"),
	}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	if err := rerun(t, srv.URL, token, nil); err != nil {
		t.Fatal(err)
	}
	if f.asked[0] != "GET "+repoAt+"/actions/runs?exclude_pull_requests=true&head_sha="+head+"&per_page=100" {
		t.Fatalf("the runs were read at %s", f.asked[0])
	}
	if want := []string{"POST " + repoAt + "/actions/runs/11/rerun-failed-jobs", "POST " + repoAt + "/actions/runs/13/rerun-failed-jobs"}; !slices.Equal(f.posts(), want) {
		t.Fatalf("it asked %q", f.posts())
	}
	if err := rerun(t, srv.URL, token, &forge.Job{ID: "150", Name: "unit", Status: forge.Failed}); err != nil {
		t.Fatal(err)
	}
	if posts := f.posts(); posts[len(posts)-1] != "POST "+repoAt+"/actions/jobs/150/rerun" {
		t.Fatalf("it asked %q", posts)
	}
}

// What GitHub refuses is said: a token that may not, a repository it hides
// from the token, a re-run it refuses in its own words, a run that failed
// after another went through, nothing failed to re-run, a wrong token.
func TestARerunGitHubRefusesIsSaid(t *testing.T) {
	failed := []string{runAnswer(11, "CI", head, "completed", "failure"), runAnswer(13, "E2E", head, "completed", "failure")}
	scope := "a fine grained token needs the permission Actions read and write, a classic one the scope repo."
	for _, c := range []struct {
		name     string
		runs     []string
		refusals map[string]int
		tok      string
		job      *forge.Job
		says     string
	}{
		{"scope", failed, map[string]int{repoAt + "/actions/runs/11/rerun-failed-jobs": 403}, token, nil, "The token may not re-run jobs of %s: " + scope},
		{"hidden", failed, map[string]int{repoAt + "/actions/runs": 404}, token, nil, "GitHub knows no workflow run at the pushed head in %s, or the token may not re-run it: " + scope},
		{"refused", failed, map[string]int{repoAt + "/actions/runs/11/rerun-failed-jobs": 409}, token, nil, `GitHub refused to re-run workflow run "CI": Unable to re-run this workflow run because it was created over a month ago.`},
		{"second run", failed, map[string]int{repoAt + "/actions/runs/13/rerun-failed-jobs": 409}, token, nil, `The failed jobs of "CI" run again, those of "E2E" do not. GitHub refused to re-run workflow run "E2E": Unable to re-run this workflow run because it was created over a month ago.`},
		{"nothing failed", []string{runAnswer(12, "Lint", head, "completed", "success")}, nil, token, nil, "GitHub lists no failed workflow run at the pushed head."},
		{"job gone", nil, map[string]int{repoAt + "/actions/jobs/150/rerun": 404}, token, &forge.Job{ID: "150", Name: "unit\ntest"}, `GitHub knows no job "unit\ntest" in %s, or the token may not re-run it: ` + scope},
		{"job scope", nil, map[string]int{repoAt + "/actions/jobs/150/rerun": 403}, token, &forge.Job{ID: "150", Name: "unit"}, "The token may not re-run jobs of %s: " + scope},
		{"bad token", failed, nil, "ghp_wrong", nil, "GitHub refused the token: it is wrong, expired or revoked."},
	} {
		srv := httptest.NewServer(&fakeReruns{runs: c.runs, refusals: c.refusals})
		says := c.says
		if strings.Contains(says, "%s") {
			says = fmt.Sprintf(says, strings.TrimPrefix(srv.URL, "http://")+"/octo/app")
		}
		if err := rerun(t, srv.URL, c.tok, c.job); err == nil || err.Error() != says {
			t.Errorf("%s: %v, want %q", c.name, err, says)
		}
		srv.Close()
	}
}

// GitHub re-runs failed jobs, and its kind says so in its users' verb.
func TestGitHubRerunsInItsOwnWord(t *testing.T) {
	var p forge.Provider = Provider{}
	if _, ok := p.(forge.Retrier); !ok || p.Kind().Retry != "Re-run" {
		t.Fatalf("the kind reads %+v", p.Kind())
	}
}
