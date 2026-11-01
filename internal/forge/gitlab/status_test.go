package gitlab_test

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

const (
	head      = "f471a571e52a7d8211d5cd279aa17f890d2336d9"
	older     = "0208075b065b959528974b98fc3ecdf15fa44f3d"
	projectAt = "/api/v4/projects/group%2Fsub%2Fapp"
)

// The answers of a GitLab 19.5 as its API sends them, trimmed to a few of
// their fields: a merge request of the list, a pipeline of the list, a job
// of a pipeline's jobs.
func mergeRequestAnswer(iid int, state, sha string, source, draft bool) string {
	sourceProject := 2
	if !source {
		sourceProject = 7
	}
	return fmt.Sprintf(`{"id":%d,"iid":%d,"project_id":2,"title":"Fix the login %d","description":null,"state":%q,"created_at":"2026-10-28T08:40:07.438Z","merged_at":null,"target_branch":"main","source_branch":"feature/login","source_project_id":%d,"target_project_id":2,"draft":%t,"work_in_progress":%t,"merge_status":"can_be_merged","detailed_merge_status":"draft_status","sha":%q,"reference":"!%d","references":{"short":"!%d","full":"group/sub/app!%d"},"web_url":"https://gitlab.example.com/group/sub/app/-/merge_requests/%d"}`,
		iid+100, iid, iid, state, sourceProject, draft, draft, sha, iid, iid, iid, iid)
}

func pipelineAnswer(id int, ref, status string) string {
	return fmt.Sprintf(`{"id":%d,"iid":%d,"project_id":2,"sha":%q,"ref":%q,"status":%q,"source":"push","created_at":"2026-10-28T08:40:07.620Z","web_url":"https://gitlab.example.com/group/sub/app/-/pipelines/%d","name":null}`, id, id, head, ref, status, id)
}

func jobAnswer(id int, name, stage, status string, allowFailure bool) string {
	return fmt.Sprintf(`{"id":%d,"status":%q,"stage":%q,"name":%q,"ref":"feature/login","tag":false,"coverage":null,"allow_failure":%t,"created_at":"2026-10-28T08:40:07.694Z","user":{"id":1,"username":"root"},"commit":{"id":%q,"short_id":"f471a571"},"pipeline":{"id":12,"sha":%q},"web_url":"https://gitlab.example.com/group/sub/app/-/jobs/%d"}`,
		id, status, stage, name, allowFailure, head, head, id)
}

// bridgeAnswer is a trigger job of a pipeline's bridges, its downstream
// pipeline one GitLab could not create.
func bridgeAnswer(id int, name, stage, status string) string {
	return fmt.Sprintf(`{"id":%d,"status":%q,"stage":%q,"name":%q,"ref":"feature/login","allow_failure":false,"failure_reason":"downstream_pipeline_creation_failed","pipeline":{"id":12},"web_url":"https://gitlab.example.com/group/sub/app/-/jobs/%d","downstream_pipeline":{"id":%d,"sha":%q,"ref":"feature/login","status":"failed","web_url":"https://gitlab.example.com/group/sub/app/-/pipelines/%d"}}`,
		id, status, stage, name, id, id+1000, head, id+1000)
}

// commitStatusAnswer is a status of a commit's statuses, which hold the jobs
// and trigger jobs of its pipelines next to the external ones. A pipelineID
// of 0 leaves the pipeline out, as GitLab before 16.3 does.
func commitStatusAnswer(id int, name, status, targetURL string, pipelineID int) string {
	target := "null"
	if targetURL != "" {
		target = fmt.Sprintf("%q", targetURL)
	}
	pipeline := ""
	if pipelineID != 0 {
		pipeline = fmt.Sprintf(`"pipeline_id":%d,`, pipelineID)
	}
	return fmt.Sprintf(`{"id":%d,"sha":%q,"ref":"feature/login","status":%q,"name":%q,"target_url":%s,"description":null,"allow_failure":false,"coverage":null,%s"author":{"id":1,"username":"root"}}`,
		id, head, status, name, target, pipeline)
}

func list(items ...string) string {
	return "[" + strings.Join(items, ",") + "]"
}

// fakeProject answers the project group/sub/app below /api/v4 for the token
// alone: its merge requests, the pipelines of head, the jobs of pipeline 12
// in pages, its trigger jobs, the statuses of head and the traces of jobs.
// It keeps the escaped path and query of every request.
type fakeProject struct {
	mergeRequests string
	pipelines     string
	jobPages      []string
	moreJobs      bool
	bridges       string
	statuses      string
	traces        map[string]string

	mu   sync.Mutex
	asks []string
}

func (f *fakeProject) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.asks = append(f.asks, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
		return
	}
	switch r.URL.EscapedPath() {
	case projectAt + "/merge_requests":
		fmt.Fprint(w, f.mergeRequests)
	case projectAt + "/pipelines":
		fmt.Fprint(w, f.pipelines)
	case projectAt + "/pipelines/12/jobs":
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		if page < len(f.jobPages) || f.moreJobs {
			w.Header().Set("X-Next-Page", fmt.Sprint(page+1))
		}
		if page <= len(f.jobPages) {
			fmt.Fprint(w, f.jobPages[page-1])
			return
		}
		fmt.Fprint(w, list(jobAnswer(900+page, "more", "test", "success", false)))
	case projectAt + "/pipelines/12/bridges":
		fmt.Fprint(w, cmp.Or(f.bridges, list()))
	case projectAt + "/repository/commits/" + head + "/statuses":
		fmt.Fprint(w, cmp.Or(f.statuses, list()))
	default:
		job, ok := strings.CutPrefix(r.URL.EscapedPath(), projectAt+"/jobs/")
		if trace, found := f.traces[strings.TrimSuffix(job, "/trace")]; ok && found && strings.HasSuffix(job, "/trace") {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, trace)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 Project Not Found"}`)
	}
}

func (f *fakeProject) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asks...)
}

func branchStatus(t *testing.T, webURL, tok, branch, at string) (forge.BranchStatus, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b := forge.Binding{Host: host(webURL), Path: "group/sub/app"}
	status, err := gitlab.Provider{}.BranchStatus(ctx, forge.NewClient(), b, tok, branch, at)
	if err != nil && strings.Contains(err.Error(), tok) {
		t.Fatalf("the error names the token: %v", err)
	}
	return status, err
}

// The open merge request of the project's own branch wins over a fork's of
// the same branch name, and the pipeline is the newest of head that ran for
// the branch or for that merge request, not one of another branch at the
// same commit. Its jobs come over every page in the order they were made,
// in the neutral words, with its trigger jobs and the external statuses
// posted to it, neither with a log, and the project path travels as one
// encoded segment.
func TestTheStatusReadsTheOwnMergeRequestAndThePipelineOfTheHead(t *testing.T) {
	f := &fakeProject{
		mergeRequests: list(
			mergeRequestAnswer(9, "opened", head, false, false),
			mergeRequestAnswer(4, "opened", head, true, true),
			mergeRequestAnswer(2, "merged", older, true, false),
		),
		pipelines: list(
			pipelineAnswer(13, "main", "success"),
			pipelineAnswer(12, "refs/merge-requests/4/head", "failed"),
			pipelineAnswer(11, "feature/login", "success"),
		),
		jobPages: []string{
			list(jobAnswer(33, "lint", "test", "failed", true), jobAnswer(32, "unit", "test", "failed", false)),
			list(jobAnswer(31, "build", "build", "success", false)),
		},
		bridges: list(bridgeAnswer(34, "child", "deploy", "failed")),
		statuses: list(
			commitStatusAnswer(31, "build", "success", "", 12),
			commitStatusAnswer(34, "child", "failed", "", 12),
			commitStatusAnswer(36, "ci/external", "failed", "https://ci.example.com/36", 12),
			commitStatusAnswer(35, "ci/older", "success", "https://ci.example.com/35", 11),
		),
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	status, err := branchStatus(t, srv.URL, token, "feature/login", head)
	if err != nil {
		t.Fatal(err)
	}
	want := forge.Change{Number: 4, Title: "Fix the login 4", Draft: true, State: forge.Open, URL: "https://gitlab.example.com/group/sub/app/-/merge_requests/4", Target: "main", Head: head}
	if status.Change == nil || *status.Change != want {
		t.Fatalf("the change is %+v", status.Change)
	}
	p := status.Pipeline
	if p == nil || p.ID != "12" || p.Status != forge.Failed || p.URL != "https://gitlab.example.com/group/sub/app/-/pipelines/12" || p.Partial {
		t.Fatalf("the pipeline is %+v", p)
	}
	jobs := []forge.Job{
		{ID: "31", Name: "build", Stage: "build", Status: forge.Success, URL: "https://gitlab.example.com/group/sub/app/-/jobs/31"},
		{ID: "32", Name: "unit", Stage: "test", Status: forge.Failed, URL: "https://gitlab.example.com/group/sub/app/-/jobs/32"},
		{ID: "33", Name: "lint", Stage: "test", Status: forge.Failed, URL: "https://gitlab.example.com/group/sub/app/-/jobs/33", AllowFailure: true},
		{ID: "34", Name: "child", Stage: "deploy", Status: forge.Failed, URL: "https://gitlab.example.com/group/sub/app/-/pipelines/1034", NoLog: "A trigger job has no log, the jobs of its downstream pipeline do."},
		{ID: "36", Name: "ci/external", Status: forge.Failed, URL: "https://ci.example.com/36", NoLog: "An external commit status has no log on GitLab.", NoRetry: "GitLab cannot run an external commit status again."},
	}
	if !reflect.DeepEqual(p.Jobs, jobs) {
		t.Fatalf("the jobs are %+v", p.Jobs)
	}
	asked := f.asked()
	for i, want := range []string{
		projectAt + "/merge_requests?order_by=created_at&per_page=20&sort=desc&source_branch=feature%2Flogin",
		projectAt + "/pipelines?order_by=id&per_page=20&sha=" + head + "&sort=desc",
		projectAt + "/pipelines/12/jobs?page=1&per_page=100",
		projectAt + "/pipelines/12/jobs?page=2&per_page=100",
		projectAt + "/pipelines/12/bridges?page=1&per_page=100",
		projectAt + "/repository/commits/" + head + "/statuses?page=1&per_page=100&pipeline_id=12&ref=refs%2Fmerge-requests%2F4%2Fhead",
	} {
		if i >= len(asked) || asked[i] != want {
			t.Fatalf("request %d is not %s: %v", i, want, asked)
		}
	}
}

// A GitLab before 16.3 names no pipeline of a status and filters them by
// commit and ref alone, so the jobs of an older pipeline of the same commit
// and ref and its external statuses stay out of the newest one's jobs.
func TestStatusesThatNameNoPipelineStayOut(t *testing.T) {
	f := &fakeProject{
		mergeRequests: list(),
		pipelines:     list(pipelineAnswer(12, "feature/login", "failed"), pipelineAnswer(11, "feature/login", "failed")),
		jobPages:      []string{list(jobAnswer(32, "unit", "test", "failed", false), jobAnswer(31, "build", "build", "success", false))},
		statuses: list(
			commitStatusAnswer(32, "unit", "failed", "", 0),
			commitStatusAnswer(31, "build", "success", "", 0),
			commitStatusAnswer(23, "ci/older", "failed", "https://ci.example.com/23", 0),
			commitStatusAnswer(22, "unit", "failed", "", 0),
			commitStatusAnswer(21, "build", "failed", "", 0),
		),
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	status, err := branchStatus(t, srv.URL, token, "feature/login", head)
	if err != nil || status.Pipeline == nil || status.Pipeline.ID != "12" || status.Pipeline.Partial {
		t.Fatalf("got %+v, %v", status.Pipeline, err)
	}
	jobs := []forge.Job{
		{ID: "31", Name: "build", Stage: "build", Status: forge.Success, URL: "https://gitlab.example.com/group/sub/app/-/jobs/31"},
		{ID: "32", Name: "unit", Stage: "test", Status: forge.Failed, URL: "https://gitlab.example.com/group/sub/app/-/jobs/32"},
	}
	if !reflect.DeepEqual(status.Pipeline.Jobs, jobs) {
		t.Fatalf("the jobs are %+v", status.Pipeline.Jobs)
	}
}

// Without an open one, a merged or closed merge request counts only at the
// pushed head, so an old one of a branch name used again stays out. A
// branch never pushed has no pipeline to ask for.
func TestAMergeRequestThatIsOverCountsAtTheHeadAlone(t *testing.T) {
	f := &fakeProject{
		mergeRequests: list(mergeRequestAnswer(3, "merged", older, true, false), mergeRequestAnswer(2, "closed", head, true, false)),
		pipelines:     list(),
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	status, err := branchStatus(t, srv.URL, token, "feature/login", head)
	if err != nil || status.Change == nil || status.Change.Number != 2 || status.Change.State != forge.Closed || status.Pipeline != nil {
		t.Fatalf("got %+v, %v", status, err)
	}
	status, err = branchStatus(t, srv.URL, token, "feature/login", older)
	if err != nil || status.Change == nil || status.Change.Number != 3 || status.Change.State != forge.Merged {
		t.Fatalf("at the older head: %+v, %v", status, err)
	}
	asks := len(f.asked())
	status, err = branchStatus(t, srv.URL, token, "feature/login", "")
	if err != nil || status.Change != nil || status.Pipeline != nil {
		t.Fatalf("without a pushed head: %+v, %v", status, err)
	}
	if got := f.asked()[asks:]; len(got) != 1 || !strings.Contains(got[0], "/merge_requests?") {
		t.Fatalf("without a pushed head it asked %v", got)
	}
}

// No merge request and no pipeline of the branch are an answer, not a
// failure, also when head ran for another branch only.
func TestNothingThereIsAnAnswer(t *testing.T) {
	f := &fakeProject{mergeRequests: list(mergeRequestAnswer(9, "opened", head, false, false)), pipelines: list(pipelineAnswer(13, "main", "success"))}
	srv := httptest.NewServer(f)
	defer srv.Close()
	status, err := branchStatus(t, srv.URL, token, "feature/login", head)
	if err != nil || status.Change != nil || status.Pipeline != nil {
		t.Fatalf("got %+v, %v", status, err)
	}
}

// Every status GitLab gives a pipeline or a job lands on a neutral one, and
// one it adds later reads as not finished.
func TestTheStatusesAreNeutral(t *testing.T) {
	statuses := map[string]forge.Status{
		"created": forge.Pending, "waiting_for_resource": forge.Pending, "preparing": forge.Pending, "pending": forge.Pending,
		"scheduled": forge.Pending, "running": forge.Running, "canceling": forge.Running, "success": forge.Success,
		"failed": forge.Failed, "canceled": forge.Canceled, "skipped": forge.Skipped, "manual": forge.Manual, "frobbing": forge.Pending,
	}
	for status, want := range statuses {
		f := &fakeProject{
			mergeRequests: list(),
			pipelines:     list(pipelineAnswer(12, "feature/login", status)),
			jobPages:      []string{list(jobAnswer(1, "job", "test", status, false))},
		}
		srv := httptest.NewServer(f)
		got, err := branchStatus(t, srv.URL, token, "feature/login", head)
		srv.Close()
		if err != nil || got.Pipeline == nil || got.Pipeline.Status != want || got.Pipeline.Jobs[0].Status != want {
			t.Errorf("%s: %+v, %v, want %s", status, got.Pipeline, err, want)
		}
	}
}

// A pipeline of more jobs than the pages read is marked partial instead of
// read without end, and its statuses stay unread, where a job beyond the
// pages would pass for an external one.
func TestTheJobsStopAtTheirBound(t *testing.T) {
	f := &fakeProject{mergeRequests: list(), pipelines: list(pipelineAnswer(12, "feature/login", "running")), jobPages: []string{list(jobAnswer(1, "first", "test", "running", false))}, moreJobs: true}
	srv := httptest.NewServer(f)
	defer srv.Close()
	status, err := branchStatus(t, srv.URL, token, "feature/login", head)
	if err != nil || status.Pipeline == nil || !status.Pipeline.Partial || len(status.Pipeline.Jobs) != 10 {
		t.Fatalf("got %+v, %v", status.Pipeline, err)
	}
	if asked := f.asked(); len(asked) != 13 || strings.Contains(strings.Join(asked, " "), "/statuses") {
		t.Fatalf("it asked %d times: %v", len(asked), asked)
	}
}

func TestReadFailuresAreSaid(t *testing.T) {
	scope := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, scopeAnswer)
	}))
	defer scope.Close()
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"403 Forbidden"}`)
	}))
	defer forbidden.Close()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<!DOCTYPE html><title>Sign in</title>")
	}))
	defer page.Close()
	gone := httptest.NewServer(&fakeProject{})
	defer gone.Close()
	address := func(webURL string) string { return strings.TrimPrefix(webURL, "http://") + "/group/sub/app" }
	for _, c := range []struct {
		name, webURL, tok, says string
	}{
		{"bad token", gone.URL, "glpat-wrong-wrong-wrong", "GitLab refused the token: it is wrong, expired or revoked."},
		{"no project", gone.URL + "/elsewhere", token, "GitLab knows no project " + address(gone.URL+"/elsewhere") + ", or the token may not read it."},
		{"missing scope", scope.URL, token, "The token may not read the API: give it the scope read_api, or api to also write."},
		{"forbidden", forbidden.URL, token, "The token may not read " + address(forbidden.URL) + " on GitLab."},
		{"no GitLab", page.URL, token, "No GitLab API answers at " + page.URL + "/api/v4: check the kind and the web address."},
	} {
		if _, err := branchStatus(t, c.webURL, c.tok, "feature/login", head); err == nil || err.Error() != c.says {
			t.Errorf("%s: %v, want %q", c.name, err, c.says)
		}
	}
}
