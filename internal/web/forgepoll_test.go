package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

// The poller keeps the branch HEAD is on of every repository bound where it
// reads, at the commit origin's tracking ref names, and follows a push, a
// checkout and a detached HEAD; a repository bound where it does not read,
// and one without a binding, it keeps not.
func TestTheForgePollerWatchesTheBranchHEADIsOn(t *testing.T) {
	root := t.TempDir()
	dir := worktreeSourceRepo(t, root, "app")
	worktreeGit(t, dir, "remote", "add", "origin", "git@gitlab.com:group/sub/app.git")
	worktreeGit(t, dir, "update-ref", "refs/remotes/origin/feature", "feature")
	worktreeGit(t, dir, "switch", "-q", "feature")
	other := worktreeSourceRepo(t, root, "other")
	worktreeGit(t, other, "remote", "add", "origin", "git@example.com:group/other.git")
	if err := os.Mkdir(filepath.Join(root, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, _ := ownerPages(t, worktreeProjects(t, root))
	s.SetForge(forge.New(t.TempDir(), []forge.Provider{github.Provider{}, gitlab.Provider{}}, github.Builtin(), gitlab.Builtin()))
	rev := func(ref string) string {
		out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	watches := func(polled bool) []string {
		t.Helper()
		ws, err := s.ForgeWatches(context.Background(), func(b forge.Binding) bool { return polled && b.Host.ID == "gitlab.com" })
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, w := range ws {
			out = append(out, w.Project+" "+w.Binding.Path+" "+w.Branch+">"+w.Remote+"@"+w.Head)
		}
		return out
	}
	if got, want := strings.Join(watches(true), ","), "app group/sub/app feature>feature@"+rev("feature"); got != want {
		t.Fatalf("watched %q, want %q", got, want)
	}
	if got := watches(false); len(got) != 0 {
		t.Fatalf("a binding the poller does not read is watched: %q", got)
	}
	worktreeGit(t, dir, "commit", "-q", "--allow-empty", "-m", "pushed")
	worktreeGit(t, dir, "push", "-q", ".", "HEAD:refs/remotes/origin/feature")
	if got, want := strings.Join(watches(true), ","), "app group/sub/app feature>feature@"+rev("HEAD"); got != want {
		t.Fatalf("after a push watched %q, want %q", got, want)
	}
	worktreeGit(t, dir, "switch", "-q", "master")
	if got, want := strings.Join(watches(true), ","), "app group/sub/app master>master@"; got != want {
		t.Fatalf("after a checkout watched %q, want %q", got, want)
	}
	worktreeGit(t, dir, "switch", "-q", "--detach", "feature")
	if got := watches(true); len(got) != 0 {
		t.Fatalf("a detached HEAD is watched: %q", got)
	}
}

// What the poller read stands on the project's row as its badge, the change
// request and the pipeline each linking to its page, and nowhere on a row
// it does not poll. The status read answers from the same read while it is
// fresh and asks the forge when asked to.
func TestTheProjectRowShowsWhatTheForgePollerRead(t *testing.T) {
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
	worktreeGit(t, other, "update-ref", "refs/remotes/origin/feature", "feature")
	worktreeGit(t, other, "switch", "-q", "feature")
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
	if strings.Contains(raw(http.MethodGet, "/projects", "", "").Body.String(), "data-forge-badge") {
		t.Fatal("a badge stands before the poller read anything")
	}
	if count() != 0 {
		t.Fatal("the projects page asked the forge")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	moved := make(chan string, 8)
	go f.Run(ctx, s.ForgeWatches, func(project string) { moved <- project })
	select {
	case project := <-moved:
		if project != "app" {
			t.Fatalf("the poller told %q", project)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the poller read nothing")
	}
	read := count()
	page := raw(http.MethodGet, "/projects", "", "").Body.String()
	row, rest, _ := strings.Cut(page[strings.Index(page, `id="project-app"`):], `id="project-other"`)
	for _, want := range []string{
		`<span class="badge bg-blue-lt d-inline-flex align-items-center gap-2 text-nowrap" data-forge-badge data-forge-pipeline="running">`,
		`<a href="https://gitlab.example.com/group/sub/app/-/merge_requests/4" target="_blank" rel="noopener" class="text-reset text-decoration-none d-inline-flex align-items-center gap-1" title="Merge request !4, draft: Fix the login" data-forge-change><i class="ti ti-git-pull-request-draft dc-ink-icon" aria-hidden="true"></i><span class="dc-ink-cap">!4</span></a>`,
		`<a href="https://gitlab.example.com/group/sub/app/-/pipelines/12" target="_blank" rel="noopener" class="text-reset text-decoration-none d-inline-flex align-items-center gap-1" title="Pipeline #12 running" data-forge-status><i class="ti ti-progress dc-icon-spinner dc-ink-icon" aria-hidden="true"></i><span class="dc-ink-cap text-capitalize">running</span></a>`,
	} {
		if !strings.Contains(row, want) {
			t.Fatalf("the row lacks %s:\n%s", want, row)
		}
	}
	if strings.Contains(rest, "data-forge-badge") {
		t.Fatal("a project the poller does not read wears a badge")
	}
	if count() != read {
		t.Fatal("the projects page asked the forge")
	}
	if code, answer := readForgeStatus(t, raw, "/projects/app/forge/status"); code != http.StatusOK || answer.Change == nil || answer.Pipeline == nil || answer.Pipeline.Status != "running" || count() != read {
		t.Fatalf("a fresh read answered %d %+v after %d requests", code, answer, count()-read)
	}
	if code, _ := readForgeStatus(t, raw, "/projects/app/forge/status?fresh=1"); code != http.StatusOK || count() != read+5 {
		t.Fatalf("asked for fresh answered %d after %d requests", code, count()-read)
	}
}

func readForgeStatus(t *testing.T, raw func(method, path, contentType, body string) *httptest.ResponseRecorder, path string) (int, forgeStatusAnswer) {
	t.Helper()
	rec := raw(http.MethodGet, path, "", "")
	var answer forgeStatusAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("%s answered %d %q", path, rec.Code, rec.Body.String())
	}
	return rec.Code, answer
}
