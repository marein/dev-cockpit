package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

const (
	head   = "6dcb09b5b57875f334f61aebed695e2e4193db5e"
	older  = "553c2077f0edc3d5dc5d17262f6aa498e69d6f8e"
	repoAt = "/api/v3/repos/octo/app"
)

// The answers of GitHub's REST API as it sends them, trimmed to a few of
// their fields: a pull request of the list, a check run of a commit's check
// runs, a status of a commit's combined status.
func pullAnswer(number int, state string, merged bool, headRepo, sha string, draft bool) string {
	repo := "null"
	if headRepo != "" {
		owner, name, _ := strings.Cut(headRepo, "/")
		repo = fmt.Sprintf(`{"id":2,"name":%q,"full_name":%q,"owner":{"login":%q},"private":false,"fork":%t}`, name, headRepo, owner, headRepo != "octo/app")
	}
	mergedAt := "null"
	if merged {
		mergedAt = `"2026-10-27T12:00:00Z"`
	}
	return fmt.Sprintf(`{"url":"https://github.example.com/api/v3/repos/octo/app/pulls/%d","id":%d,"html_url":"https://github.example.com/octo/app/pull/%d","number":%d,"state":%q,"locked":false,"title":"Fix the login %d","user":{"login":"octo"},"body":null,"created_at":"2026-10-27T10:00:00Z","closed_at":null,"merged_at":%s,"draft":%t,"head":{"label":"octo:feature/login","ref":"feature/login","sha":%q,"user":{"login":"octo"},"repo":%s},"base":{"label":"octo:main","ref":"main","sha":%q,"repo":{"full_name":"octo/app"}}}`,
		number, number+1000, number, number, state, number, mergedAt, draft, sha, repo, older)
}

func checkRunAnswer(id int, name, status, conclusion string) string {
	c := "null"
	if conclusion != "" {
		c = strconv.Quote(conclusion)
	}
	return fmt.Sprintf(`{"id":%d,"head_sha":%q,"node_id":"MDg6Q2hlY2tSdW40","external_id":"","url":"https://github.example.com/api/v3/repos/octo/app/check-runs/%d","html_url":"https://github.example.com/octo/app/runs/%d","details_url":"https://ci.example.com/%d","status":%q,"conclusion":%s,"started_at":"2026-10-27T10:00:00Z","completed_at":null,"output":{"title":null,"summary":null,"text":null,"annotations_count":0},"name":%q,"check_suite":{"id":5},"app":{"id":15368,"slug":"github-actions","name":"GitHub Actions"},"pull_requests":[]}`,
		id, head, id, id, id, status, c, name)
}

// appCheckRunAnswer is a check run another app than GitHub Actions posted,
// none when slug is "".
func appCheckRunAnswer(id int, name, slug, appName string) string {
	app := "null"
	if slug != "" {
		app = fmt.Sprintf(`{"id":7,"slug":%q,"name":%q}`, slug, appName)
	}
	return strings.Replace(checkRunAnswer(id, name, "completed", "failure"), `{"id":15368,"slug":"github-actions","name":"GitHub Actions"}`, app, 1)
}

func statusAnswer(id int, context, state string) string {
	return fmt.Sprintf(`{"url":"https://github.example.com/api/v3/repos/octo/app/statuses/%s","avatar_url":"https://github.example.com/images/ci.png","id":%d,"node_id":"MDY6U3RhdHVzMQ==","state":%q,"description":"Build has completed","target_url":"https://ci.example.com/%d/output","context":%q,"created_at":"2026-10-27T10:00:00Z","updated_at":"2026-10-27T10:00:00Z"}`,
		head, id, state, id, context)
}

func items(answers ...string) string {
	return strings.Join(answers, ",")
}

// fakeRepository answers the repository octo/app of an Enterprise Server for
// the token alone: its pull requests, and the check runs and the combined
// status of a commit, both in pages of what the request asks for. It keeps the
// escaped path and query of every request.
type fakeRepository struct {
	pulls    string
	runs     []string
	statuses []string
	total    int
	// noPulls answers the list of pull requests with 404, the way GitHub
	// answers a repository whose pull requests are switched off, and repo
	// is the answer to the repository itself.
	noPulls bool
	repo    string

	mu   sync.Mutex
	asks []string
}

func (f *fakeRepository) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.asks = append(f.asks, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials","documentation_url":"https://docs.github.com/rest","status":"401"}`)
		return
	}
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	pageNumber, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page := func(all []string) (string, int) {
		total := len(all)
		if f.total > 0 && len(all) > 0 {
			total = f.total
			for len(all) < total {
				all = append(all, all[len(all)-1])
			}
		}
		from := min((pageNumber-1)*perPage, len(all))
		return items(all[from:min(from+perPage, len(all))]...), total
	}
	path := r.URL.EscapedPath()
	commit, _ := strings.CutPrefix(path, repoAt+"/commits/")
	switch {
	case path == repoAt && f.repo != "":
		fmt.Fprint(w, f.repo)
	case path == repoAt+"/pulls" && !f.noPulls:
		fmt.Fprint(w, "["+f.pulls+"]")
	case strings.HasSuffix(commit, "/check-runs"):
		runs, total := page(f.runs)
		fmt.Fprintf(w, `{"total_count":%d,"check_runs":[%s]}`, total, runs)
	case strings.HasSuffix(commit, "/status"):
		statuses, total := page(f.statuses)
		fmt.Fprintf(w, `{"state":"pending","statuses":[%s],"sha":%q,"total_count":%d,"repository":{"full_name":"octo/app"},"commit_url":"https://github.example.com/api/v3/repos/octo/app/commits/%s"}`, statuses, head, total, head)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found","documentation_url":"https://docs.github.com/rest","status":"404"}`)
	}
}

func (f *fakeRepository) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asks...)
}

func branchStatus(t *testing.T, webURL, path, tok, at string) (forge.BranchStatus, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, err := Provider{}.BranchStatus(ctx, forge.NewClient(), forge.Binding{Host: host(webURL), Path: path}, tok, "feature/login", at)
	if err != nil && strings.Contains(err.Error(), tok) {
		t.Fatalf("the error names the token: %v", err)
	}
	return status, err
}

// The pull request is asked as owner:branch and held against the head's
// repository: a fork of the same owner and one whose fork is gone stay out.
// The check runs come over every page of 100, the combined status's newest
// status of each context beside them, one pipeline of the two.
func TestThePullRequestOfTheOwnBranchAndTheChecksCombined(t *testing.T) {
	runs := make([]string, 0, 150)
	for i := 1; i <= 149; i++ {
		runs = append(runs, checkRunAnswer(i, fmt.Sprintf("check %d", i), "completed", "success"))
	}
	runs = append(runs, checkRunAnswer(150, "e2e", "in_progress", ""))
	f := &fakeRepository{
		pulls: items(
			pullAnswer(9, "open", false, "octo/app-fork", head, false),
			pullAnswer(8, "open", false, "", head, false),
			pullAnswer(7, "open", false, "octo/app", head, true),
		),
		runs:     runs,
		statuses: []string{statusAnswer(1, "ci/jenkins", "failure"), statusAnswer(2, "ci/coverage", "success")},
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	status, err := branchStatus(t, srv.URL, "octo/app", token, head)
	if err != nil {
		t.Fatal(err)
	}
	want := forge.Change{Number: 7, Title: "Fix the login 7", Draft: true, State: forge.Open, URL: "https://github.example.com/octo/app/pull/7", Target: "main", Head: head}
	if status.Change == nil || *status.Change != want {
		t.Fatalf("the change is %+v", status.Change)
	}
	p := status.Pipeline
	if p == nil || p.ID != "" || p.Status != forge.Running || p.URL != srv.URL+"/octo/app/commit/"+head || p.Partial || len(p.Jobs) != 152 {
		t.Fatalf("the pipeline is %+v", p)
	}
	if j := p.Jobs[149]; j != (forge.Job{ID: "150", Name: "e2e", Status: forge.Running, URL: "https://github.example.com/octo/app/runs/150"}) {
		t.Fatalf("the last check run is %+v", j)
	}
	if j := p.Jobs[150]; j != (forge.Job{ID: "1", Name: "ci/jenkins", Status: forge.Failed, URL: "https://ci.example.com/1/output", NoLog: "A commit status has no log on GitHub.", NoRetry: "GitHub cannot run a commit status again."}) {
		t.Fatalf("the first status is %+v", j)
	}
	asked := f.asked()
	for i, want := range []string{
		repoAt + "/pulls?direction=desc&head=octo%3Afeature%2Flogin&per_page=20&sort=created&state=all",
		repoAt + "/commits/" + head + "/check-runs?per_page=100&page=1",
		repoAt + "/commits/" + head + "/check-runs?per_page=100&page=2",
		repoAt + "/commits/" + head + "/status?per_page=100&page=1",
	} {
		if i >= len(asked) || asked[i] != want {
			t.Fatalf("request %d is not %s: %v", i, want, asked)
		}
	}
	if len(asked) != 4 {
		t.Fatalf("it asked %v", asked)
	}
}

// Once the running check is through, the failed status fails the pipeline.
// Without an open one, a merged or closed pull request counts at the pushed
// head alone, and a branch never pushed has no checks to ask for.
func TestAPullRequestThatIsOverCountsAtTheHeadAlone(t *testing.T) {
	f := &fakeRepository{
		pulls:    items(pullAnswer(6, "closed", true, "octo/app", older, false), pullAnswer(5, "closed", false, "octo/app", head, false)),
		runs:     []string{checkRunAnswer(1, "build", "completed", "success")},
		statuses: []string{statusAnswer(1, "ci/jenkins", "error")},
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	status, err := branchStatus(t, srv.URL, "octo/app", token, head)
	if err != nil || status.Change == nil || status.Change.Number != 5 || status.Change.State != forge.Closed || status.Pipeline == nil || status.Pipeline.Status != forge.Failed {
		t.Fatalf("got %+v %+v, %v", status.Change, status.Pipeline, err)
	}
	asks := len(f.asked())
	status, err = branchStatus(t, srv.URL, "octo/app", token, "")
	if err != nil || status.Change != nil || status.Pipeline != nil {
		t.Fatalf("without a pushed head: %+v, %v", status, err)
	}
	if got := f.asked()[asks:]; len(got) != 1 || !strings.Contains(got[0], "/pulls?") {
		t.Fatalf("without a pushed head it asked %v", got)
	}
	f.pulls = items(pullAnswer(6, "closed", true, "octo/app", older, false))
	if status, err = branchStatus(t, srv.URL, "octo/app", token, older); err != nil || status.Change == nil || status.Change.State != forge.Merged {
		t.Fatalf("at the older head: %+v, %v", status.Change, err)
	}
}

// A check run of GitHub Actions is a job with a log that runs again, one of
// another app or of none and a commit status are not and say why.
func TestOnlyAnActionsJobHasALogAndRunsAgain(t *testing.T) {
	f := &fakeRepository{
		runs: []string{
			checkRunAnswer(1, "build", "completed", "failure"),
			appCheckRunAnswer(2, "scan", "codeql", "Code \"scanning\""),
			appCheckRunAnswer(3, "orphan", "", ""),
		},
		statuses: []string{statusAnswer(4, "ci/jenkins", "failure")},
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	status, err := branchStatus(t, srv.URL, "octo/app", token, head)
	if err != nil || status.Pipeline == nil || len(status.Pipeline.Jobs) != 4 {
		t.Fatalf("got %+v, %v", status.Pipeline, err)
	}
	for i, want := range [][2]string{
		{"", ""},
		{`The check run comes from "Code \"scanning\"", GitHub keeps no log of it.`, `The check run comes from "Code \"scanning\"", only that app runs it again.`},
		{"The check run is no GitHub Actions job, GitHub keeps no log of it.", "The check run is no GitHub Actions job, GitHub cannot re-run it here."},
		{"A commit status has no log on GitHub.", "GitHub cannot run a commit status again."},
	} {
		if j := status.Pipeline.Jobs[i]; j.NoLog != want[0] || j.NoRetry != want[1] {
			t.Errorf("job %d says %q and %q, want %q", i, j.NoLog, j.NoRetry, want)
		}
	}
}

// A commit without check runs and statuses has no pipeline, a branch without
// a pull request no change, and both are an answer.
func TestNothingThereIsAnAnswer(t *testing.T) {
	srv := httptest.NewServer(&fakeRepository{})
	defer srv.Close()
	status, err := branchStatus(t, srv.URL, "octo/app", token, head)
	if err != nil || status.Change != nil || status.Pipeline != nil {
		t.Fatalf("got %+v, %v", status, err)
	}
}

// Every status and conclusion of a check run and every state of a commit
// status lands on a neutral status.
func TestTheChecksAreNeutral(t *testing.T) {
	for _, c := range []struct {
		status, conclusion string
		want               forge.Status
	}{
		{"queued", "", forge.Pending}, {"requested", "", forge.Pending}, {"waiting", "", forge.Pending}, {"pending", "", forge.Pending},
		{"in_progress", "", forge.Running}, {"completed", "success", forge.Success}, {"completed", "failure", forge.Failed},
		{"completed", "timed_out", forge.Failed}, {"completed", "startup_failure", forge.Failed}, {"completed", "cancelled", forge.Canceled},
		{"completed", "stale", forge.Canceled}, {"completed", "action_required", forge.Manual}, {"completed", "neutral", forge.Skipped},
		{"completed", "skipped", forge.Skipped},
	} {
		srv := httptest.NewServer(&fakeRepository{runs: []string{checkRunAnswer(1, "build", c.status, c.conclusion)}})
		status, err := branchStatus(t, srv.URL, "octo/app", token, head)
		srv.Close()
		if err != nil || status.Pipeline == nil || status.Pipeline.Jobs[0].Status != c.want {
			t.Errorf("%s %s: %+v, %v, want %s", c.status, c.conclusion, status.Pipeline, err, c.want)
		}
	}
	for state, want := range map[string]forge.Status{"pending": forge.Pending, "success": forge.Success, "failure": forge.Failed, "error": forge.Failed} {
		srv := httptest.NewServer(&fakeRepository{statuses: []string{statusAnswer(1, "ci", state)}})
		status, err := branchStatus(t, srv.URL, "octo/app", token, head)
		srv.Close()
		if err != nil || status.Pipeline == nil || status.Pipeline.Status != want {
			t.Errorf("%s: %+v, %v, want %s", state, status.Pipeline, err, want)
		}
	}
}

// A commit with more check runs than the pages read is marked partial
// instead of read without end.
func TestTheChecksStopAtTheirBound(t *testing.T) {
	f := &fakeRepository{runs: []string{checkRunAnswer(1, "build", "completed", "success")}, total: 1500}
	srv := httptest.NewServer(f)
	defer srv.Close()
	status, err := branchStatus(t, srv.URL, "octo/app", token, head)
	if err != nil || status.Pipeline == nil || !status.Pipeline.Partial || len(status.Pipeline.Jobs) != 1000 {
		t.Fatalf("got %+v, %v", status.Pipeline != nil && status.Pipeline.Partial, err)
	}
	if asks := len(f.asked()); asks != 12 {
		t.Fatalf("it asked %d times", asks)
	}
}

// A repository whose pull requests are switched off answers 404 to their
// list: the branch has none and its checks read all the same. A server that
// names no such switch is not taken for one.
func TestPullRequestsSwitchedOffLeaveTheChecks(t *testing.T) {
	f := &fakeRepository{noPulls: true, repo: `{"id":2,"full_name":"octo/app","default_branch":"main","has_pull_requests":false}`, runs: []string{checkRunAnswer(1, "build", "completed", "success")}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	status, err := branchStatus(t, srv.URL, "octo/app", token, head)
	if err != nil || status.Change != nil || status.Pipeline == nil || status.Pipeline.Status != forge.Success || len(status.Pipeline.Jobs) != 1 {
		t.Fatalf("got %+v, %v", status, err)
	}
	f.repo = `{"id":2,"full_name":"octo/app","default_branch":"main"}`
	says := "GitHub knows no repository " + strings.TrimPrefix(srv.URL, "http://") + "/octo/app, or the token may not read it."
	if _, err := branchStatus(t, srv.URL, "octo/app", token, head); err == nil || err.Error() != says {
		t.Fatalf("a server without the switch answered %v", err)
	}
}

func TestReadFailuresAreSaid(t *testing.T) {
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Resource not accessible by personal access token","documentation_url":"https://docs.github.com/rest","status":"403"}`)
	}))
	defer forbidden.Close()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<!DOCTYPE html><title>Sign in</title>")
	}))
	defer page.Close()
	srv := httptest.NewServer(&fakeRepository{})
	defer srv.Close()
	address := func(webURL, path string) string { return strings.TrimPrefix(webURL, "http://") + "/" + path }
	for _, c := range []struct {
		name, webURL, path, tok, says string
	}{
		{"bad token", srv.URL, "octo/app", "github_pat_wrong", "GitHub refused the token: it is wrong, expired or revoked."},
		{"no repository", srv.URL, "octo/gone", token, "GitHub knows no repository " + address(srv.URL, "octo/gone") + ", or the token may not read it."},
		{"forbidden", forbidden.URL, "octo/app", token, "The token may not read the pull requests, check runs or commit statuses of " + address(forbidden.URL, "octo/app") + "."},
		{"no GitHub", page.URL, "octo/app", token, "No GitHub API answers at " + page.URL + "/api/v3: check the kind and the web address."},
		{"a GitLab path", srv.URL, "group/sub/app", token, "A GitHub repository path is owner/name, " + address(srv.URL, "group/sub/app") + " is none."},
	} {
		if _, err := branchStatus(t, c.webURL, c.path, c.tok, head); err == nil || err.Error() != c.says {
			t.Errorf("%s: %v, want %q", c.name, err, c.says)
		}
	}
}
