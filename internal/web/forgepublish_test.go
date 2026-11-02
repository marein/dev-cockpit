package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
	"github.com/marein/dev-cockpit/internal/linecomment"
	"github.com/marein/dev-cockpit/internal/localapi"
)

const publishLogin = "package login\n\nfunc login() {\n\tname := bb\n\treturn\n}\n"

// publishGitLab answers group/sub/app like GitLab: the open merge request !4
// of feature at head, no pipeline, its latest diff refs and the diff of
// src/login.go, whose first four lines it shows, and a discussion for each
// note posted, the one numbered refuse refused in GitLab's words; with hold
// set a post says so on got and waits for hold to close. It keeps what was
// posted.
type publishGitLab struct {
	mu     sync.Mutex
	head   string
	refuse int
	hold   chan struct{}
	got    chan struct{}
	posted []map[string]any
}

func (g *publishGitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	hold, got := g.hold, g.got
	g.mu.Unlock()
	if hold != nil && r.Method == http.MethodPost {
		got <- struct{}{}
		<-hold
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+forgeStatusToken {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
		return
	}
	project := "/api/v4/projects/group%2Fsub%2Fapp"
	mr := project + "/merge_requests/4"
	switch path := r.URL.EscapedPath(); {
	case path == project+"/merge_requests":
		fmt.Fprintf(w, `[{"iid":4,"title":"Fix the login","state":"opened","draft":false,"web_url":"https://gitlab.example.com/group/sub/app/-/merge_requests/4","target_branch":"master","source_project_id":2,"target_project_id":2,"sha":%q}]`, g.head)
	case path == project+"/pipelines":
		fmt.Fprint(w, `[]`)
	case path == mr:
		fmt.Fprintf(w, `{"iid":4,"diff_refs":{"base_sha":"b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0","start_sha":"b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0","head_sha":%q}}`, g.head)
	case path == mr+"/diffs":
		fmt.Fprint(w, `[{"diff":"@@ -1,4 +1,4 @@\n package login\n \n func login() {\n-\tname := b\n+\tname := bb\n","new_path":"src/login.go","old_path":"src/login.go","deleted_file":false}]`)
	case path == mr+"/discussions" && r.Method == http.MethodPost:
		var sent map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &sent)
		g.posted = append(g.posted, sent)
		if len(g.posted) == g.refuse {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"message":"400 Bad request - Note {:line_code=>[\"can't be blank\"]}"}`)
			return
		}
		id := 300 + len(g.posted)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":"d%d","individual_note":false,"notes":[{"id":%d,"type":"DiffNote","body":%q,"author":{"username":"root"},"created_at":"2026-10-29T18:32:29.871Z","system":false,"resolvable":true,"resolved":false}]}`, id, id, sent["body"])
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 Not found"}`)
	}
}

// asked answers each note posted as its line and body, with its old line
// where it named one.
func (g *publishGitLab) asked() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, 0, len(g.posted))
	for _, sent := range g.posted {
		position, _ := sent["position"].(map[string]any)
		at := fmt.Sprintf("%v:%v", position["new_path"], position["new_line"])
		if old, ok := position["old_line"]; ok {
			at += fmt.Sprintf(" old %v", old)
		}
		out = append(out, fmt.Sprintf("%s at %v: %v", at, position["head_sha"], sent["body"]))
	}
	return out
}

type forgePublishFixture struct {
	*composeFixture
	gitlab *publishGitLab
	repo   string
	pushed string
}

// newForgePublishFixture is the compose fixture with a repository app on
// feature, whose src/login.go is pushed to the fake GitLab with the merge
// request !4 of it.
func newForgePublishFixture(t *testing.T) *forgePublishFixture {
	t.Helper()
	f := &forgePublishFixture{}
	f.composeFixture, f.repo, f.pushed = newForgeLoginFixture(t, func(pushed string) http.Handler {
		f.gitlab = &publishGitLab{head: pushed}
		return f.gitlab
	})
	f.router.POST("/projects/:name/forge/publish", f.s.handleProjectForgePublish)
	return f
}

// newForgeLoginFixture is the compose fixture with line comments and a
// repository app on feature whose src/login.go is pushed to the GitLab that
// serve answers for the pushed head, bound with its token. It answers the
// repository and the pushed head.
func newForgeLoginFixture(t *testing.T, serve func(pushed string) http.Handler) (*composeFixture, string, string) {
	t.Helper()
	f := newComposeFixture(t)
	f.s.lineComments = linecomment.New(t.TempDir())
	repo := worktreeSourceRepo(t, filepath.Dir(f.dir), "app")
	worktreeGit(t, repo, "switch", "-q", "feature")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "login.go"), []byte(publishLogin), 0o644); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, repo, "add", "-A")
	worktreeGit(t, repo, "commit", "-qm", "login")
	out, err := exec.Command("git", "-C", repo, "rev-parse", "feature").Output()
	if err != nil {
		t.Fatal(err)
	}
	pushed := strings.TrimSpace(string(out))
	srv := httptest.NewServer(serve(pushed))
	t.Cleanup(srv.Close)
	worktreeGit(t, repo, "remote", "add", "origin", srv.URL+"/group/sub/app.git")
	worktreeGit(t, repo, "update-ref", "refs/remotes/origin/feature", "feature")
	service := forge.New(t.TempDir(), []forge.Provider{gitlab.Provider{}})
	f.s.SetForge(service)
	addForgeHost(t, service, forge.Input{Kind: "gitlab", WebURL: srv.URL}, forgeStatusToken)
	return f, repo, pushed
}

// comment writes a line comment of the owner, or of the guest of link, on
// src/login.go.
func (f *forgePublishFixture) comment(t *testing.T, line int, quote, text, link string) linecomment.Comment {
	t.Helper()
	c, _, err := f.s.lineComments.Save("app", linecomment.Comment{Path: "src/login.go", Line: line, LineText: quote, Text: text}, linecomment.Writer{Link: link, Name: link})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *forgePublishFixture) publish(t *testing.T, local bool, id string, form url.Values) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/projects/app/forge/publish", strings.NewReader(form.Encode()))
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

// stored is the comment with that id, or the published one that had it.
func (f *forgePublishFixture) stored(t *testing.T, id string) linecomment.Comment {
	t.Helper()
	for _, c := range f.s.lineComments.List("app") {
		if c.ID == id || c.Was == id {
			return c
		}
	}
	t.Fatalf("no comment %s in %+v", id, f.s.lineComments.List("app"))
	return linecomment.Comment{}
}

// A click publishes the comment at its line of the diff, a line the change
// adds without its old line, one both sides share with it, and each one
// published becomes its note of the forge, keeping its line, under the id
// its thread comes back under.
func TestAClickPublishesCommentsAsNotesOfTheForge(t *testing.T) {
	f := newForgePublishFixture(t)
	added := f.comment(t, 4, "\tname := bb", "Why bb?", "")
	code, answer := f.publish(t, false, "", url.Values{"id": {added.ID}})
	if code != http.StatusOK || answer["message"] != "The comment is published on merge request !4." {
		t.Fatalf("the publish answered %d %v", code, answer)
	}
	c := f.stored(t, added.ID)
	want := linecomment.Forge{Kind: "gitlab", Note: "301", URL: "https://gitlab.example.com/group/sub/app/-/merge_requests/4#note_301", At: linecomment.Place{Path: "src/login.go", Line: 4, Quote: "\tname := bb", Quoted: true}}
	if c.Forge == nil || *c.Forge != want || c.Author != "root" || c.Text != "Why bb?" || c.Line != 4 || c.ID == added.ID || c.Was != added.ID {
		t.Fatalf("the published comment reads %+v %+v", c, c.Forge)
	}
	first := f.comment(t, 1, "package login", "Name it auth?", "")
	moved := f.comment(t, 9, "func login() {", "Exported?", "")
	code, answer = f.publish(t, false, "", url.Values{"all": {"1"}})
	if code != http.StatusOK || answer["message"] != "The 2 comments are published on merge request !4." {
		t.Fatalf("the publish of all answered %d %v", code, answer)
	}
	asked := []string{
		"src/login.go:4 at " + f.pushed + ": Why bb?",
		"src/login.go:1 old 1 at " + f.pushed + ": Name it auth?",
		"src/login.go:3 old 3 at " + f.pushed + ": Exported?",
	}
	if got := f.gitlab.asked(); !reflect.DeepEqual(got, asked) {
		t.Fatalf("the forge was asked %q", got)
	}
	if c := f.stored(t, moved.ID); c.Forge == nil || c.Forge.At.Line != 3 || c.Line != 9 || f.stored(t, first.ID).Forge == nil {
		t.Fatalf("the comment that follows its quote reads %+v", c)
	}
	if code, answer := f.publish(t, false, "", url.Values{"all": {"1"}}); code != http.StatusConflict || answer["error"] != `"app" holds no comment of yours to publish.` {
		t.Fatalf("a publish of all with none left answered %d %v", code, answer)
	}
}

// Nothing is posted where one comment of a publish stands outside the diff,
// quotes a line the pushed head does not hold, or is no comment of the
// owner's to publish, nor for a request that names nothing to publish, nor
// of a branch never pushed. A push carries only what the files on disk hold,
// a file that does not read as text cannot tell. Each refusal names a
// comment at its line in the editor.
func TestAPublishIsRefusedBeforeAnythingIsPosted(t *testing.T) {
	f := newForgePublishFixture(t)
	in := f.comment(t, 4, "\tname := bb", "fine", "")
	outside := f.comment(t, 5, "\treturn", "far", "")
	shifted := f.comment(t, 12, "}", "below", "")
	local := f.comment(t, 2, "// not pushed", "local", "")
	outdated := f.comment(t, 1, "package auth", "gone", "")
	if err := os.WriteFile(filepath.Join(f.repo, "src", "login.go"), []byte(strings.Replace(publishLogin, "\n\n", "\n// not pushed\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	guest := f.comment(t, 3, "func login() {", "guest's", "l1")
	f.s.lineComments.SyncForge("app", f.repo, []linecomment.ForgeThread{{Kind: "gitlab", At: linecomment.Place{Path: "src/login.go", Line: 4}, Notes: []linecomment.ForgeNote{{ID: "9", Author: "bob", Text: "theirs"}}}}, true)
	var note string
	for _, c := range f.s.lineComments.List("app") {
		if c.Forge != nil {
			note = c.ID
		}
	}
	for _, c := range []struct {
		form url.Values
		code int
		says string
	}{
		{url.Values{}, http.StatusBadRequest, "Name the comments to publish by their ids, or all of them."},
		{url.Values{"all": {"1"}, "id": {in.ID}}, http.StatusBadRequest, "Name comments by their ids, or all of them, not both."},
		{url.Values{"id": {strings.Repeat("a", 65)}}, http.StatusBadRequest, "A comment's id has 1 to 64 characters."},
		{url.Values{"id": {in.ID, "nobody"}}, http.StatusConflict, `"app" holds no comment "nobody".`},
		{url.Values{"id": {guest.ID}}, http.StatusConflict, `The comment at "src/login.go":3 is a guest's, only your own publish.`},
		{url.Values{"id": {note}}, http.StatusConflict, `The comment at "src/login.go":4 is a review thread on the forge already.`},
		{url.Values{"id": {in.ID, outside.ID}}, http.StatusConflict, `1 of the 2 comments stands on no line the diff of merge request !4 shows, nothing was published: "src/login.go":5.`},
		{url.Values{"id": {shifted.ID}}, http.StatusConflict, `"src/login.go":12 stands on no line the diff of merge request !4 shows, nothing was published.`},
		{url.Values{"id": {local.ID}}, http.StatusConflict, `The line the comment at "src/login.go":2 quotes is not in the pushed head ` + forge.ShortHead(f.pushed) + ` of "feature", push it first.`},
		{url.Values{"id": {outdated.ID}}, http.StatusConflict, `The line the comment at "src/login.go":1 quotes is not in the saved file: save it, or delete the outdated comment or write it again on its line.`},
	} {
		if code, answer := f.publish(t, false, "", c.form); code != c.code || answer["error"] != c.says {
			t.Errorf("%v answered %d %v", c.form, code, answer)
		}
	}
	worktreeGit(t, f.repo, "checkout", "-qb", "local-only")
	if code, answer := f.publish(t, false, "", url.Values{"id": {in.ID}}); code != http.StatusConflict || answer["error"] != `"local-only" is not pushed, it has no merge request to publish comments on.` {
		t.Fatalf("a branch never pushed answered %d %v", code, answer)
	}
	worktreeGit(t, f.repo, "checkout", "-q", "feature")
	saved, err := os.ReadFile(filepath.Join(f.repo, "src", "login.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "src", "login.go"), append(saved, 0), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, answer := f.publish(t, false, "", url.Values{"id": {local.ID}}); code != http.StatusConflict || answer["error"] != `The line the comment at "src/login.go":2 quotes is not in the pushed head `+forge.ShortHead(f.pushed)+` of "feature", and the saved file does not read as text.` {
		t.Fatalf("a saved file that does not read as text answered %d %v", code, answer)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "src", "login.go"), saved, 0o644); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, f.repo, "rm", "-qf", "--cached", "src/login.go")
	worktreeGit(t, f.repo, "commit", "-qm", "gone")
	worktreeGit(t, f.repo, "update-ref", "refs/remotes/origin/feature", "feature")
	if code, answer := f.publish(t, false, "", url.Values{"id": {in.ID}}); code != http.StatusConflict || answer["error"] != `"src/login.go" is not in the pushed head `+forge.ShortHead(strings.TrimSpace(gitOutput(t, f.repo, "rev-parse", "feature")))+` of "feature", push it first.` {
		t.Fatalf("a file the pushed head lacks answered %d %v", code, answer)
	}
	if err := os.Remove(filepath.Join(f.repo, "src", "login.go")); err != nil {
		t.Fatal(err)
	}
	if code, answer := f.publish(t, false, "", url.Values{"id": {in.ID}}); code != http.StatusConflict || answer["error"] != `The line the comment at "src/login.go":4 quotes is not in the saved file: save it, or delete the outdated comment or write it again on its line.` {
		t.Fatalf("a file gone from the pushed head and the disk answered %d %v", code, answer)
	}
	if got := f.gitlab.asked(); len(got) != 0 || f.stored(t, in.ID).Forge != nil {
		t.Fatalf("a refused publish posted %q", got)
	}
}

// A refusal of the forge halfway keeps what went through: the comments
// posted before it are notes of the forge, the rest stays the owner's.
func TestAPublishTheForgeRefusesHalfwayKeepsWhatWentThrough(t *testing.T) {
	f := newForgePublishFixture(t)
	first := f.comment(t, 1, "package login", "one", "")
	second := f.comment(t, 4, "\tname := bb", "two", "")
	f.gitlab.refuse = 2
	code, answer := f.publish(t, false, "", url.Values{"all": {"1"}})
	says := `1 of the 2 comments are published on the merge request !4, the rest is not. GitLab refused to comment on merge request !4: 400 Bad request - Note {:line_code=>["can't be blank"]}.`
	if code != http.StatusBadGateway || answer["error"] != says {
		t.Fatalf("the publish answered %d %v", code, answer)
	}
	if f.stored(t, first.ID).Forge == nil || f.stored(t, second.ID).Forge != nil {
		t.Fatalf("after the refusal the comments read %+v", f.s.lineComments.List("app"))
	}
}

// An assistant's publish waits for the user, who sees each comment at its
// line with its text, the merge request, branch, forge and project. Approved,
// the comments are published and the note links their threads; a comment
// changed meanwhile fails the approval and nothing is published.
func TestAnAssistantsPublishWaitsForTheApproval(t *testing.T) {
	f := newForgePublishFixture(t)
	c := f.comment(t, 4, "\tname := bb", "Why bb?", "")
	code, answer := f.publish(t, true, f.owner, url.Values{"id": {c.ID}})
	what := "Publish a line comment on merge request !4 of feature in app"
	if code != http.StatusOK || answer["pending"] != true || answer["what"] != what {
		t.Fatalf("the publish answered %d %v", code, answer)
	}
	q := f.waitQuestion(t)
	address := f.s.forgeByProject(f.s.projects.List())["app"].Address()
	want := []askpass.Detail{
		{Label: "Action", Value: "Publish line comments"},
		{Label: "Comment", Value: "src/login.go:4 · Why bb?"},
		{Label: "On", Value: "merge request !4 · Fix the login · https://gitlab.example.com/group/sub/app/-/merge_requests/4"},
		{Label: "Branch", Value: "feature at " + forge.ShortHead(f.pushed)},
		{Label: "Forge", Value: "GitLab · " + address},
		{Label: "Project", Value: "app"},
	}
	if q.Action != what || !reflect.DeepEqual(q.Details, want) || len(f.gitlab.asked()) != 0 {
		t.Fatalf("the question reads %+v, posted %q", q, f.gitlab.asked())
	}
	if code, answer := f.publish(t, true, f.owner, url.Values{"all": {"1"}}); code != http.StatusConflict || !strings.Contains(answer["error"].(string), "already waits") {
		t.Fatalf("the same publish again answered %d %v", code, answer)
	}
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	says := "On GitLab " + address + ` the line comment of feature in app is published on the merge request !4 "Fix the login":` + "\n- src/login.go:4 https://gitlab.example.com/group/sub/app/-/merge_requests/4#note_301"
	if note.Note.Verdict != approval.Done || !strings.Contains(note.Content, says) || f.stored(t, c.ID).Forge == nil {
		t.Fatalf("the approval posted %q, note %s", f.gitlab.asked(), note.Content)
	}
	f.waitNoQuestion(t)
	other := f.comment(t, 1, "package login", "Name it auth?", "")
	f.publish(t, true, f.owner, url.Values{"id": {other.ID}})
	q = f.waitQuestion(t)
	if _, _, err := f.s.lineComments.Save("app", linecomment.Comment{ID: other.ID, Path: other.Path, Line: other.Line, LineText: other.LineText, Text: "Rather auth."}, linecomment.Writer{}); err != nil {
		t.Fatal(err)
	}
	f.decide(t, q, true, false)
	note = f.waitApprovalNotes(t, f.owner, 2)[1]
	if note.Note.Verdict != approval.Failed || !strings.Contains(note.Content, `The comment at "src/login.go":1 changed since you were asked, nothing was published.`) || len(f.gitlab.asked()) != 1 {
		t.Fatalf("the approval of a changed comment posted %q, note %s", f.gitlab.asked(), note.Content)
	}
	f.waitNoQuestion(t)
	third := f.comment(t, 3, "func login() {", "Exported?", "")
	f.publish(t, true, f.owner, url.Values{"id": {third.ID}})
	q = f.waitQuestion(t)
	if _, _, err := f.s.lineComments.Save("app", linecomment.Comment{ID: third.ID, Path: third.Path, Line: 2, LineText: "", Text: third.Text}, linecomment.Writer{}); err != nil {
		t.Fatal(err)
	}
	f.decide(t, q, true, false)
	note = f.waitApprovalNotes(t, f.owner, 3)[2]
	if note.Note.Verdict != approval.Failed || !strings.Contains(note.Content, `The comment at "src/login.go":2 changed since you were asked, nothing was published.`) || len(f.gitlab.asked()) != 1 {
		t.Fatalf("the approval of a comment moved to another line posted %q, note %s", f.gitlab.asked(), note.Content)
	}
}

// With the approval off an assistant's publish goes at once and answers what
// its note would say.
func TestAnAssistantsPublishWithTheApprovalOffGoesAtOnce(t *testing.T) {
	f := newForgePublishFixture(t)
	setApprovalAsks(f.s.settings, approvalForgePublish, false)
	f.comment(t, 1, "package login", "one", "")
	f.comment(t, 4, "\tname := bb", "two", "")
	code, answer := f.publish(t, true, f.owner, url.Values{"all": {"1"}})
	published, _ := answer["published"].(string)
	if code != http.StatusOK || !strings.Contains(published, ` 2 line comments of feature in app are published on the merge request !4 "Fix the login", each a review thread of its own`) ||
		!strings.HasSuffix(published, "\n- src/login.go:1 https://gitlab.example.com/group/sub/app/-/merge_requests/4#note_301\n- src/login.go:4 https://gitlab.example.com/group/sub/app/-/merge_requests/4#note_302") {
		t.Fatalf("the publish answered %d %v", code, answer)
	}
	if len(f.broker.Questions()) != 0 || len(f.gitlab.asked()) != 2 {
		t.Fatalf("with the approval off the publish asked %v, posted %q", f.broker.Questions(), f.gitlab.asked())
	}
}

// Two publishes naming one comment post it once: the second picks its
// comments after the first published, and finds a note of the forge.
func TestTwoPublishesOfOneCommentPostItOnce(t *testing.T) {
	f := newForgePublishFixture(t)
	c := f.comment(t, 4, "\tname := bb", "Why bb?", "")
	f.gitlab.mu.Lock()
	f.gitlab.hold, f.gitlab.got = make(chan struct{}), make(chan struct{}, 2)
	f.gitlab.mu.Unlock()
	answers := make(chan string, 2)
	publish := func() {
		code, answer := f.publish(t, false, "", url.Values{"id": {c.ID}})
		answers <- fmt.Sprintf("%d %v%v", code, answer["message"], answer["error"])
	}
	go publish()
	<-f.gitlab.got
	go publish()
	time.Sleep(200 * time.Millisecond)
	close(f.gitlab.hold)
	got := []string{<-answers, <-answers}
	slices.Sort(got)
	want := []string{"200 The comment is published on merge request !4.<nil>", `409 <nil>The comment at "src/login.go":4 is a review thread on the forge already.`}
	if !slices.Equal(got, want) || len(f.gitlab.asked()) != 1 {
		t.Fatalf("the publishes answered %q, posted %q", got, f.gitlab.asked())
	}
}
