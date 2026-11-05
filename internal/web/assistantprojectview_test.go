package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
	"github.com/marein/dev-cockpit/internal/linecomment"
	"github.com/marein/dev-cockpit/internal/web/render"
)

func forgeThreadAt(id, quote string, resolved bool, created time.Time) linecomment.ForgeThread {
	return linecomment.ForgeThread{Kind: "gitlab", ID: id, URL: "https://gitlab.example.com/group/sub/app/-/merge_requests/4#note_" + id, Resolved: resolved,
		At:    linecomment.Place{Path: "a.txt", Line: 1, Quote: quote, Quoted: true},
		Notes: []linecomment.ForgeNote{{ID: id + "-1", Author: "root", Text: "Why " + id + "?", Created: created}, {ID: id + "-2", Text: "Because.", Created: created}}}
}

// A card opened in the aside lists the review threads of its branch and the
// local threads from the line comments: the open ones with file, line, where
// they live, quote, notes and what they take, a thread of the forge there and
// a local one through the editor's line comments, outdated where the file lost
// the quoted line, the resolved ones folded, a local one to reopen, and the
// owner's comments a publish would post. The project comments stand first,
// titled without a place: a local one, a general thread of the forge, a
// general note the forge resolves not and one it keeps alone, each taking
// what the forge takes. The hand off names the thread, the project, the
// branch and the place where there is one. A project no job names answers
// 404.
func TestAProjectViewListsTheReviewThreadsOfItsBranch(t *testing.T) {
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
	worktreeSourceRepo(t, root, "elsewhere")
	s, raw := ownerPages(t, worktreeProjects(t, root))
	f := forge.New(t.TempDir(), []forge.Provider{github.Provider{}, gitlab.Provider{}}, github.Builtin(), gitlab.Builtin())
	s.SetForge(f)
	addForgeHost(t, f, forge.Input{Kind: "gitlab", WebURL: srv.URL}, forgeStatusToken)
	inst, err := s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	assistant.NewJobs(assistant.NewStore(s.cfg.StateDir)).Of(inst.ID).Save(assistant.Job{Terminal: "t-app", Project: "app", CoderID: "claude", DoneWhen: "x", State: assistant.JobSteering, CreatedAt: now})
	save := func(text string) linecomment.Comment {
		t.Helper()
		c, _, err := s.lineComments.Save("app", linecomment.Comment{Path: "a.txt", Line: 1, LineText: "a", Text: text}, linecomment.Writer{})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	mine, answered, done := save("mine"), save("answered"), save("done")
	whole, _, err := s.lineComments.Save("app", linecomment.Comment{Project: true, Text: "whole"}, linecomment.Writer{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.lineComments.Reply("app", answered.ID, linecomment.Reply{Text: "yes"}, linecomment.Writer{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.lineComments.Resolve("app", done.ID, true, linecomment.Writer{}); err != nil {
		t.Fatal(err)
	}
	general := func(id string, unresolvable bool) linecomment.ForgeThread {
		return linecomment.ForgeThread{Kind: "gitlab", ID: id, URL: "https://gitlab.example.com/group/sub/app/-/merge_requests/4#note_" + id + "n", Project: true, Unresolvable: unresolvable,
			Notes: []linecomment.ForgeNote{{ID: id + "n", Author: "root", Text: "General " + id, Created: now}}}
	}
	s.lineComments.SyncForge("app", dir, []linecomment.ForgeThread{forgeThreadAt("d1", "a", false, now), forgeThreadAt("d2", "a", true, now), forgeThreadAt("d3", "gone", false, now),
		general("g1", false), general("g2", true), general("", true)}, true)
	lone := ""
	for _, c := range s.lineComments.List("app") {
		if c.Forge != nil && c.Project && c.Forge.Thread == "" {
			lone = c.ID
		}
	}
	if lone == "" {
		t.Fatal("the note the forge keeps alone is not a project comment")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.Run(ctx, s.ForgeWatches, func(string) {})
	p, err := s.projects.FindByName("app")
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, polled, ok := s.forgePolledOf(p); ok && polled.Status.Change != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the poller read no change request")
		}
	}
	view := func(project string) *httptest.ResponseRecorder {
		return raw(http.MethodGet, assistantProjectViewPath+"?assistant="+inst.ID+"&project="+project, "", "")
	}
	rec := view("app")
	html := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("the view answered %d: %s", rec.Code, html)
	}
	for _, want := range []string{
		`data-assistant-project-view-body="app"`,
		`data-assistant-project-back>`,
		`data-assistant-project-change="open" data-assistant-project-draft>`,
		`data-assistant-project-reviews="8" data-thread-url="/projects/app/forge/thread" data-branch="feature" data-comments-url="/projects/app/editor/comments">`,
		`<div class="card" data-assistant-thread="`,
		`data-thread="d1" data-assistant-thread-path="a.txt" data-assistant-thread-line="1" data-assistant-thread-mark="open">`,
		`href="/projects/app/editor?file=a.txt&amp;line=1" title="a.txt:1" data-file-link data-assistant-thread-file>a.txt:1</a>`,
		`title="A review thread on GitLab" data-assistant-thread-origin="forge"><i class="ti ti-brand-gitlab me-1" aria-hidden="true"></i>GitLab</span>`,
		`<div class="card" data-assistant-thread="` + mine.ID + `" data-assistant-thread-local data-assistant-thread-path="a.txt" data-assistant-thread-line="1" data-assistant-thread-mark="open">`,
		`data-assistant-thread-origin="local">local</span>`,
		`<template data-assistant-diff-comment="/projects/app/editor/comments">`,
		"data-assistant-thread-handoff=\"Line comment " + mine.ID + " in project app on branch feature at a.txt:1.\n\"",
		`data-assistant-thread-quote>a</div>`,
		`data-comment-note-author>root</span>`,
		`data-comment-note-text>Why d1?</div>`,
		`data-comment-note-author>GitLab</span>`,
		`href="https://gitlab.example.com/group/sub/app/-/merge_requests/4#note_d1" target="_blank" rel="noopener" title="Open the thread on GitLab"`,
		"data-assistant-thread-handoff=\"Review thread d1 in project app on branch feature at a.txt:1.\n\"",
		`data-assistant-thread-reply-open>`,
		`data-assistant-thread-resolve>`,
		`<form class="px-3 pb-2" hidden novalidate data-assistant-thread-reply data-thread="d1">`,
		`It is posted on merge request !4 on GitLab, for everybody who can read it there.`,
		`data-assistant-project-unpublished-box="3"`,
		`2 line comments and 1 project comment not published`,
		`data-assistant-project-publish="/projects/app/forge/publish" data-forge-publish-forge="GitLab" data-forge-publish-noun="merge request" data-forge-publish-ref="!4" data-ids="` + whole.ID + ` ` + mine.ID + ` ` + done.ID + `" data-general="1"><i class="ti ti-send me-1" aria-hidden="true"></i>Publish</button>`,
		`data-assistant-project-resolved-toggle><span data-fold-shut>Show 2 resolved threads</span><span data-fold-open>Hide resolved threads</span></button>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("the view lacks %s:\n%s", want, html)
		}
	}
	open, resolved, _ := strings.Cut(html, "data-assistant-project-threads-resolved")
	if strings.Contains(open, `data-thread="d2"`) || !strings.Contains(resolved, `data-thread="d2"`) || strings.Count(resolved, "data-assistant-thread-resolve>") != 0 {
		t.Fatalf("the resolved thread stands among the open ones or offers a resolve:\n%s", html)
	}
	local := func(id string) string {
		at := html[strings.Index(html, `data-assistant-thread="`+id+`"`):]
		if end := strings.Index(at[1:], `<div class="card" data-assistant-thread=`); end >= 0 {
			return at[:end+1]
		}
		return at
	}
	for id, want := range map[string][]string{
		mine.ID:     {`data-comment-note-author>You</span>`, `data-comment-note-text>mine</div>`, `data-assistant-thread-reply-open>`, `data-assistant-thread-resolve>`, `<form class="px-3 pb-2" hidden novalidate data-assistant-thread-reply>`, `value="1" data-assistant-thread-reply-resolve><span class="form-check-label">Resolve the thread</span>`},
		answered.ID: {`data-comment-note-text>yes</div>`, `data-assistant-thread-resolve>`, `<span class="ms-auto d-flex gap-1 me-n2">`, `title="Delete reply" aria-label="Delete reply" data-comment-note-drop>`},
		done.ID:     {`data-assistant-thread-reopen>`, `value="0" data-assistant-thread-reply-resolve><span class="form-check-label">Reopen the thread</span>`},
	} {
		chunk := local(id)
		for _, w := range append(want, `title="Delete comment" aria-label="Delete comment" data-assistant-thread-delete>`) {
			if !strings.Contains(chunk, w) {
				t.Fatalf("the local thread lacks %s:\n%s", w, chunk)
			}
		}
		if strings.Contains(chunk, "form-hint") || strings.Contains(chunk, "data-thread=") || strings.Contains(chunk, "data-assistant-thread-forge") {
			t.Fatalf("a local thread speaks of the forge:\n%s", chunk)
		}
		head, rest, _ := strings.Cut(chunk, "data-assistant-thread-notes")
		_, bin, _ := strings.Cut(head, `data-assistant-thread-origin="local">local</span>`)
		if !strings.Contains(bin, "data-assistant-thread-delete>") || strings.Contains(rest, "data-assistant-thread-delete") {
			t.Fatalf("the bin of a local thread does not stand in its head after the badge:\n%s", chunk)
		}
	}
	card := func(id string) string { return `data-assistant-thread="` + id + `"` }
	if strings.Contains(open, card(done.ID)) || !strings.Contains(resolved, card(done.ID)) || strings.Contains(resolved, card(answered.ID)) {
		t.Fatalf("the resolved local thread stands among the open ones:\n%s", html)
	}
	firstLine := strings.Index(open, "data-assistant-thread-file")
	for id, want := range map[string][]string{
		whole.ID: {`data-assistant-thread-local>`, `title="In the cockpit only" data-assistant-thread-origin="local">local</span>`, `data-comment-note-text>whole</div>`,
			`data-assistant-thread-reply-open>`, `data-assistant-thread-resolve>`, "data-assistant-thread-handoff=\"Project comment " + whole.ID + " in project app on branch feature.\n\""},
		"g1": {`data-thread="g1">`, `title="A general comment on GitLab" data-assistant-thread-origin="forge">`, `data-comment-note-text>General g1</div>`, `data-assistant-thread-forge>`,
			`data-assistant-thread-reply-open>`, `data-assistant-thread-resolve>`, "data-assistant-thread-handoff=\"General thread g1 in project app on branch feature.\n\""},
		"g2": {`data-thread="g2">`, `data-assistant-thread-reply-open>`, "data-assistant-thread-handoff=\"General thread g2 in project app on branch feature.\n\""},
		lone: {`data-comment-note-text>General </div>`, "data-assistant-thread-handoff=\"Project comment " + lone + " in project app on branch feature.\n\""},
	} {
		at := strings.Index(open, `data-assistant-thread="`+id+`"`)
		if id == "g1" || id == "g2" {
			at = strings.LastIndex(open[:strings.Index(open, `data-thread="`+id+`"`)], `<div class="card"`)
		}
		if at < 0 || at > firstLine {
			t.Fatalf("the project comment %s does not stand before the line comments:\n%s", id, html)
		}
		chunk := open[at:]
		if end := strings.Index(chunk[1:], `<div class="card" data-assistant-thread=`); end >= 0 {
			chunk = chunk[:end+1]
		}
		for _, w := range append(want, `data-assistant-thread-project>Project comment</span>`) {
			if !strings.Contains(chunk, w) {
				t.Fatalf("the project comment %s lacks %s:\n%s", id, w, chunk)
			}
		}
		if strings.Contains(chunk, "data-assistant-thread-file") || strings.Contains(chunk, "data-assistant-thread-quote") || strings.Contains(chunk, "data-assistant-thread-outdated") {
			t.Fatalf("the project comment %s names a place:\n%s", id, chunk)
		}
		if id == "g2" && strings.Contains(chunk, "data-assistant-thread-resolve") {
			t.Fatalf("a general note the forge does not resolve offers a resolve:\n%s", chunk)
		}
		if id == lone && (strings.Contains(chunk, "data-thread=") || strings.Contains(chunk, "data-assistant-thread-reply") || strings.Contains(chunk, "data-assistant-thread-resolve")) {
			t.Fatalf("a note the forge keeps alone takes a write:\n%s", chunk)
		}
	}
	d3 := html[strings.Index(html, `data-thread="d3"`):]
	if !strings.Contains(d3[:strings.Index(d3, "data-comment-note")], "data-assistant-thread-outdated>outdated</span>") {
		t.Fatalf("the thread whose line is gone is not outdated:\n%s", html)
	}
	if strings.Count(open, "data-assistant-thread-outdated") != 1 || strings.Contains(html, "data-comment-note-edit") {
		t.Fatalf("a thread that stands is outdated or a note offers an edit:\n%s", html)
	}
	if strings.Count(html, "data-assistant-thread-delete>") != 4 || strings.Count(html, "data-comment-note-drop>") != 1 {
		t.Fatalf("a thread of the forge or its note offers a delete:\n%s", html)
	}
	for at, mark := range map[string]string{card(mine.ID): "open", `data-thread="d2"`: "resolved", `data-thread="d3"`: "outdated", card(done.ID): "resolved"} {
		rest := html[strings.Index(html, at):]
		if head := rest[:strings.Index(rest, ">")]; !strings.Contains(head, `data-assistant-thread-mark="`+mark+`"`) {
			t.Fatalf("the thread %s is not marked %s for the diff:\n%s", at, mark, head)
		}
	}
	if rec := view("elsewhere"); rec.Code != http.StatusNotFound {
		t.Fatalf("a project no job names answered %d", rec.Code)
	}
	if rec := view(""); rec.Code != http.StatusNotFound {
		t.Fatalf("no project answered %d", rec.Code)
	}
	if rec := raw(http.MethodGet, assistantProjectViewPath+"?assistant=nobody&project=app", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown assistant answered %d", rec.Code)
	}
}

// A card counts what its branch brought since it split from the main branch
// and opens the project on its Changes: the counts, the base it compares
// with, the working copy's files, the branch's files with the largest change
// first, each opening the editor's compare on it, and the commits newest
// first. A project without a forge offers Reviews beside Changes with its
// field of a new project comment and says it has no comment until a local
// thread comes, which its card's review line opens.
func TestAProjectViewShowsTheChangesOfItsBranch(t *testing.T) {
	root := t.TempDir()
	dir := worktreeSourceRepo(t, root, "app")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	worktreeGit(t, dir, "switch", "-q", "feature")
	write("big.txt", "1\n2\n3\n4\n")
	worktreeGit(t, dir, "add", "-A")
	worktreeGit(t, dir, "commit", "-qm", "Add big")
	write("a.txt", "a\nb\n")
	worktreeGit(t, dir, "commit", "-qam", "Grow a")
	worktreeGit(t, dir, "switch", "-q", "master")
	write("m.txt", "m\n")
	worktreeGit(t, dir, "add", "-A")
	worktreeGit(t, dir, "commit", "-qm", "master moves on")
	worktreeGit(t, dir, "switch", "-q", "feature")
	write("a.txt", "a\nb\nc\n")
	write("new.txt", "n\n")
	split := strings.TrimSpace(gitOutput(t, dir, "merge-base", "master", "feature"))[:7]
	s, raw := ownerPages(t, worktreeProjects(t, root))
	inst, err := s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	assistant.NewJobs(assistant.NewStore(s.cfg.StateDir)).Of(inst.ID).Save(assistant.Job{Terminal: "t-app", Project: "app", CoderID: "claude", DoneWhen: "x", State: assistant.JobSteering, CreatedAt: time.Now().UTC()})

	list := raw(http.MethodGet, assistantProjectsPath+"?assistant="+inst.ID, "", "").Body.String()
	for _, want := range []string{
		`title="Working copy since the split from master at ` + split + `" data-assistant-project-changes data-assistant-project-open="app" data-assistant-project-section="changes">`,
		`<span class="text-truncate"><span data-assistant-project-commits="2">2 commits</span> · <span data-assistant-project-files="3">3 files</span> · <span class="text-green" data-assistant-project-added="7">+7</span> <span class="text-red" data-assistant-project-removed="0">−0</span></span>`,
		`<span class="text-nowrap flex-shrink-0">· <span class="text-warning" data-assistant-project-changed="2">2 uncommitted</span></span>`,
	} {
		if !strings.Contains(list, want) {
			t.Fatalf("the card lacks %s:\n%s", want, list)
		}
	}
	rec := raw(http.MethodGet, assistantProjectViewPath+"?assistant="+inst.ID+"&project=app&section=changes", "", "")
	html := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("the view answered %d: %s", rec.Code, html)
	}
	for _, want := range []string{
		`data-assistant-project-view-body="app" data-section="changes">`,
		`<section class="tab-pane active" id="assistant-project-changes" role="tabpanel" aria-labelledby="assistant-project-changes-tab" data-assistant-project-changes-section>`,
		`data-assistant-changes-commits>2</div>`,
		`data-assistant-changes-files>3</div>`,
		`data-assistant-changes-added>+7</div>`,
		`title="Working copy since the split from master at ` + split + `" data-assistant-changes-base>`,
		`data-assistant-changes-from>master</span>`,
		`data-assistant-changes-to>Working copy</span>`,
		`data-assistant-changes-split>since the split at <span class="font-monospace">` + split + `</span>`,
		`href="/projects/app/editor?file=a.txt&amp;view=compare" data-file-link data-assistant-changed-open>`,
		`data-assistant-diff data-list="files" data-path="a.txt">`,
		`data-assistant-changed-mark>U</span>`,
		`data-assistant-uncommitted-count>2</span>`,
		`data-assistant-changes-subject>Grow a</div>`,
		`data-assistant-changes-author>t</span> · <dc-time datetime="`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("the view lacks %s:\n%s", want, html)
		}
	}
	if strings.Count(html, `data-assistant-changed-files="files">`) != 1 || strings.Count(html, `data-assistant-changed-files="uncommitted">`) != 1 || strings.Contains(html, "Branch files") || strings.Contains(html, "data-assistant-changed-more") || strings.Contains(html, "data-assistant-rest-wait") {
		t.Fatalf("Changes is not the uncommitted files over one list of three files:\n%s", html)
	}
	pair := html[strings.Index(html, `data-assistant-changed-files="files">`):]
	if !(strings.Index(pair, `data-assistant-changed-file="big.txt"`) < strings.Index(pair, `data-assistant-changed-file="a.txt"`) && strings.Index(pair, `data-assistant-changed-file="a.txt"`) < strings.Index(pair, `data-assistant-changed-file="new.txt"`)) || strings.Index(html, "Grow a") > strings.Index(html, "Add big") {
		t.Fatalf("the largest file or the newest commit does not come first:\n%s", html)
	}
	for _, want := range []string{
		`data-assistant-project-switch="reviews">Reviews<span class="badge bg-secondary-lt" data-assistant-project-reviews-count>0</span>`,
		`<section class="tab-pane" id="assistant-project-reviews" role="tabpanel" aria-labelledby="assistant-project-reviews-tab" data-assistant-project-reviews="0" data-comments-url="/projects/app/editor/comments">`,
		`<div class="mb-3" data-assistant-project-comment>`,
		`data-assistant-project-threads-none>No comment.</div>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("Reviews of a project without a comment lacks %s:\n%s", want, html)
		}
	}
	here, _, err := s.lineComments.Save("app", linecomment.Comment{Path: "a.txt", Line: 1, LineText: "a", Text: "here"}, linecomment.Writer{})
	if err != nil {
		t.Fatal(err)
	}
	list = raw(http.MethodGet, assistantProjectsPath+"?assistant="+inst.ID, "", "").Body.String()
	if want := `data-assistant-project-open="app" data-assistant-project-section="reviews" data-assistant-project-threads="1" data-assistant-project-unpublished="0">`; !strings.Contains(list, want) {
		t.Fatalf("the card without a forge lacks %s:\n%s", want, list)
	}
	html = raw(http.MethodGet, assistantProjectViewPath+"?assistant="+inst.ID+"&project=app&section=reviews", "", "").Body.String()
	for _, want := range []string{
		`data-assistant-project-switch="reviews"`,
		`data-assistant-project-reviews="1" data-comments-url="/projects/app/editor/comments">`,
		`<div class="card" data-assistant-thread="` + here.ID + `" data-assistant-thread-local data-assistant-thread-path="a.txt" data-assistant-thread-line="1" data-assistant-thread-mark="open">`,
		"data-assistant-thread-handoff=\"Line comment " + here.ID + " in project app on branch feature at a.txt:1.\n\"",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("the reviews of a project without a forge lack %s:\n%s", want, html)
		}
	}
	if strings.Contains(html, "data-assistant-project-unpublished-box") || strings.Contains(html, "data-thread-url") {
		t.Fatalf("a project without a forge offers a publish or the forge's thread route:\n%s", html)
	}

	same := worktreeSourceRepo(t, root, "same")
	worktreeGit(t, same, "switch", "-q", "feature")
	assistant.NewJobs(assistant.NewStore(s.cfg.StateDir)).Of(inst.ID).Save(assistant.Job{Terminal: "t-same", Project: "same", CoderID: "claude", DoneWhen: "x", State: assistant.JobSteering, CreatedAt: time.Now().UTC()})
	list = raw(http.MethodGet, assistantProjectsPath+"?assistant="+inst.ID, "", "").Body.String()
	html = raw(http.MethodGet, assistantProjectViewPath+"?assistant="+inst.ID+"&project=same&section=changes", "", "").Body.String()
	if !strings.Contains(list, `title="Working copy since master" data-assistant-project-changes data-assistant-project-open="same"`) || !strings.Contains(html, `title="Working copy since master" data-assistant-changes-base>`) || strings.Contains(html, "data-assistant-changes-split") || !strings.Contains(html, `data-assistant-changes-commits>0</div>`) || !strings.Contains(html, "data-assistant-changed-none>No file differs.</div>") || strings.Contains(html, "data-assistant-uncommitted") {
		t.Fatalf("a branch at its base's commit does not name the base:\n%s\n%s", list, html)
	}

	fresh := filepath.Join(root, "fresh")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, fresh, "init", "-q")
	assistant.NewJobs(assistant.NewStore(s.cfg.StateDir)).Of(inst.ID).Save(assistant.Job{Terminal: "t-fresh", Project: "fresh", CoderID: "claude", DoneWhen: "x", State: assistant.JobSteering, CreatedAt: time.Now().UTC()})
	html = raw(http.MethodGet, assistantProjectViewPath+"?assistant="+inst.ID+"&project=fresh&section=changes", "", "").Body.String()
	if !strings.Contains(html, "data-assistant-changes-no-commit>") || strings.Contains(html, "data-assistant-changed-files") || strings.Contains(html, "data-assistant-changes-error") {
		t.Fatalf("a repository without a commit reads:\n%s", html)
	}
}

// The rows of Changes past the first ten, files and commits, stand behind a
// fold that waits for them, and a view pulled with a list unfolded renders
// them in its open fold.
func TestChangesUnfoldTheRestOfAList(t *testing.T) {
	root := t.TempDir()
	dir := worktreeSourceRepo(t, root, "many")
	worktreeGit(t, dir, "switch", "-q", "feature")
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("f%02d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("x\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		worktreeGit(t, dir, "add", "-A")
		worktreeGit(t, dir, "commit", "-qm", "Add "+name)
	}
	s, raw := ownerPages(t, worktreeProjects(t, root))
	inst, err := s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	assistant.NewJobs(assistant.NewStore(s.cfg.StateDir)).Of(inst.ID).Save(assistant.Job{Terminal: "t-many", Project: "many", CoderID: "claude", DoneWhen: "x", State: assistant.JobSteering, CreatedAt: time.Now().UTC()})
	view := func(more ...string) string {
		t.Helper()
		query := url.Values{"assistant": {inst.ID}, "project": {"many"}, "section": {"changes"}, "more": more}
		rec := raw(http.MethodGet, assistantProjectViewPath+"?"+query.Encode(), "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("the view answered %d: %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	folded := view()
	for _, want := range []string{
		`aria-expanded="false" aria-controls="assistant-project-`,
		`data-assistant-changed-more="files"><span data-fold-shut>Show 2 more files</span><span data-fold-open>Show fewer files</span></button>`,
		`data-assistant-changes-older><span data-fold-shut>Show 2 older commits</span><span data-fold-open>Show fewer commits</span></button>`,
	} {
		if !strings.Contains(folded, want) {
			t.Fatalf("the folded view lacks %s:\n%s", want, folded)
		}
	}
	if strings.Count(folded, `data-assistant-changed-file="`) != 10 || strings.Count(folded, `data-assistant-changes-commit="`) != 10 || strings.Count(folded, "data-assistant-rest-wait") != 2 || strings.Contains(folded, `data-assistant-changed-file="f01.txt"`) {
		t.Fatalf("the folded view does not show the first ten of each list:\n%s", folded)
	}
	unfolded := view(render.ChangesFiles, render.ChangesCommits)
	if strings.Count(unfolded, `data-assistant-changed-file="`) != 12 || strings.Count(unfolded, `data-assistant-changes-commit="`) != 12 || strings.Contains(unfolded, "data-assistant-rest-wait") || strings.Count(unfolded, `<div class="collapse show" id="assistant-project-`) != 2 || strings.Count(unfolded, `aria-expanded="true"`) != 2 {
		t.Fatalf("the unfolded view does not render the rest open:\n%s", unfolded)
	}
	if files := view(render.ChangesFiles); strings.Count(files, `data-assistant-changed-file="`) != 12 || strings.Count(files, `data-assistant-changes-commit="`) != 10 {
		t.Fatalf("unfolding the files unfolds the commits:\n%s", files)
	}
}

// Changes shows on top what the working copy holds uncommitted against HEAD,
// over the pair's list, which holds the same file again: each file opening
// the commit view on its diff against HEAD and answering that diff, the rest
// past the first ten behind its own fold. A file the list does not show and
// a list Changes has not are refused, and once everything is committed the
// section is gone.
func TestChangesShowWhatIsUncommittedOnTop(t *testing.T) {
	root := t.TempDir()
	dir := worktreeSourceRepo(t, root, "app")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	worktreeGit(t, dir, "switch", "-q", "feature")
	write("a.txt", "a\nb\n")
	write("b.txt", "b\n")
	write("gone.txt", "g\n")
	worktreeGit(t, dir, "add", "-A")
	worktreeGit(t, dir, "commit", "-qm", "Feature")
	write("a.txt", "a\nb\nc\n")
	write("new.txt", "n\n")
	write("staged.txt", "s\n")
	worktreeGit(t, dir, "add", "staged.txt")
	worktreeGit(t, dir, "rm", "-q", "gone.txt")
	s, raw := ownerPages(t, worktreeProjects(t, root))
	inst, err := s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	assistant.NewJobs(assistant.NewStore(s.cfg.StateDir)).Of(inst.ID).Save(assistant.Job{Terminal: "t-app", Project: "app", CoderID: "claude", DoneWhen: "x", State: assistant.JobSteering, CreatedAt: time.Now().UTC()})
	view := func(more ...string) string {
		t.Helper()
		query := url.Values{"assistant": {inst.ID}, "project": {"app"}, "section": {"changes"}, "more": more}
		rec := raw(http.MethodGet, assistantProjectViewPath+"?"+query.Encode(), "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("the view answered %d: %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	html := view()
	top, pair, ok := strings.Cut(html, "data-assistant-changes-base>")
	if !ok || !strings.Contains(top, "data-assistant-uncommitted>") {
		t.Fatalf("Uncommitted does not stand over the pair:\n%s", html)
	}
	for _, want := range []string{
		`data-assistant-uncommitted-count>4</span>`,
		`<div data-assistant-changed-file="a.txt">`,
		`<span class="text-green">+1</span> <span class="text-red">−0</span>`,
		`href="/projects/app/editor?file=a.txt&amp;view=commit" data-file-link data-assistant-changed-open>`,
		`data-assistant-diff data-list="uncommitted" data-path="a.txt">`,
		`title="Deleted" data-assistant-changed-mark>D</span>`,
		`data-assistant-diff-note>` + render.DiffNoteDeleted + `</div>`,
		`title="Added" data-assistant-changed-mark>A</span>`,
		`title="Untracked" data-assistant-changed-mark>U</span>`,
	} {
		if !strings.Contains(top, want) {
			t.Fatalf("Uncommitted lacks %s:\n%s", want, top)
		}
	}
	for _, never := range []string{`data-assistant-changed-file="b.txt"`, "view=compare"} {
		if strings.Contains(top, never) {
			t.Fatalf("Uncommitted carries %s:\n%s", never, top)
		}
	}
	for _, want := range []string{`href="/projects/app/editor?file=a.txt&amp;view=compare" data-file-link data-assistant-changed-open>`, `data-assistant-changed-file="b.txt"`, `<span class="text-green">+2</span> <span class="text-red">−0</span>`} {
		if !strings.Contains(pair, want) {
			t.Fatalf("the pair's list lacks %s:\n%s", want, pair)
		}
	}
	if strings.Contains(pair, "view=commit") || !strings.Contains(html, `id="assistant-diff-files-`) || !strings.Contains(html, `id="assistant-diff-uncommitted-`) {
		t.Fatalf("the two lists share their rows' folds or links:\n%s", html)
	}

	diff := func(list, path string) (int, projectDiff) {
		t.Helper()
		rec := raw(http.MethodGet, assistantProjectDiffPath+"?"+url.Values{"assistant": {inst.ID}, "project": {"app"}, "list": {list}, "path": {path}}.Encode(), "", "")
		var got projectDiff
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("%s answered %s", path, rec.Body.String())
			}
		}
		return rec.Code, got
	}
	for c, want := range map[[2]string]projectDiff{
		{render.ChangesUncommitted, "a.txt"}:      {Original: "a\nb\n", Content: "a\nb\nc\n"},
		{render.ChangesFiles, "a.txt"}:            {Original: "a\n", Content: "a\nb\nc\n"},
		{render.ChangesUncommitted, "new.txt"}:    {Content: "n\n"},
		{render.ChangesUncommitted, "staged.txt"}: {Content: "s\n"},
		{render.ChangesUncommitted, "gone.txt"}:   {Note: render.DiffNoteDeleted},
	} {
		if code, got := diff(c[0], c[1]); code != http.StatusOK || got != want {
			t.Fatalf("%s of %s answered %d %+v, want %+v", c[1], c[0], code, got, want)
		}
	}
	for what, c := range map[string][2]string{
		"a committed file":         {render.ChangesUncommitted, "b.txt"},
		"no list":                  {"", "a.txt"},
		"a list of no files":       {render.ChangesCommits, "a.txt"},
		"a list Changes never has": {"tree", "a.txt"},
	} {
		if code, _ := diff(c[0], c[1]); code != http.StatusNotFound {
			t.Fatalf("%s answered %d", what, code)
		}
	}

	for i := 1; i <= 12; i++ {
		write(fmt.Sprintf("u%02d.txt", i), strings.Repeat("u\n", i))
	}
	folded := view()
	top, _, _ = strings.Cut(folded, "data-assistant-changes-base>")
	if !strings.Contains(top, `data-assistant-uncommitted-count>16</span>`) || !strings.Contains(top, `data-assistant-rest="uncommitted">`) || !strings.Contains(top, `data-assistant-changed-more="uncommitted"><span data-fold-shut>Show 6 more files</span>`) || strings.Count(top, `data-assistant-changed-file="`) != 10 {
		t.Fatalf("Uncommitted does not fold its rest:\n%s", top)
	}
	unfolded := view(render.ChangesUncommitted)
	top, pair, _ = strings.Cut(unfolded, "data-assistant-changes-base>")
	if strings.Count(top, `data-assistant-changed-file="`) != 16 || strings.Contains(top, "data-assistant-rest-wait") || !strings.Contains(pair, "data-assistant-rest-wait") {
		t.Fatalf("unfolding Uncommitted does not render its rest alone:\n%s", unfolded)
	}

	worktreeGit(t, dir, "add", "-A")
	worktreeGit(t, dir, "commit", "-qm", "All")
	if html := view(); strings.Contains(html, "data-assistant-uncommitted") || !strings.Contains(html, `data-assistant-changes-files>16</div>`) {
		t.Fatalf("a clean working copy shows Uncommitted:\n%s", html)
	}
}

// A changed file of Changes answers its diff, the base against the disk: a
// file the branch and the working copy changed, one untracked against
// nothing. A binary file, a deleted one, one with a side of more lines than
// the editor's diff limit and one with a side of more KiB answer the line it
// shows instead, naming the limit, the view rendering the lines it knows. A
// limit of zero is off. A path the list does not show, a project no job names
// and a project whose files the view does not list, one below the root of
// another repository, are refused.
func TestAChangedFileAnswersItsDiff(t *testing.T) {
	root := t.TempDir()
	dir := worktreeSourceRepo(t, root, "app")
	worktreeSourceRepo(t, root, "elsewhere")
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	large := strings.Repeat("a line that fills the file\n", 8<<10/27+1)
	write("app/gone.txt", "g\n")
	write("app/large.txt", large)
	worktreeGit(t, dir, "add", "-A")
	worktreeGit(t, dir, "commit", "-qm", "more")
	worktreeGit(t, dir, "branch", "-f", "feature")
	worktreeGit(t, dir, "switch", "-q", "feature")
	write("app/a.txt", "a\nb\n")
	write("app/bin.dat", "x\x00y")
	write("app/many.txt", strings.Repeat("m\n", 100))
	worktreeGit(t, dir, "rm", "-q", "gone.txt")
	worktreeGit(t, dir, "add", "-A")
	worktreeGit(t, dir, "commit", "-qm", "Feature")
	write("app/a.txt", "a\nb\nc\n")
	write("app/new.txt", "n\n")
	write("app/large.txt", large+"one more\n")
	write("nested/n.txt", "n\n")
	if err := os.Symlink("/etc/hostname", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "master"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}, {"config", "commit.gpgsign", "false"}, {"add", "nested/n.txt"}, {"commit", "-qm", "root"}} {
		worktreeGit(t, root, args...)
	}
	write("nested/n.txt", "n\nm\n")
	s, raw := ownerPages(t, worktreeProjects(t, root))
	s.settings.Set(editorDiffMaxLinesKey, "100")
	s.settings.Set(editorDiffMaxKiBKey, "8")
	inst, err := s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app", "nested"} {
		assistant.NewJobs(assistant.NewStore(s.cfg.StateDir)).Of(inst.ID).Save(assistant.Job{Terminal: "t-" + name, Project: name, CoderID: "claude", DoneWhen: "x", State: assistant.JobSteering, CreatedAt: time.Now().UTC()})
	}
	diff := func(project, path string) (int, projectDiff) {
		t.Helper()
		rec := raw(http.MethodGet, assistantProjectDiffPath+"?"+url.Values{"assistant": {inst.ID}, "project": {project}, "list": {render.ChangesFiles}, "path": {path}}.Encode(), "", "")
		var got projectDiff
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("%s answered %s", path, rec.Body.String())
			}
		}
		return rec.Code, got
	}
	for path, want := range map[string]projectDiff{
		"a.txt":     {Original: "a\n", Content: "a\nb\nc\n"},
		"new.txt":   {Content: "n\n"},
		"large.txt": {Note: "Larger than 8 KiB, not shown here."},
		"gone.txt":  {Note: render.DiffNoteDeleted},
		"bin.dat":   {Note: render.DiffNoteBinary},
		"many.txt":  {Note: "More than 100 lines, not shown here."},
		"link":      {Note: render.DiffNoteIrregular},
	} {
		if code, got := diff("app", path); code != http.StatusOK || got != want {
			t.Fatalf("%s answered %d %+v, want %+v", path, code, got, want)
		}
	}
	s.settings.Set(editorDiffMaxLinesKey, "0")
	s.settings.Set(editorDiffMaxKiBKey, "0")
	for path, want := range map[string]string{"many.txt": strings.Repeat("m\n", 100), "large.txt": large + "one more\n"} {
		if code, got := diff("app", path); code != http.StatusOK || got.Note != "" || got.Content != want {
			t.Fatalf("%s answered %d %q without a limit", path, code, got.Note)
		}
	}
	for what, c := range map[string][2]string{
		"a path the list does not show":      {"app", "nope.txt"},
		"a path no list holds":               {"app", "../elsewhere/a.txt"},
		"a project no job names":             {"elsewhere", "a.txt"},
		"a project below another repository": {"nested", "n.txt"},
	} {
		if code, _ := diff(c[0], c[1]); code != http.StatusNotFound {
			t.Fatalf("%s answered %d", what, code)
		}
	}
	html := raw(http.MethodGet, assistantProjectViewPath+"?assistant="+inst.ID+"&project=app&section=changes", "", "").Body.String()
	for _, want := range []string{
		`data-assistant-diff data-list="files" data-path="a.txt">`,
		`data-assistant-diff data-list="files" data-path="large.txt">`,
		`data-assistant-diff data-list="files" data-path="many.txt">`,
		`data-assistant-diff-note>` + render.DiffNoteDeleted + `</div>`,
		`data-assistant-diff-note>` + render.DiffNoteBinary + `</div>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("the view lacks %s:\n%s", want, html)
		}
	}
	if html := raw(http.MethodGet, assistantProjectViewPath+"?assistant="+inst.ID+"&project=nested&section=changes", "", "").Body.String(); strings.Contains(html, "data-assistant-changed-file") || !strings.Contains(html, "data-assistant-project-norepo") {
		t.Fatalf("the view of a project below another repository lists files:\n%s", html)
	}
}

// A guest of an assistant's link reads a project of its jobs read only: the
// threads of the forge and the local ones with their notes, a file leading
// into the share where the link grants the project and nowhere else, and
// nothing that writes, no thread id of the forge and no hand off. A project of
// another assistant is the plain 404.
func TestAGuestReadsAProjectViewReadOnly(t *testing.T) {
	sa := newSharedAssistant(t)
	jobs := assistant.NewJobs(assistant.NewStore(sa.s.cfg.StateDir))
	now := time.Now().UTC()
	for _, job := range []assistant.Job{
		{Terminal: "coder-shop", Project: "shop", State: assistant.JobSteering, CreatedAt: now},
		{Terminal: "coder-blog", Project: "blog", State: assistant.JobDone, CreatedAt: now},
	} {
		job.CoderID, job.DoneWhen = "claude", "x"
		jobs.Of(sa.id).Save(job)
	}
	jobs.Of(sa.other).Save(assistant.Job{Terminal: "coder-other", Project: "other", CoderID: "claude", DoneWhen: "x", State: assistant.JobSteering, CreatedAt: now})
	root := sa.s.projects.Root
	thread := forgeThreadAt("d1", "x", false, now)
	thread.At.Path = "README.md"
	sa.s.lineComments.SyncForge("shop", root+"/shop", []linecomment.ForgeThread{thread}, true)
	thread.At.Path = "notes.md"
	sa.s.lineComments.SyncForge("blog", root+"/blog", []linecomment.ForgeThread{thread}, true)
	mine, _, err := sa.s.lineComments.Save("shop", linecomment.Comment{Path: "README.md", Line: 1, Text: "local note"}, linecomment.Writer{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sa.s.lineComments.Save("blog", linecomment.Comment{Path: "notes.md", Line: 1, Text: "done"}, linecomment.Writer{}); err != nil {
		t.Fatal(err)
	}
	for _, c := range sa.s.lineComments.List("blog") {
		if c.Forge == nil {
			if _, _, err := sa.s.lineComments.Resolve("blog", c.ID, true, linecomment.Writer{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	base := sa.link(t, []string{"shop"}, sa.id)

	page := sa.guest(base + "/assistants/" + sa.id).Body.String()
	if want := `view-url="` + base + assistantProjectViewPath + `?assistant=` + sa.id + `"`; !strings.Contains(page, want) {
		t.Fatalf("the guest's aside lacks %s", want)
	}
	read := func(project string) string {
		t.Helper()
		rec := sa.guest(base + assistantProjectViewPath + "?assistant=" + sa.id + "&project=" + project)
		if rec.Code != http.StatusOK {
			t.Fatalf("the guest's view of %s answered %d", project, rec.Code)
		}
		return rec.Body.String()
	}
	shop, blog := read("shop"), read("blog")
	for _, want := range []string{`href="` + base + `/projects/shop/editor?file=README.md&amp;line=1" title="README.md:1" data-file-link`, `data-comment-note-author>root</span>`, `data-comment-note-text>Why d1?</div>`, `<div class="card" data-assistant-thread="` + mine.ID + `" data-assistant-thread-local data-assistant-thread-path="README.md" data-assistant-thread-line="1" data-assistant-thread-mark="`, `data-comment-note-author>Owner</span>`, `data-comment-note-text>local note</div>`} {
		if !strings.Contains(shop, want) {
			t.Fatalf("the guest's view lacks %s:\n%s", want, shop)
		}
	}
	if !strings.Contains(blog, `<span class="text-truncate min-w-0 font-monospace" title="notes.md:1" data-assistant-thread-file>notes.md:1</span>`) || !strings.Contains(blog, `data-assistant-thread-origin="local"`) || strings.Contains(blog, "/projects/blog/editor") {
		t.Fatalf("a project the link does not grant leads somewhere:\n%s", blog)
	}
	for _, never := range []string{"data-thread=", "data-assistant-thread-handoff", "data-assistant-thread-reply", "data-assistant-thread-resolve", "data-assistant-thread-reopen", "data-comments-url", "data-comment-note-edit", "data-comment-note-drop", "data-assistant-thread-delete", "data-assistant-project-publish", "data-assistant-diff-comment", "data-assistant-project-comment", "data-project-comment", `href="/projects`, "<form", "csrf_token"} {
		if strings.Contains(shop+blog, never) {
			t.Fatalf("the guest's view carries %s:\n%s", never, shop+blog)
		}
	}
	sa.notFound(t, "a project of another assistant", base+assistantProjectViewPath+"?assistant="+sa.id+"&project=other")
	sa.notFound(t, "another assistant's view", base+assistantProjectViewPath+"?assistant="+sa.other+"&project=other")
}

// A guest reads the diff of a file the view lists, on a project the link does
// not grant too, and nothing else: a file the view does not list, a project
// no job of the link's assistant names and another assistant are the plain
// 404.
func TestAGuestReadsTheDiffOfAListedFile(t *testing.T) {
	sa := newSharedAssistant(t)
	jobs := assistant.NewJobs(assistant.NewStore(sa.s.cfg.StateDir))
	now := time.Now().UTC()
	jobs.Of(sa.id).Save(assistant.Job{Terminal: "coder-blog", Project: "blog", CoderID: "claude", DoneWhen: "x", State: assistant.JobDone, CreatedAt: now})
	jobs.Of(sa.other).Save(assistant.Job{Terminal: "coder-shop", Project: "shop", CoderID: "claude", DoneWhen: "x", State: assistant.JobSteering, CreatedAt: now})
	blog := filepath.Join(sa.s.projects.Root, "blog")
	for name, content := range map[string]string{".gitignore": "secret.txt\n", "secret.txt": "secret\n"} {
		if err := os.WriteFile(filepath.Join(blog, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "master"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}, {"config", "commit.gpgsign", "false"}, {"add", "-A"}, {"commit", "-qm", "init"}} {
		worktreeGit(t, blog, args...)
	}
	if err := os.WriteFile(filepath.Join(blog, "notes.md"), []byte("x\ny\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := sa.link(t, []string{"shop"}, sa.id)
	if page := sa.guest(base + "/assistants/" + sa.id).Body.String(); !strings.Contains(page, `diff-url="`+base+assistantProjectDiffPath+`?assistant=`+sa.id+`"`) {
		t.Fatalf("the guest's aside lacks its diff path:\n%s", page)
	}
	ask := func(assistant, project, list, path string) string {
		return base + assistantProjectDiffPath + "?" + url.Values{"assistant": {assistant}, "project": {project}, "list": {list}, "path": {path}}.Encode()
	}
	for _, list := range []string{render.ChangesFiles, render.ChangesUncommitted} {
		rec := sa.guest(ask(sa.id, "blog", list, "notes.md"))
		var got projectDiff
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got != (projectDiff{Original: "x\n", Content: "x\ny\n"}) {
			t.Fatalf("the guest's diff of the list %s answered %d %s", list, rec.Code, rec.Body.String())
		}
	}
	view := sa.guest(base + assistantProjectViewPath + "?assistant=" + sa.id + "&project=blog&section=changes").Body.String()
	if !strings.Contains(view, `data-assistant-diff data-list="uncommitted" data-path="notes.md">`) || strings.Contains(view, `href="/projects`) || strings.Contains(view, "data-assistant-changed-open") {
		t.Fatalf("the guest's uncommitted files lead off the link:\n%s", view)
	}
	sa.notFound(t, "a file the view does not list", ask(sa.id, "blog", render.ChangesUncommitted, "secret.txt"))
	sa.notFound(t, "a list the view does not show", ask(sa.id, "blog", "commits", "notes.md"))
	sa.notFound(t, "a project no job of the assistant names", ask(sa.id, "shop", render.ChangesFiles, "README.md"))
	sa.notFound(t, "another assistant's project", ask(sa.other, "shop", render.ChangesUncommitted, "README.md"))
}

// The notes of a thread are one rendering the editor's dialog pulls: each
// note by who wrote it in the reader's words with the edit and the delete of
// what the reader may change, a thread of the forge by its authors there and
// with neither. A thread nobody holds is a 404.
func TestTheThreadNotesNameTheirWritersForEachReader(t *testing.T) {
	f := newCommentGuests(t)
	f.r.GET("/projects/:name/editor/comments/thread", f.s.handleEditorCommentThread)
	if rec := f.send(f.anna, "/projects/shop/editor/comments/reply", map[string]string{"thread": f.owner.ID, "text": "anna's reply"}); rec.Code != http.StatusOK {
		t.Fatalf("Anna's reply answered %d %s", rec.Code, rec.Body.String())
	}
	notes := func(token, id string) (int, string) {
		target := "/projects/shop/editor/comments/thread?id=" + id
		if token != "" {
			target = sharePathPrefix + token + target
		}
		rec := httptest.NewRecorder()
		f.r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec.Code, rec.Body.String()
	}
	shape := func(html string) string {
		var out []string
		for _, note := range strings.Split(html, "data-comment-note=")[1:] {
			author := note[strings.Index(note, "data-comment-note-author>")+len("data-comment-note-author>"):]
			line := author[:strings.Index(author, "</span>")]
			if strings.Contains(note, "data-comment-note-edit") {
				line += " +edit"
			}
			if strings.Contains(note, "data-comment-note-drop") {
				line += " +drop"
			}
			out = append(out, line)
		}
		return strings.Join(out, " | ")
	}
	for _, c := range []struct{ who, token, want string }{
		{"the owner", "", "You +edit | Anna +edit +drop"},
		{"Anna", f.anna, "Owner | You +edit +drop"},
		{"Bert", f.bert, "Owner | Anna"},
	} {
		code, html := notes(c.token, f.owner.ID)
		if code != http.StatusOK || shape(html) != c.want {
			t.Fatalf("%s reads %d %q, want %q:\n%s", c.who, code, shape(html), c.want, html)
		}
	}
	if code, html := notes("", f.owner.ID); !strings.Contains(html, `title="Edit comment" aria-label="Edit comment"`) || !strings.Contains(html, `title="Delete reply" aria-label="Delete reply"`) || !strings.Contains(html, `<dc-time class="text-nowrap" datetime="`) || strings.Contains(html, "me-n2") || code != http.StatusOK {
		t.Fatalf("the owner's notes lack their labels or times, or their buttons leave the dialog's inset:\n%s", html)
	}
	thread := forgeThreadAt("d1", "one", false, time.Now())
	f.s.lineComments.SyncForge("shop", f.s.projects.Root+"/shop", []linecomment.ForgeThread{thread}, true)
	var forgeID string
	for _, c := range f.s.lineComments.List("shop") {
		if c.Forge != nil {
			forgeID = c.ID
		}
	}
	if code, html := notes("", forgeID); code != http.StatusOK || shape(html) != "root | gitlab" {
		t.Fatalf("the forge's thread reads %d %q:\n%s", code, shape(html), html)
	}
	if code, _ := notes("", "nobody"); code != http.StatusNotFound {
		t.Fatalf("a thread nobody holds answered %d", code)
	}
}

// A line that would break shows its breaks as escapes, the way the editor's
// visibleLine shows them.
func TestVisibleLineEscapesWhatBreaksALine(t *testing.T) {
	got := ""
	for _, part := range render.VisibleLine("a\tb\u2028c\x01\u0085") {
		if part.Escape {
			got += "[" + part.Text + "]"
		} else {
			got += part.Text
		}
	}
	if want := `a[\t]b[\u2028]c[\x01][\x85]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// A side over the editor's diff limits shows the line naming the limit it
// hit, lines counted like the editor's countLines, a limit of zero off, a
// side larger than the editor reads at all over a limit of that size.
func TestTheDiffLimitsAreTheEditors(t *testing.T) {
	set := editorSettings{DiffMaxLines: 3, DiffMaxKiB: 1}
	for _, c := range []struct {
		set  editorSettings
		text string
		size int
		want string
	}{
		{set, "a\nb\nc", 0, ""},
		{set, "a\nb\nc\n", 0, render.DiffNoteLines(3)},
		{set, "", 1 << 10, ""},
		{set, "", 1<<10 + 1, render.DiffNoteBytes(1)},
		{editorSettings{}, strings.Repeat("a\n", 1000), 0, ""},
		{editorSettings{}, "", filesystem.MaxEditableBytes + 1, render.DiffNoteBytes(filesystem.MaxEditableBytes >> 10)},
	} {
		size := c.size
		if size == 0 {
			size = len(c.text)
		}
		if got := overDiffLimits(c.set, size, textLines([]byte(c.text))); got != c.want {
			t.Fatalf("%d bytes, %q under %+v answered %q, want %q", size, c.text, c.set, got, c.want)
		}
	}
}
