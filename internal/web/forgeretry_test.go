package web

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
	"github.com/marein/dev-cockpit/internal/localapi"
)

// retryGitLab answers group/sub/app like GitLab: the pipeline it holds of
// head on its ref, feature unless told another, with its job unit, a job
// lint allowed to fail and an external status, failed while the pipeline
// is, and runs the jobs again on a retry. It keeps what it was asked to run
// again.
type retryGitLab struct {
	mu       sync.Mutex
	requests int
	head     string
	ref      string
	pipeline int
	status   string
	retried  []string
}

func (g *retryGitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests++
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+forgeStatusToken {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
		return
	}
	project := "/api/v4/projects/group%2Fsub%2Fapp"
	pipeline := fmt.Sprintf("%s/pipelines/%d", project, g.pipeline)
	switch path := r.URL.EscapedPath(); {
	case r.Method == http.MethodPost && (path == pipeline+"/retry" || path == project+"/jobs/31/retry"):
		g.retried = append(g.retried, strings.TrimPrefix(path, project))
		g.status = "running"
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":%d,"status":"running"}`, g.pipeline)
	case path == project+"/merge_requests", path == pipeline+"/bridges":
		fmt.Fprint(w, `[]`)
	case path == project+"/pipelines":
		fmt.Fprintf(w, `[{"id":%d,"iid":3,"project_id":2,"sha":%q,"ref":%q,"status":%q,"source":"push","web_url":"https://gitlab.example.com/group/sub/app/-/pipelines/%d"}]`, g.pipeline, g.head, cmp.Or(g.ref, "feature"), g.status, g.pipeline)
	case path == pipeline+"/jobs":
		fmt.Fprintf(w, `[{"id":31,"status":%q,"stage":"test","name":"unit","allow_failure":false,"web_url":"https://gitlab.example.com/group/sub/app/-/jobs/31"},{"id":32,"status":%q,"stage":"test","name":"lint","allow_failure":true,"web_url":"https://gitlab.example.com/group/sub/app/-/jobs/32"}]`, g.status, g.status)
	case path == project+"/repository/commits/"+g.head+"/statuses":
		fmt.Fprintf(w, `[{"id":35,"sha":%q,"ref":"feature","status":%q,"name":"ci/external","target_url":null,"allow_failure":false,"pipeline_id":%d}]`, g.head, g.status, g.pipeline)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 Not found"}`)
	}
}

func (g *retryGitLab) asked() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.retried)
}

func (g *retryGitLab) on(ref string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ref = ref
}

func (g *retryGitLab) set(pipeline int, status string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pipeline, g.status = pipeline, status
}

type forgeRetryFixture struct {
	*composeFixture
	gitlab  *retryGitLab
	repo    string
	pushed  string
	address string
}

// newForgeRetryFixture is the compose fixture with a repository app whose
// branch feature is pushed to the fake GitLab, its pipeline 12 failed.
func newForgeRetryFixture(t *testing.T) *forgeRetryFixture {
	t.Helper()
	f := &forgeRetryFixture{composeFixture: newComposeFixture(t)}
	f.repo = worktreeSourceRepo(t, filepath.Dir(f.dir), "app")
	out, err := exec.Command("git", "-C", f.repo, "rev-parse", "feature").Output()
	if err != nil {
		t.Fatal(err)
	}
	f.pushed = strings.TrimSpace(string(out))
	f.gitlab = &retryGitLab{head: f.pushed, pipeline: 12, status: "failed"}
	srv := httptest.NewServer(f.gitlab)
	t.Cleanup(srv.Close)
	worktreeGit(t, f.repo, "remote", "add", "origin", srv.URL+"/group/sub/app.git")
	worktreeGit(t, f.repo, "update-ref", "refs/remotes/origin/feature", "feature")
	worktreeGit(t, f.repo, "switch", "-q", "feature")
	service := forge.New(t.TempDir(), []forge.Provider{gitlab.Provider{}})
	f.s.SetForge(service)
	addForgeHost(t, service, forge.Input{Kind: "gitlab", WebURL: srv.URL}, forgeStatusToken)
	f.address = strings.TrimPrefix(srv.URL, "http://") + "/group/sub/app"
	f.router.POST("/projects/:name/forge/retry", f.s.handleProjectForgeRetry)
	return f
}

// retry posts the form as a click does, or over the local socket as the
// assistant id names, none for a call that names none.
func (f *forgeRetryFixture) retry(t *testing.T, local bool, id string, form url.Values) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/projects/app/forge/retry", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if local {
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		if id != "" {
			req.Header.Set(localapi.AssistantHeader, id)
		}
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec.Code, decodeJSON(t, rec)
}

// A click runs the failed jobs of the pipeline it saw again, or the one job,
// at once; one that saw a pipeline or a head that is not the one standing,
// or a pipeline that no longer failed, is refused and runs nothing.
func TestAClickRunsTheFailedJobsItSawAgain(t *testing.T) {
	f := newForgeRetryFixture(t)
	code, answer := f.retry(t, false, "", url.Values{"branch": {"feature"}, "head": {f.pushed}, "pipeline": {"11"}})
	if code != http.StatusConflict || answer["error"] != `The pipeline of "feature" is #12 now, not #11.` || len(f.gitlab.asked()) != 0 {
		t.Fatalf("a stale click answered %d %v, ran %q", code, answer, f.gitlab.asked())
	}
	code, answer = f.retry(t, false, "", url.Values{"head": {strings.Repeat("a", 40)}})
	if code != http.StatusConflict || !strings.HasPrefix(answer["error"].(string), `"feature" was pushed again`) {
		t.Fatalf("a click on another head answered %d %v", code, answer)
	}
	code, answer = f.retry(t, false, "", url.Values{"branch": {"feature"}, "head": {f.pushed}, "pipeline": {"12"}})
	if code != http.StatusOK || answer["message"] != "The failed jobs of pipeline #12 run again." || !slices.Equal(f.gitlab.asked(), []string{"/pipelines/12/retry"}) {
		t.Fatalf("the click answered %d %v, ran %q", code, answer, f.gitlab.asked())
	}
	code, answer = f.retry(t, false, "", url.Values{"pipeline": {"12"}, "job": {"31"}})
	if code != http.StatusConflict || answer["error"] != `The job "unit" is running, not failed.` {
		t.Fatalf("a click on a running job answered %d %v", code, answer)
	}
	f.gitlab.set(12, "failed")
	code, answer = f.retry(t, false, "", url.Values{"pipeline": {"12"}, "job": {"31"}})
	if code != http.StatusOK || answer["message"] != `The job "unit" runs again.` || len(f.gitlab.asked()) != 2 {
		t.Fatalf("the job's click answered %d %v, ran %q", code, answer, f.gitlab.asked())
	}
	f.gitlab.set(12, "failed")
	code, answer = f.retry(t, false, "", url.Values{"job": {"35"}})
	if code != http.StatusConflict || answer["error"] != "GitLab cannot run an external commit status again." || len(f.gitlab.asked()) != 2 {
		t.Fatalf("a click on an external status answered %d %v", code, answer)
	}
}

// What a retry names is checked before the forge is asked: its bounds, a
// branch that does not exist, a project bound to no forge, a host without a
// token, and a local call that names no assistant.
func TestARetryIsCheckedBeforeTheForgeIsAsked(t *testing.T) {
	f := newForgeRetryFixture(t)
	for _, c := range []struct {
		form url.Values
		code int
		says string
	}{
		{url.Values{"job": {strings.Repeat("9", 65)}}, http.StatusBadRequest, "A job id has at most 64 characters."},
		{url.Values{"pipeline": {strings.Repeat("9", 65)}}, http.StatusBadRequest, "A pipeline id has at most 64 characters."},
		{url.Values{"head": {strings.Repeat("a", 65)}}, http.StatusBadRequest, "A commit has at most 64 characters."},
		{url.Values{"branch": {strings.Repeat("b", 256)}}, http.StatusBadRequest, "A branch name has at most 255 characters."},
	} {
		if code, answer := f.retry(t, false, "", c.form); code != c.code || answer["error"] != c.says {
			t.Errorf("%v answered %d %v", c.form, code, answer)
		}
	}
	if code, answer := f.retry(t, true, "", url.Values{}); code != http.StatusForbidden || answer["error"] != assistantCallerRefusal {
		t.Fatalf("a local call without an assistant answered %d %v", code, answer)
	}
	worktreeGit(t, f.repo, "remote", "set-url", "origin", "https://unknown.example.com/group/app.git")
	if code, answer := f.retry(t, false, "", url.Values{}); code != http.StatusConflict || answer["error"] != `"app" is bound to no forge: its origin names no forge host and none is set under its Forge.` {
		t.Fatalf("an unbound project answered %d %v", code, answer)
	}
	if len(f.gitlab.asked()) != 0 || len(f.broker.Questions()) != 0 {
		t.Fatalf("a refused retry ran %q, asked %v", f.gitlab.asked(), f.broker.Questions())
	}
}

// An assistant's retry waits for the user, who sees the pipeline, its
// failed jobs, what cannot run again, branch, forge and project. Approved,
// the forge is read again and the jobs run, and the note says so; denied,
// nothing runs.
func TestAnAssistantsRetryWaitsForTheApproval(t *testing.T) {
	f := newForgeRetryFixture(t)
	code, answer := f.retry(t, true, f.owner, url.Values{})
	what := "Retry failed jobs of pipeline #12 on feature in app"
	if code != http.StatusOK || answer["pending"] != true || answer["what"] != what {
		t.Fatalf("the retry answered %d %v", code, answer)
	}
	q := f.waitQuestion(t)
	want := []askpass.Detail{
		{Label: "Action", Value: "Retry failed jobs"},
		{Label: "Failed jobs", Value: "unit, lint, ci/external"},
		{Label: "Left as they are", Value: `"ci/external": GitLab cannot run an external commit status again.`},
		{Label: "Pipeline", Value: "#12 · https://gitlab.example.com/group/sub/app/-/pipelines/12"},
		{Label: "Branch", Value: "feature"},
		{Label: "Forge", Value: "GitLab · " + f.address},
		{Label: "Project", Value: "app"},
	}
	if q.Action != what || !reflect.DeepEqual(q.Details, want) || len(f.gitlab.asked()) != 0 {
		t.Fatalf("the question reads %+v, ran %q", q, f.gitlab.asked())
	}
	if code, answer := f.retry(t, true, f.owner, url.Values{"branch": {"feature"}}); code != http.StatusConflict || !strings.Contains(answer["error"].(string), "already waits") {
		t.Fatalf("the same retry again answered %d %v", code, answer)
	}
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Done || !slices.Equal(f.gitlab.asked(), []string{"/pipelines/12/retry"}) {
		t.Fatalf("the approval ran %q, note %s", f.gitlab.asked(), note.Content)
	}
	for _, says := range []string{"On GitLab " + f.address + " the failed jobs of pipeline #12 of feature in app run again: https://gitlab.example.com/group/sub/app/-/pipelines/12.", `Not run again: "ci/external": GitLab cannot run an external commit status again.`} {
		if !strings.Contains(note.Content, says) {
			t.Fatalf("the note lacks %q: %s", says, note.Content)
		}
	}
	f.waitNoQuestion(t)
	f.gitlab.set(12, "failed")
	f.retry(t, true, f.owner, url.Values{"job": {"31"}})
	q = f.waitQuestion(t)
	if q.Action != `Retry job "unit" of pipeline #12 on feature in app` || q.Details[0] != (askpass.Detail{Label: "Action", Value: "Retry job"}) || q.Details[1] != (askpass.Detail{Label: "Job", Value: "unit"}) {
		t.Fatalf("the job's question reads %+v", q)
	}
	f.decide(t, q, false, false)
	if note := f.waitApprovalNotes(t, f.owner, 2)[1]; note.Note.Verdict != approval.Declined || len(f.gitlab.asked()) != 1 {
		t.Fatalf("the denied retry ran %q, note %s", f.gitlab.asked(), note.Content)
	}
}

// An approved retry resolves its pipeline again: one that is another by the
// time the user approves fails the approval and runs nothing, and so does a
// branch pushed again.
func TestAnApprovedRetryOfAPipelineThatMovedOnRunsNothing(t *testing.T) {
	f := newForgeRetryFixture(t)
	f.retry(t, true, f.owner, url.Values{})
	q := f.waitQuestion(t)
	f.gitlab.set(13, "failed")
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Failed || !strings.Contains(note.Content, `The pipeline of "feature" is #13 now, not #12.`) || len(f.gitlab.asked()) != 0 {
		t.Fatalf("the approval ran %q, note %s", f.gitlab.asked(), note.Content)
	}
	f.waitNoQuestion(t)
	f.retry(t, true, f.owner, url.Values{})
	q = f.waitQuestion(t)
	worktreeGit(t, f.repo, "commit", "-q", "--allow-empty", "-m", "pushed again")
	worktreeGit(t, f.repo, "update-ref", "refs/remotes/origin/feature", "feature")
	f.decide(t, q, true, false)
	note = f.waitApprovalNotes(t, f.owner, 2)[1]
	if note.Note.Verdict != approval.Failed || !strings.Contains(note.Content, `"feature" was pushed again and stands at`) || len(f.gitlab.asked()) != 0 {
		t.Fatalf("the approval ran %q, note %s", f.gitlab.asked(), note.Content)
	}
}

// With the approval off an assistant's retry runs at once and answers what
// its note would say.
func TestAnAssistantsRetryRunsAtOnceWithTheApprovalOff(t *testing.T) {
	f := newForgeRetryFixture(t)
	setApprovalAsks(f.s.settings, approvalForgeRetry, false)
	code, answer := f.retry(t, true, f.owner, url.Values{"job": {"31"}})
	if code != http.StatusOK || answer["retried"] != "On GitLab "+f.address+` the job "unit" of pipeline #12 of feature in app runs again: https://gitlab.example.com/group/sub/app/-/jobs/31.` || len(f.broker.Questions()) != 0 {
		t.Fatalf("the retry answered %d %v", code, answer)
	}
	if !slices.Equal(f.gitlab.asked(), []string{"/jobs/31/retry"}) {
		t.Fatalf("the retry ran %q", f.gitlab.asked())
	}
}

// A branch git took with C1 controls in its name, as GitLab hands one out,
// runs nothing again: it is refused where it is named and where HEAD stands
// on it, before anybody is asked or the forge hears of it. A name the gate
// takes still reads visible in the line a retry's question, a second ask
// and the note are read by, of the pipeline and of a job.
func TestARetryOfABranchTheGateRefusesAsksNobody(t *testing.T) {
	f := newForgeRetryFixture(t)
	branch := "x\u009b2J\u009d0;pwn\u009c"
	worktreeGit(t, f.repo, "branch", branch, "feature")
	worktreeGit(t, f.repo, "update-ref", "refs/remotes/origin/"+branch, "feature")
	if code, answer := f.retry(t, true, f.owner, url.Values{"branch": {branch}}); code != http.StatusBadRequest || answer["error"] != "A branch name holds no spaces, no control characters and no invisible characters, its character 2 is U+009B." {
		t.Fatalf("a retry of the branch answered %d %v", code, answer)
	}
	worktreeGit(t, f.repo, "checkout", "-q", branch)
	off := "The forge features are off for this branch, since its name breaks the cockpit's rule: a branch name holds no spaces, no control characters and no invisible characters, its character 2 is U+009B."
	if code, answer := f.retry(t, true, f.owner, url.Values{}); code != http.StatusConflict || answer["error"] != off {
		t.Fatalf("a retry with HEAD on the branch answered %d %v", code, answer)
	}
	if code, answer := f.retry(t, false, "", url.Values{}); code != http.StatusConflict || answer["error"] != off {
		t.Fatalf("a click's retry with HEAD on the branch answered %d %v", code, answer)
	}
	f.gitlab.mu.Lock()
	requests := f.gitlab.requests
	f.gitlab.mu.Unlock()
	if len(f.broker.Questions()) != 0 || requests != 0 {
		t.Fatalf("a refused branch asked %d questions and the forge %d times", len(f.broker.Questions()), requests)
	}
	worktreeGit(t, f.repo, "checkout", "-q", "feature")
	private := "x\ue000"
	worktreeGit(t, f.repo, "branch", private, "feature")
	worktreeGit(t, f.repo, "update-ref", "refs/remotes/origin/"+private, "feature")
	f.gitlab.on(private)
	code, answer := f.retry(t, true, f.owner, url.Values{"branch": {private}})
	what := `Retry failed jobs of pipeline #12 on x\ue000 in app`
	if code != http.StatusOK || answer["what"] != what {
		t.Fatalf("the retry answered %d %v", code, answer)
	}
	q := f.waitQuestion(t)
	if q.Action != what {
		t.Fatalf("the question reads %+v", q)
	}
	if code, answer := f.retry(t, true, f.owner, url.Values{"branch": {private}}); code != http.StatusConflict || answer["error"] != what+" already waits for the user's approval." {
		t.Fatalf("the same retry again answered %d %v", code, answer)
	}
	f.decide(t, q, false, false)
	if note := f.waitApprovalNotes(t, f.owner, 1)[0]; note.Note.Headline != "Declined: "+what || len(f.gitlab.asked()) != 0 {
		t.Fatalf("the note reads %+v, ran %q", note.Note, f.gitlab.asked())
	}
	f.waitNoQuestion(t)
	code, answer = f.retry(t, true, f.owner, url.Values{"branch": {private}, "job": {"31"}})
	if what := `Retry job "unit" of pipeline #12 on x\ue000 in app`; code != http.StatusOK || answer["what"] != what {
		t.Fatalf("the job's retry answered %d %v", code, answer)
	}
}
