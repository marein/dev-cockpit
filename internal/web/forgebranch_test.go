package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

// The editor's forge sheet shows what the poller read of the branch, its
// badge the very markup of the project row's, with the jobs and the failed
// job's log one tap away, its button sums it up in one line, and it asks the
// forge nothing for it; a project the poller does not read shows nothing. The
// end of a job's log is read from the forge at the tap, for a job of the
// pipeline the poller holds alone.
func TestTheEditorsForgeSheetShowsWhatTheForgePollerRead(t *testing.T) {
	var (
		asked []string
		mu    sync.Mutex
	)
	root := t.TempDir()
	dir := worktreeSourceRepo(t, root, "app")
	pushedOut, err := exec.Command("git", "-C", dir, "rev-parse", "feature").Output()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(fakeGitLabProject(strings.TrimSpace(string(pushedOut)), &asked, &mu))
	defer srv.Close()
	worktreeGit(t, dir, "remote", "add", "origin", srv.URL+"/group/sub/app.git")
	worktreeGit(t, dir, "update-ref", "refs/remotes/origin/feature", "feature")
	worktreeGit(t, dir, "switch", "-q", "feature")
	other := worktreeSourceRepo(t, root, "other")
	worktreeGit(t, other, "remote", "add", "origin", srv.URL+"/group/other.git")
	s, raw := ownerPages(t, worktreeProjects(t, root))
	f := forge.New(t.TempDir(), []forge.Provider{github.Provider{}, gitlab.Provider{}}, github.Builtin(), gitlab.Builtin())
	s.SetForge(f)
	addForgeHost(t, f, forge.Input{Kind: "gitlab", WebURL: srv.URL}, forgeStatusToken)
	if err := f.SetOverride("other", forge.Override{How: forge.Off}); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(asked)
	}
	if rec := raw(http.MethodGet, "/projects/app/forge/branch", "", ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "" {
		t.Fatalf("before the poller read anything the sheet got %d %q", rec.Code, rec.Body.String())
	}
	if rec := raw(http.MethodGet, "/projects/app/forge/log?job=31", "", ""); rec.Code != http.StatusNotFound || count() != 0 {
		t.Fatalf("a log before the poller read anything answered %d after %d requests", rec.Code, count())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	moved := make(chan string, 8)
	go f.Run(ctx, s.ForgeWatches, func(project string) { moved <- project })
	select {
	case <-moved:
	case <-time.After(10 * time.Second):
		t.Fatal("the poller read nothing")
	}
	read := count()
	sheet := raw(http.MethodGet, "/projects/app/forge/branch", "", "").Body.String()
	page := raw(http.MethodGet, "/projects", "", "").Body.String()
	badge := func(html string) string {
		start := strings.Index(html, `<span class="badge`)
		if start < 0 {
			t.Fatalf("no badge in %s", html)
		}
		end := strings.Index(html[start:], "\n</span>")
		return html[start : start+end]
	}
	if got, want := badge(sheet), badge(page[strings.Index(page, `id="project-app"`):]); got != want {
		t.Fatalf("the sheet's badge differs from the row's:\n%s\n%s", got, want)
	}
	for _, want := range []string{
		`<div data-forge-summary data-forge-label="GitLab">`,
		`<span class="small text-secondary text-truncate mw-100" data-forge-title>Fix the login</span>`,
		`<span hidden data-forge-bar data-forge-bar-title="GitLab: !4 draft · Pipeline #12 running"><i class="ti ti-brand-gitlab dc-ink-icon" aria-hidden="true"></i><span class="dc-ink-cap">!4</span><i class="ti ti-progress dc-icon-spinner text-blue dc-ink-icon" aria-hidden="true" data-forge-bar-pipeline="running"></i>`,
		`<button type="button" class="dropdown-item d-flex align-items-center gap-2" data-forge-jobs>`,
		`<span class="small text-secondary text-truncate">5 jobs, 5 failed</span>`,
		`<button type="button" class="dropdown-item d-flex align-items-center gap-2" data-forge-job="31" data-forge-job-name="unit" data-forge-job-status="failed">`,
		`<span class="text-truncate">Log of unit</span><span class="small text-secondary text-truncate">test · failed</span>`,
		`<div data-forge-jobs-level data-forge-level-title="Pipeline #12 running">`,
		`<a class="dropdown-item d-flex align-items-center gap-2" href="https://gitlab.example.com/group/sub/app/-/pipelines/12" target="_blank" rel="noopener" data-editor-sheet-pin data-forge-pipeline-page>`,
		`<a class="dropdown-item d-flex align-items-center gap-2" href="https://gitlab.example.com/group/sub/app/-/jobs/32" target="_blank" rel="noopener" title="A trigger job has no log, the jobs of its downstream pipeline do." data-forge-job-page>`,
		`<button type="button" class="dropdown-item d-flex align-items-center gap-2" title="An external commit status has no log on GitLab." disabled>`,
	} {
		if !strings.Contains(sheet, want) {
			t.Fatalf("the sheet lacks %s:\n%s", want, sheet)
		}
	}
	if strings.Count(sheet, "Log of ") != 1 {
		t.Fatalf("the sheet offers a log of a job that has none:\n%s", sheet)
	}
	if rec := raw(http.MethodGet, "/projects/other/forge/branch", "", ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "" {
		t.Fatalf("a project the poller does not read got %d %q", rec.Code, rec.Body.String())
	}
	if count() != read {
		t.Fatalf("the sheet asked the forge %d times", count()-read)
	}

	logOf := raw(http.MethodGet, "/projects/app/forge/log?job=31", "", "")
	for _, want := range []string{
		`<div data-forge-log-view data-forge-job="31" data-forge-job-status="failed">`,
		`<a class="dropdown-item d-flex align-items-center gap-2" href="https://gitlab.example.com/group/sub/app/-/jobs/31" target="_blank" rel="noopener" data-forge-job-page>`,
		`<span>Open on GitLab</span><span class="small text-secondary text-truncate">unit · test · failed</span>`,
		`data-forge-log-head>The whole log, 3 lines.</div>`,
		"data-forge-log-text>$ go test ./...\n--- FAIL: TestLogin\nFAIL</pre>",
	} {
		if logOf.Code != http.StatusOK || !strings.Contains(logOf.Body.String(), want) {
			t.Fatalf("the log answered %d without %s:\n%s", logOf.Code, want, logOf.Body.String())
		}
	}
	if count() != read+1 {
		t.Fatalf("a log asked the forge %d times", count()-read)
	}
	noLog := raw(http.MethodGet, "/projects/app/forge/log?job=32", "", "")
	if want := `data-forge-log-error>A trigger job has no log, the jobs of its downstream pipeline do.</div>`; noLog.Code != http.StatusOK || !strings.Contains(noLog.Body.String(), want) {
		t.Fatalf("a job without a log answered %d %s", noLog.Code, noLog.Body.String())
	}
	for _, c := range []struct {
		path string
		code int
		says string
	}{
		{"/projects/app/forge/log?job=99", http.StatusNotFound, `The pipeline of "feature" holds no job "99" any more.`},
		{"/projects/app/forge/log?job=" + strings.Repeat("9", maxForgeJobID+1), http.StatusBadRequest, "A job id has at most 64 characters."},
		{"/projects/other/forge/log?job=31", http.StatusNotFound, `The pipeline of "master" holds no job "31" any more.`},
		{"/projects/gone/forge/log?job=31", http.StatusNotFound, "Unknown project."},
		{"/projects/gone/forge/branch", http.StatusNotFound, "Unknown project."},
	} {
		if rec := raw(http.MethodGet, c.path, "", ""); rec.Code != c.code || rec.Body.String() != c.says {
			t.Errorf("%s answered %d %q", c.path, rec.Code, rec.Body.String())
		}
	}
	if count() != read+1 {
		t.Fatalf("a refused log asked the forge %d times", count()-read-1)
	}
}
