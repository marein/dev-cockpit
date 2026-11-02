package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

// The threads of a review on a line stand as line comment threads: each at
// its line quoting that line of the commit the forge names, read from the
// repository, without a quote when the repository lacks the commit, its first
// note the comment's and the others its replies with author and time, with
// the forge's name, icon and page, resolved with the thread there; a thread
// on a whole file stands on no line, a general one is a project comment, one
// the forge keeps alone without a thread's id and marked unresolvable. A guest
// reads them like every comment, without the id of the thread the owner
// answers and resolves by, and nobody edits or deletes them here, the text
// names the forge and the author of each note. The next read drops a deleted note and resolves a
// thread, and a branch without a change request takes them all, so does the
// poller no longer keeping the project, which leaves the owner's own threads.
func TestTheThreadsOfAReviewStandAsLineComments(t *testing.T) {
	f := newCommentGuests(t)
	p, err := f.s.projects.FindByName("shop")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.Path, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Path, "src", "login.go"), []byte("package login\nfunc a() {}\nb := 2\nname := b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "feature"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}, {"config", "commit.gpgsign", "false"}, {"add", "-A"}, {"commit", "-qm", "init"}} {
		worktreeGit(t, p.Path, args...)
	}
	out, err := exec.Command("git", "-C", p.Path, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(out))
	f.s.SetForge(forge.New(t.TempDir(), []forge.Provider{github.Provider{}, gitlab.Provider{}}, github.Builtin(), gitlab.Builtin()))
	page := "https://gitlab.com/team/shop/-/merge_requests/4#note_"
	at := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	note := func(id, author, body string) forge.Note {
		at = at.Add(time.Minute)
		return forge.Note{ID: id, Author: author, Body: body, Created: at, URL: page + id}
	}
	read := forge.ReviewRead{
		Watch:  forge.Watch{Project: "shop", Binding: forge.Binding{Host: gitlab.Builtin(), Path: "team/shop"}, Branch: "feature", Remote: "feature", Head: head},
		Change: &forge.Change{Number: 4},
		Review: forge.Review{
			Diff: []forge.Thread{
				{ID: "d1", Path: "src/login.go", Line: 4, Side: forge.NewSide, Commit: head, Resolvable: true, Notes: []forge.Note{note("29", "root", "New name?"), note("33", "alice", "Yes.")}},
				{ID: "d2", Path: "src/login.go", Line: 3, Side: forge.OldSide, Commit: "4b449edfa7d092dd25836adba8055a2e4223fbe8", Resolvable: true, Resolved: true, Notes: []forge.Note{note("31", "root", "Why drop c?")}},
				{ID: "d3", Path: "README.md", Resolvable: true, Notes: []forge.Note{note("37", "root", "File note")}},
			},
			General: []forge.Thread{
				{ID: "d4", Resolvable: true, Notes: []forge.Note{note("34", "root", "General thread")}},
				{ID: "IC_5", Single: true, Notes: []forge.Note{note("36", "bob", "Looks good")}},
			},
		},
	}
	f.s.ForgeReview(read)
	byThread := func(views []lineCommentView) map[string]lineCommentView {
		out := map[string]lineCommentView{}
		for _, v := range views {
			if v.Forge != nil {
				out[strings.TrimPrefix(v.Forge.URL, page)] = v
			}
		}
		return out
	}
	_, views := f.read(t, f.bert)
	got := byThread(views)
	if len(views) != 5 || len(got) != 4 || !views[0].Project || !views[1].Project {
		t.Fatalf("the guest reads %+v", views)
	}
	general, alone := got["34"], got["36"]
	if !general.Project || general.Path != "" || general.Line != 0 || general.Outdated || general.Text != "General thread" || *general.Forge != (forgeThreadView{Kind: "gitlab", Label: "GitLab", Icon: "ti-brand-gitlab", URL: page + "34"}) {
		t.Fatalf("the general thread reads %+v %+v", general, general.Forge)
	}
	if !alone.Project || *alone.Forge != (forgeThreadView{Kind: "gitlab", Label: "GitLab", Icon: "ti-brand-gitlab", URL: page + "36", Unresolvable: true}) {
		t.Fatalf("the note the forge keeps alone reads %+v %+v", alone, alone.Forge)
	}
	first := got["29"]
	if first.Path != "src/login.go" || first.Line != 4 || first.LineText != "name := b" || first.Outdated || first.Author != "root" || first.Text != "New name?" || first.Mine || first.Resolved || !first.UpdatedAt.Equal(time.Date(2026, 10, 30, 9, 1, 0, 0, time.UTC)) {
		t.Fatalf("the first thread reads %+v", first)
	}
	if *first.Forge != (forgeThreadView{Kind: "gitlab", Label: "GitLab", Icon: "ti-brand-gitlab", URL: page + "29"}) {
		t.Fatalf("its forge reads %+v", *first.Forge)
	}
	if len(first.Replies) != 1 || first.Replies[0] != (lineReplyView{ID: "33", Text: "Yes.", Author: "alice", UpdatedAt: time.Date(2026, 10, 30, 9, 2, 0, 0, time.UTC)}) {
		t.Fatalf("its replies read %+v", first.Replies)
	}
	f.r.GET("/projects/:name/editor/comments", f.s.handleEditorComments)
	owner := httptest.NewRecorder()
	f.r.ServeHTTP(owner, httptest.NewRequest(http.MethodGet, "/projects/shop/editor/comments", nil))
	var owned struct {
		Comments []lineCommentView `json:"comments"`
	}
	if err := json.Unmarshal(owner.Body.Bytes(), &owned); err != nil || byThread(owned.Comments)["29"].Forge.Thread != "d1" || byThread(owned.Comments)["34"].Forge.Thread != "d4" || byThread(owned.Comments)["36"].Forge.Thread != "" {
		t.Fatalf("the owner reads %d %s", owner.Code, owner.Body.String())
	}
	if old := got["31"]; old.Line != 3 || old.LineText != "" || !old.Outdated || !old.Resolved {
		t.Fatalf("the thread on a commit the repository lacks reads %+v %+v", old, old.Forge)
	}

	if rec := f.send("", shopComments, map[string]any{"id": first.ID, "path": "src/login.go", "line": 4, "text": "rewritten"}); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "changes there, not here") {
		t.Fatalf("the owner's edit of a thread of the forge = %d %s", rec.Code, rec.Body.String())
	}
	for _, w := range []string{"", f.anna} {
		if rec := f.send(w, shopComments+"/reply", map[string]any{"thread": first.ID, "text": "here"}); rec.Code != http.StatusForbidden {
			t.Fatalf("a reply here to a thread of the forge = %d %s", rec.Code, rec.Body.String())
		}
	}
	f.send(f.anna, shopComments+"/delete", map[string]any{"ids": []string{first.ID}})
	if rec := f.send("", shopComments+"/delete", map[string]any{"all": true}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"removed":1`) {
		t.Fatalf("the owner's delete of all = %d %s", rec.Code, rec.Body.String())
	}
	if _, views := f.read(t, f.bert); len(views) != 4 {
		t.Fatalf("after the deletes the guest reads %+v", views)
	}
	text := httptest.NewRecorder()
	f.r.ServeHTTP(text, httptest.NewRequest(http.MethodGet, sharePathPrefix+f.bert+"/projects/shop/editor/comments/text", nil))
	for _, want := range []string{"- GitLab root: Project comment\n  > General thread\n- GitLab bob: Project comment\n  > Looks good\n- GitLab root: src/login.go:3", "- GitLab root: src/login.go:4, code: name := b\n  > New name?\n  - Reply of GitLab alice:\n    > Yes.", "- GitLab root: src/login.go:3 (outdated) (resolved)\n  > Why drop c?"} {
		if !strings.Contains(text.Body.String(), want) {
			t.Fatalf("the text lacks %q:\n%s", want, text.Body.String())
		}
	}

	read.Review.Diff = read.Review.Diff[:1]
	read.Review.Diff[0].Resolved = true
	read.Review.Diff[0].Notes = read.Review.Diff[0].Notes[:1]
	f.s.ForgeReview(read)
	_, views = f.read(t, f.bert)
	if got := byThread(views); len(views) != 3 || got["29"].ID != first.ID || !got["29"].Resolved || len(got["29"].Replies) != 0 || got["34"].ID != general.ID {
		t.Fatalf("after the next read the guest reads %+v", views)
	}
	f.s.ForgeReview(forge.ReviewRead{Watch: read.Watch})
	if _, views := f.read(t, f.bert); len(views) != 0 {
		t.Fatalf("a branch without a change request leaves %+v", views)
	}

	f.s.ForgeReview(read)
	if rec := f.send("", shopComments, map[string]any{"path": "src/login.go", "line": 2, "text": "mine"}); rec.Code != http.StatusOK {
		t.Fatalf("the owner's own note = %d %s", rec.Code, rec.Body.String())
	}
	f.s.ForgeKept(map[string]bool{"shop": true})
	if _, views := f.read(t, f.bert); len(views) != 4 {
		t.Fatalf("while the poller keeps the project the guest reads %+v", views)
	}
	f.s.ForgeKept(map[string]bool{"blog": true})
	if _, views := f.read(t, f.bert); len(views) != 1 || views[0].Forge != nil || views[0].Text != "mine" {
		t.Fatalf("once the poller no longer keeps the project the guest reads %+v", views)
	}
}
