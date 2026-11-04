package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/activity"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
	"github.com/marein/dev-cockpit/internal/linecomment"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
)

const projectsOwner = "11111111-1111-4111-8111-111111111111"

// projectsServer is a server with one assistant and a projects root holding
// dirs, its jobs saved into the store the returned function writes.
func projectsServer(t *testing.T, dirs ...string) (*Server, func(assistant.Job), string) {
	t.Helper()
	stateDir := t.TempDir()
	conversations, _, err := assistant.New(stateDir, noCoders{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	assistant.NewStore(stateDir).Save(assistant.Instance{Summary: assistant.Summary{ID: projectsOwner, Title: "One", CoderID: "claude"}})
	jobs := assistant.NewJobs(assistant.NewStore(stateDir))
	root := t.TempDir()
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{
		assistants:   conversations,
		watcher:      assistant.NewWatcher(conversations, jobs, nil, nil),
		projects:     project.NewRepository(root, nil),
		lineComments: linecomment.New(stateDir),
		activity:     activity.NewTracker(),
		notifier:     notify.NewService(filepath.Join(stateDir, "notifications.json"), nil),
	}
	save := func(job assistant.Job) {
		job.CoderID, job.DoneWhen = "claude", "x"
		jobs.Of(projectsOwner).Save(job)
	}
	return s, save, root
}

func cardNames(data render.AssistantProjectsData) []string {
	var names []string
	for _, card := range data.Projects {
		names = append(names, card.Name)
	}
	return names
}

// A project with an open job leads, the quiet ones follow by their newest job,
// and past the cap they fold behind one row that names them. A job outside every project and
// a project whose directory is gone make no card and count nowhere.
func TestProjectCardsPutOpenWorkFirstAndFoldTheQuietTail(t *testing.T) {
	var dirs []string
	for i := 0; i < closedProjectsShown+2; i++ {
		dirs = append(dirs, fmt.Sprintf("quiet-%d", i))
	}
	s, save, _ := projectsServer(t, append(dirs, "busy")...)
	now := time.Now().UTC()
	save(assistant.Job{Terminal: "open-1", Project: "busy", State: assistant.JobSteering, CreatedAt: now.Add(-2 * time.Hour)})
	for i, dir := range dirs {
		save(assistant.Job{Terminal: "closed-" + dir, Project: dir, State: assistant.JobDone, CreatedAt: now.Add(-time.Duration(i) * time.Minute)})
	}
	save(assistant.Job{Terminal: "gone-1", Project: "gone", State: assistant.JobSteering, CreatedAt: now})
	save(assistant.Job{Terminal: "loose-1", State: assistant.JobDone, CreatedAt: now})

	data := s.assistantProjectsData(context.Background(), projectsOwner, false)
	want := []string{"busy", "quiet-0", "quiet-1", "quiet-2", "quiet-3", "quiet-4"}
	if got := cardNames(data); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("the cards read %v, want %v", got, want)
	}
	if data.Needs != 0 || strings.Join(data.Folded, " ") != "quiet-5 quiet-6" {
		t.Fatalf("want none that needs the user and quiet-5, quiet-6 folded, got %d and %v", data.Needs, data.Folded)
	}
	if !data.Projects[0].Open || data.Projects[1].Open {
		t.Fatal("the open card is not marked as the open one")
	}
	if data.Projects[0].URL != "/projects#project-busy" {
		t.Fatalf("the card leads to %q", data.Projects[0].URL)
	}
	all := s.assistantProjectsData(context.Background(), projectsOwner, true)
	if len(all.Projects) != 1+len(dirs) || len(all.Folded) != 0 || all.Older != 2 || !all.Unfolded {
		t.Fatalf("all has to show every card and fold 2 again, got %d, %v folded, %d older", len(all.Projects), all.Folded, all.Older)
	}
}

// A card keeps every open job and the newest closed ones in sight, the older
// closed ones stand under its fold.
func TestAProjectCardFoldsItsOlderClosedJobs(t *testing.T) {
	s, save, _ := projectsServer(t, "busy")
	now := time.Now().UTC()
	save(assistant.Job{Terminal: "open-1", Project: "busy", State: assistant.JobSteering, CreatedAt: now.Add(-time.Hour)})
	for i := 0; i < closedCardJobsShown+2; i++ {
		save(assistant.Job{Terminal: fmt.Sprintf("closed-%d", i), Project: "busy", State: assistant.JobBlocked, CreatedAt: now.Add(-time.Duration(i) * time.Minute)})
	}
	card := s.assistantProjectsData(context.Background(), projectsOwner, false).Projects[0]
	if card.Older != 2 || len(card.ShownJobs()) != 1+closedCardJobsShown {
		t.Fatalf("want %d rows and 2 folded, got %d and %d", 1+closedCardJobsShown, len(card.ShownJobs()), card.Older)
	}
	if card.ShownJobs()[0].Terminal != "open-1" || card.OlderJobs()[1].Terminal != fmt.Sprintf("closed-%d", closedCardJobsShown+1) {
		t.Fatalf("the rows stand in the wrong order: %+v", card.Jobs)
	}
	if !strings.HasPrefix(card.FoldID(), "assistant-project-") || strings.ContainsAny(card.FoldID(), " .#") {
		t.Fatalf("the fold id %q is no plain id", card.FoldID())
	}
}

// The card reads the working copy once: the branch, no upstream and every
// changed file, untracked ones included. A directory without git says so.
func TestAProjectCardReadsTheWorkingCopy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	s, save, root := projectsServer(t, "repo", "plain")
	init := exec.Command("git", "init", "-q", "-b", "work")
	init.Dir = filepath.Join(root, "repo")
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, "repo", name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	save(assistant.Job{Terminal: "t-repo", Project: "repo", State: assistant.JobSteering, CreatedAt: now})
	save(assistant.Job{Terminal: "t-plain", Project: "plain", State: assistant.JobDone, CreatedAt: now})

	data := s.assistantProjectsData(context.Background(), projectsOwner, false)
	repo, plain := data.Projects[0].Git, data.Projects[1].Git
	if !repo.Repo || !repo.Read || repo.Branch != "work" || repo.Changed != 2 || repo.Counted || repo.Upstream != "" {
		t.Fatalf("the repository reads %+v", repo)
	}
	if plain.Repo || plain.Read {
		t.Fatalf("the plain directory reads %+v", plain)
	}
}

// The tab is read on the page. An assistant's command over the local socket
// reaches neither the list, the watch nor a file's diff, and a missing
// assistant is a 404.
func TestTheProjectsTabRefusesTheLocalSocket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, _, _ := projectsServer(t)
	r := gin.New()
	r.GET(assistantProjectsPath, s.handleAssistantProjects)
	r.POST(assistantProjectsWatchPath, s.handleAssistantProjectsWatch)
	r.GET(assistantProjectDiffPath, s.handleAssistantProjectDiff)
	for path, method := range map[string]string{assistantProjectsPath: http.MethodGet, assistantProjectsWatchPath: http.MethodPost, assistantProjectDiffPath: http.MethodGet} {
		req := httptest.NewRequest(method, path+"?assistant="+projectsOwner, nil)
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s over the socket answered %d", method, rec.Code)
		}
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path+"?assistant=nobody", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s of a missing assistant answered %d", method, rec.Code)
		}
	}
}

// A guest reads the tab of its link's assistant whole: a card for a project
// off the link too, but a way into a project only where the link grants it,
// and no coder the link does not show. Another assistant's tab is the plain
// 404, the watch takes the link's CSRF token and answers like the owner's.
func TestAGuestReadsTheProjectsTabOfItsAssistant(t *testing.T) {
	sa := newSharedAssistant(t)
	jobs := assistant.NewJobs(assistant.NewStore(sa.s.cfg.StateDir)).Of(sa.id)
	now := time.Now().UTC()
	for _, job := range []assistant.Job{
		{Terminal: "coder-shop", Project: "shop", State: assistant.JobSteering, CreatedAt: now},
		{Terminal: "coder-blog", Project: "blog", State: assistant.JobDone, CreatedAt: now},
	} {
		job.CoderID, job.DoneWhen = "claude", "x"
		jobs.Save(job)
	}
	base := sa.link(t, []string{"shop"}, sa.id)

	page := sa.guest(base + "/assistants/" + sa.id).Body.String()
	for _, want := range []string{
		`id="assistant-projects-tab"`, `list-url="` + base + `/assistants/projects?assistant=` + sa.id + `"`,
		`watch-url="` + base + `/assistants/projects/watch?assistant=` + sa.id + `"`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the guest's aside lacks %s", want)
		}
	}
	rec := sa.guest(base + assistantProjectsPath + "?assistant=" + sa.id)
	list := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("the guest's tab answered %d", rec.Code)
	}
	for _, want := range []string{
		`data-assistant-project="shop"`, `data-assistant-project="blog"`,
		`href="` + base + `/projects/shop/editor" data-assistant-project-link`,
	} {
		if !strings.Contains(list, want) {
			t.Fatalf("the guest's tab lacks %s:\n%s", want, list)
		}
	}
	for _, never := range []string{`/projects/blog/editor`, `href="/projects`, `href="/coders/`, "<form", "csrf_token"} {
		if strings.Contains(list, never) {
			t.Fatalf("the guest's tab carries %s:\n%s", never, list)
		}
	}
	sa.notFound(t, "another assistant's tab", base+assistantProjectsPath+"?assistant="+sa.other)

	token := strings.TrimPrefix(base, sharePathPrefix)
	watch := func(csrf string) *httptest.ResponseRecorder {
		r := gin.New()
		r.SetHTMLTemplate(render.HTMLTemplate(sa.s.assets.assetPath, "test", "test", sa.s.plugins, nil))
		r.NoRoute(sa.s.handleNotFound)
		sa.s.registerShareRoutes(r)
		req := httptest.NewRequest(http.MethodPost, base+assistantProjectsWatchPath+"?assistant="+sa.id, nil)
		req.Header.Set("X-CSRF-Token", csrf)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := watch(""); rec.Code == http.StatusOK {
		t.Fatal("a watch without the link's token went through")
	}
	rec = watch(guestCSRF(token))
	var answer struct {
		Watching bool `json:"watching"`
		Seconds  int  `json:"seconds"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil || answer.Seconds != int(gitWatchWindow/time.Second) {
		t.Fatalf("the guest's watch answered %d %s", rec.Code, rec.Body.String())
	}
}

// A project whose newest job of the assistant closed blocked or expired
// needs the user: it stands first, never folds and counts on the tab. A job
// started there since, open or closed, takes that back.
func TestAProjectWhoseNewestJobIsStuckNeedsTheUser(t *testing.T) {
	var quiet []string
	for i := 0; i < closedProjectsShown+1; i++ {
		quiet = append(quiet, fmt.Sprintf("quiet-%d", i))
	}
	s, save, _ := projectsServer(t, append(quiet, "blocked", "expired", "resumed", "settled", "busy")...)
	now := time.Now().UTC()
	at := func(minutes int) time.Time { return now.Add(-time.Duration(minutes) * time.Minute) }
	save(assistant.Job{Terminal: "blocked-1", Project: "blocked", State: assistant.JobBlocked, CreatedAt: at(90)})
	save(assistant.Job{Terminal: "expired-1", Project: "expired", State: assistant.JobExpired, CreatedAt: at(80)})
	save(assistant.Job{Terminal: "resumed-1", Project: "resumed", State: assistant.JobBlocked, CreatedAt: at(70)})
	save(assistant.Job{Terminal: "resumed-2", Project: "resumed", State: assistant.JobSteering, CreatedAt: at(60)})
	save(assistant.Job{Terminal: "settled-1", Project: "settled", State: assistant.JobBlocked, CreatedAt: at(50)})
	save(assistant.Job{Terminal: "settled-2", Project: "settled", State: assistant.JobDone, CreatedAt: at(40)})
	save(assistant.Job{Terminal: "busy-1", Project: "busy", State: assistant.JobSteering, CreatedAt: at(30)})
	for i, dir := range quiet {
		save(assistant.Job{Terminal: "quiet-job-" + dir, Project: dir, State: assistant.JobDone, CreatedAt: at(i)})
	}

	data := s.assistantProjectsData(context.Background(), projectsOwner, false)
	names := cardNames(data)
	if strings.Join(names[:4], " ") != "expired blocked busy resumed" {
		t.Fatalf("the cards read %v", names)
	}
	if data.Needs != 2 || data.Older != 2 || len(data.Folded) != 2 || data.Unfolded {
		t.Fatalf("want 2 that need the user and 2 folded, got %d and %v", data.Needs, data.Folded)
	}
	if got := data.Projects[0].NeedsWhy(); got != "Its newest job expired." {
		t.Fatalf("the expired card says %q", got)
	}
	if got := data.Projects[1].NeedsWhy(); got != "Its newest job is blocked." {
		t.Fatalf("the blocked card says %q", got)
	}
	for _, card := range data.Projects[2:] {
		if len(card.Needs) > 0 {
			t.Fatalf("%s needs the user: %v", card.Name, card.Needs)
		}
	}
}

// failingGitLab answers like fakeGitLabProject, its pipeline failed while
// failed says so.
func failingGitLab(head string, failed *atomic.Bool, asked *[]string, mu *sync.Mutex) http.Handler {
	fake := fakeGitLabProject(head, asked, mu)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failed.Load() && r.URL.EscapedPath() == "/api/v4/projects/group%2Fsub%2Fapp/pipelines" && r.Header.Get("Authorization") == "Bearer "+forgeStatusToken {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `[{"id":12,"iid":3,"project_id":2,"sha":%q,"ref":"feature","status":"failed","source":"push","web_url":"https://gitlab.example.com/group/sub/app/-/pipelines/12"}]`, head)
			return
		}
		fake.ServeHTTP(w, r)
	})
}

// A card of a project bound to a forge shows what the poller read of its
// branch, read from the poller's cache alone: the change request, the
// pipeline with its stages, the counts of the open threads, the forge's and
// the local ones, on a line and on the project, and of the unpublished
// comments. A forge whose read failed
// says why, a branch never pushed and a project bound to no forge show git
// alone, the latter its local threads too. A failed pipeline
// puts the card first, offers the forge's run of the failed jobs again and
// counts on the tab.
func TestAProjectCardShowsWhatTheForgePollerRead(t *testing.T) {
	var (
		asked  []string
		mu     sync.Mutex
		failed atomic.Bool
	)
	root := t.TempDir()
	dir := worktreeSourceRepo(t, root, "app")
	out, err := exec.Command("git", "-C", dir, "rev-parse", "feature").Output()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(failingGitLab(strings.TrimSpace(string(out)), &failed, &asked, &mu))
	defer srv.Close()
	worktreeGit(t, dir, "remote", "add", "origin", srv.URL+"/group/sub/app.git")
	worktreeGit(t, dir, "update-ref", "refs/remotes/origin/feature", "feature")
	worktreeGit(t, dir, "switch", "-q", "feature")
	lost := worktreeSourceRepo(t, root, "lost")
	worktreeGit(t, lost, "remote", "add", "origin", srv.URL+"/group/gone.git")
	worktreeGit(t, lost, "update-ref", "refs/remotes/origin/master", "master")
	fresh := worktreeSourceRepo(t, root, "fresh")
	worktreeGit(t, fresh, "remote", "add", "origin", srv.URL+"/group/sub/app.git")
	worktreeSourceRepo(t, root, "plain")
	s, raw := ownerPages(t, worktreeProjects(t, root))
	f := forge.New(t.TempDir(), []forge.Provider{github.Provider{}, gitlab.Provider{}}, github.Builtin(), gitlab.Builtin())
	s.SetForge(f)
	addForgeHost(t, f, forge.Input{Kind: "gitlab", WebURL: srv.URL}, forgeStatusToken)
	inst, err := s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	jobs := assistant.NewJobs(assistant.NewStore(s.cfg.StateDir)).Of(inst.ID)
	now := time.Now().UTC()
	for i, name := range []string{"plain", "fresh", "lost", "app"} {
		jobs.Save(assistant.Job{Terminal: "t-" + name, Project: name, CoderID: "claude", DoneWhen: "x", State: assistant.JobDone, CreatedAt: now.Add(time.Duration(i) * time.Minute)})
	}
	for _, name := range []string{"app", "plain"} {
		for _, c := range []linecomment.Comment{{Path: "a.txt", Line: 1, Text: "mine"}, {Project: true, Text: "whole"}} {
			if _, _, err := s.lineComments.Save(name, c, linecomment.Writer{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	thread := func(id string, resolved bool) linecomment.ForgeThread {
		return linecomment.ForgeThread{Kind: "gitlab", ID: id, Resolved: resolved, At: linecomment.Place{Path: "a.txt", Line: 1, Quote: "a", Quoted: true},
			Notes: []linecomment.ForgeNote{{ID: id + "-1", Author: "root", Text: "Why?", Created: now}}}
	}
	general := linecomment.ForgeThread{Kind: "gitlab", ID: "g1", Project: true, Notes: []linecomment.ForgeNote{{ID: "g1-1", Author: "root", Text: "And?", Created: now}}}
	s.lineComments.SyncForge("app", dir, []linecomment.ForgeThread{thread("d1", false), thread("d2", true), general}, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.Run(ctx, s.ForgeWatches, func(string) {})
	cards := func() map[string]render.AssistantProjectCard {
		byName := map[string]render.AssistantProjectCard{}
		for _, card := range s.assistantProjectsData(context.Background(), inst.ID, false).Projects {
			byName[card.Name] = card
		}
		return byName
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		read := cards()
		if read["app"].Forge != nil && read["app"].Forge.Pipeline != nil && read["lost"].Forge != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the poller read nothing: %+v", read)
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	before := len(asked)
	mu.Unlock()
	list := func() string {
		rec := raw(http.MethodGet, assistantProjectsPath+"?assistant="+inst.ID, "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("the tab answered %d", rec.Code)
		}
		return rec.Body.String()
	}
	html := list()
	read := cards()
	app := read["app"]
	if app.Forge.Change == nil || app.Forge.Change.Number != 4 || app.Forge.Pipeline.Status != forge.Running || len(app.Needs) > 0 || app.Forge.Retry != nil {
		t.Fatalf("the app card reads %+v", app.Forge)
	}
	if got := fmt.Sprint(app.Forge.Stages()); got != "[{test failed} {deploy failed} {ci/33 failed} {ci/34 failed} {ci/35 failed}]" {
		t.Fatalf("the stages read %s", got)
	}
	if app.Threads != 4 || app.Unpublished != 2 || app.ReviewLine() != "4 open threads · 2 unpublished comments" {
		t.Fatalf("the review counts read %d and %d", app.Threads, app.Unpublished)
	}
	if plain := read["plain"]; plain.Threads != 2 || plain.Unpublished != 0 || plain.ReviewLine() != "2 open threads" {
		t.Fatalf("the card without a forge counts %d and %d", plain.Threads, plain.Unpublished)
	}
	for _, want := range []string{
		`data-assistant-projects-needs="0"`,
		`<i class="ti ti-brand-gitlab"></i>`,
		`data-assistant-project-change="open" data-assistant-project-draft>`,
		`href="https://gitlab.example.com/group/sub/app/-/merge_requests/4" target="_blank" rel="noopener" title="Merge request !4, draft: Fix the login" data-assistant-project-change-link><span class="fw-medium">!4</span> Fix the login</a>`,
		`data-assistant-project-change-state>draft · open</span>`,
		`data-assistant-project-pipeline="running">`,
		`title="Pipeline #12 running" data-assistant-project-pipeline-link>Pipeline</a>`,
		`<div class="progress-bar flex-fill bg-red" data-assistant-project-stage="failed"></div>`,
		`data-assistant-project-pipeline-note>running</span>`,
		`data-assistant-project-open="app" data-assistant-project-section="reviews" data-assistant-project-threads="4" data-assistant-project-unpublished="2">`,
		`data-assistant-project-open="plain" data-assistant-project-section="reviews" data-assistant-project-threads="2" data-assistant-project-unpublished="0">`,
		`The last read of the forge failed: `,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("the tab lacks %s:\n%s", want, html)
		}
	}
	if strings.Contains(html, "data-assistant-project-retry") || strings.Contains(html, "data-assistant-project-needs>") {
		t.Fatalf("a running pipeline offers a retry or needs the user:\n%s", html)
	}
	if lostCard := read["lost"]; lostCard.Forge.Change != nil || lostCard.Forge.Pipeline != nil || lostCard.Forge.NoChange != "" || lostCard.ReviewLine() != "" {
		t.Fatalf("a failed read shows more than its problem: %+v", lostCard.Forge)
	}
	if read["fresh"].Forge != nil || read["plain"].Forge != nil {
		t.Fatalf("a branch never pushed or a project without a forge shows a forge part: %+v %+v", read["fresh"].Forge, read["plain"].Forge)
	}
	mu.Lock()
	if len(asked) != before {
		t.Fatalf("the tab asked the forge %d times", len(asked)-before)
	}
	mu.Unlock()

	failed.Store(true)
	p, err := s.projects.FindByName("app")
	if err != nil {
		t.Fatal(err)
	}
	w, _, _, err := s.forgeWatch(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Status(context.Background(), w, true); err != nil {
		t.Fatal(err)
	}
	data := s.assistantProjectsData(context.Background(), inst.ID, false)
	app = data.Projects[0]
	if app.Name != "app" || data.Needs != 1 || app.NeedsWhy() != "The pipeline failed." || app.Forge.Retry == nil || app.Forge.RetryURL != "/projects/app/forge/retry" {
		t.Fatalf("a failed pipeline reads %+v, %+v", data.Needs, app)
	}
	if got := app.Forge.PipelineNote(); got != "unit +4" {
		t.Fatalf("the failed job reads %q", got)
	}
	html = list()
	for _, want := range []string{
		`data-assistant-projects-needs="1"`,
		`<div class="card border-danger" data-assistant-project="app">`,
		`data-assistant-project-needs>Needs you</span>`,
		`<div class="small text-red" data-assistant-project-needs-why>The pipeline failed.</div>`,
		`data-assistant-project-pipeline-note>unit &#43;4</span>`,
		`data-assistant-project-retry data-git-retry="/projects/app/forge/retry" data-git-project="app" data-git-retry-branch="feature" data-git-retry-head="` + w.Head + `" data-git-retry-pipeline="12">`,
		`</i>Retry</button>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("the tab lacks %s:\n%s", want, html)
		}
	}
}
