package gitlab_test

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
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

// fakeWrites answers the retries of a GitLab 19.5 the way it answers them:
// a pipeline's retry and a job's with 201 and what runs again, and every
// path its refusals name with their status and body.
type fakeWrites struct {
	mu       sync.Mutex
	posts    []string
	refusals map[string]refusal
}

type refusal struct {
	code int
	body string
}

func (f *fakeWrites) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.posts = append(f.posts, r.Method+" "+r.URL.EscapedPath())
	answer, refused := f.refusals[r.URL.EscapedPath()]
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Header.Get("Authorization") != "Bearer "+token:
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
	case refused:
		w.WriteHeader(answer.code)
		fmt.Fprint(w, answer.body)
	case strings.HasSuffix(r.URL.Path, "/pipelines/12/retry"):
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":12,"iid":3,"project_id":2,"status":"failed","ref":"feature/login","web_url":"https://gitlab.example.com/group/sub/app/-/pipelines/12"}`)
	default:
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":40,"status":"pending","stage":"test","name":"unit","allow_failure":false,"web_url":"https://gitlab.example.com/group/sub/app/-/jobs/40"}`)
	}
}

func (f *fakeWrites) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.posts)
}

func failedPipeline() forge.Pipeline {
	return forge.Pipeline{ID: "12", Status: forge.Failed, Jobs: []forge.Job{
		{ID: "31", Name: "build", Status: forge.Success},
		{ID: "32", Name: "unit", Status: forge.Failed},
		{ID: "34", Name: "child", Status: forge.Failed, NoLog: "A trigger job has no log, the jobs of its downstream pipeline do."},
		{ID: "35", Name: "notify", Status: forge.Success, NoLog: "A trigger job has no log, the jobs of its downstream pipeline do."},
		{ID: "36", Name: "ci/external", Status: forge.Failed, NoLog: "An external commit status has no log on GitLab.", NoRetry: "GitLab cannot run an external commit status again."},
	}}
}

func retry(t *testing.T, webURL, tok string, job *forge.Job) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b := forge.Binding{Host: host(webURL), Path: "group/sub/app"}
	var err error
	if job != nil {
		err = gitlab.Provider{}.RetryJob(ctx, forge.NewClient(), b, tok, *job)
	} else {
		err = gitlab.Provider{}.RetryPipeline(ctx, forge.NewClient(), b, tok, head, failedPipeline())
	}
	if err != nil && strings.Contains(err.Error(), tok) {
		t.Fatalf("the error names the token: %v", err)
	}
	return err
}

// GitLab's retry of a pipeline runs its failed and canceled jobs again but
// no trigger job, so each trigger job that failed is retried on its own
// after it. A job is retried by its id below the project.
func TestARetryRunsThePipelineAndItsFailedTriggerJobsAgain(t *testing.T) {
	f := &fakeWrites{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	if err := retry(t, srv.URL, token, nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"POST " + projectAt + "/pipelines/12/retry", "POST " + projectAt + "/jobs/34/retry"}; !slices.Equal(f.asked(), want) {
		t.Fatalf("asked %q", f.asked())
	}
	if err := retry(t, srv.URL, token, &forge.Job{ID: "32", Name: "unit", Status: forge.Failed}); err != nil {
		t.Fatal(err)
	}
	if asked := f.asked(); asked[len(asked)-1] != "POST "+projectAt+"/jobs/32/retry" {
		t.Fatalf("asked %q", asked)
	}
}

// What GitLab refuses is said: a token without the scope api, an account
// whose role may not, a job that cannot run again, a trigger job an older
// GitLab retries only on its page, a retry already running, a wrong token,
// and a trigger job that fails after its pipeline went through.
func TestARetryGitLabRefusesIsSaid(t *testing.T) {
	address := "/group/sub/app"
	for _, c := range []struct {
		name     string
		refusals map[string]refusal
		tok      string
		job      *forge.Job
		says     string
	}{
		{"scope", map[string]refusal{projectAt + "/pipelines/12/retry": {403, scopeAnswer}}, token, nil, "The token may not write to GitLab: give it the scope api."},
		{"role", map[string]refusal{projectAt + "/pipelines/12/retry": {403, `{"message":"403 Forbidden"}`}}, token, nil, "The token's account may not retry pipeline #12 in %s, it needs the Developer role there."},
		{"gone", map[string]refusal{projectAt + "/pipelines/12/retry": {404, `{"message":"404 Not found"}`}}, token, nil, "GitLab knows no pipeline #12 in %s, or the token may not read it."},
		{"running", map[string]refusal{projectAt + "/pipelines/12/retry": {409, `{"message":"Pipeline is already being retried"}`}}, token, nil, "GitLab refused to retry pipeline #12: Pipeline is already being retried."},
		{"trigger job", map[string]refusal{projectAt + "/jobs/34/retry": {404, `{"message":"404 Not found"}`}}, token, nil, `Pipeline #12 runs its failed jobs again, the trigger job "child" does not. This GitLab retries no trigger job over its API, retry "child" on its page.`},
		{"not retryable", map[string]refusal{projectAt + "/jobs/32/retry": {403, `{"message":"403 Forbidden - Job is not retryable"}`}}, token, &forge.Job{ID: "32", Name: "unit\ntest"}, `GitLab cannot retry "unit\ntest": it ran again already, is archived, or its environment refused it.`},
		{"no job", map[string]refusal{projectAt + "/jobs/32/retry": {404, `{"message":"404 Not found"}`}}, token, &forge.Job{ID: "32", Name: "unit"}, `GitLab knows no job "unit" in %s, or the token may not read it.`},
		{"bad request", map[string]refusal{projectAt + "/jobs/32/retry": {400, `{"message":"Inputs are not valid."}`}}, token, &forge.Job{ID: "32", Name: "unit"}, `GitLab refused to retry "unit": Inputs are not valid.`},
		{"bad token", nil, "glpat-wrong-wrong-wrong", nil, "GitLab refused the token: it is wrong, expired or revoked."},
	} {
		srv := httptest.NewServer(&fakeWrites{refusals: c.refusals})
		says := c.says
		if strings.Contains(says, "%s") {
			says = fmt.Sprintf(says, strings.TrimPrefix(srv.URL, "http://")+address)
		}
		if err := retry(t, srv.URL, c.tok, c.job); err == nil || err.Error() != says {
			t.Errorf("%s: %v, want %q", c.name, err, says)
		}
		srv.Close()
	}
}

// GitLab runs failed jobs again, and its kind says so in its users' verb.
func TestGitLabRetriesInItsOwnWord(t *testing.T) {
	var p forge.Provider = gitlab.Provider{}
	if _, ok := p.(forge.Retrier); !ok || p.Kind().Retry != "Retry" {
		t.Fatalf("the kind reads %+v", p.Kind())
	}
}
