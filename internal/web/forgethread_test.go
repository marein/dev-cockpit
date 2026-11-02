package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/localapi"
)

// threadGitLab answers group/sub/app like GitLab: the open merge request !4
// of feature at head, no pipeline, and its discussions, d1 on line 4 of
// src/login.go that alice opened, with every reply posted to it after, the
// general thread g1 with the replies posted to it and the comment g2, which
// GitLab does not resolve; d1 left out while gone. A reply and a resolve of
// d1 are refused the way GitLab refuses an account without the right while
// forbid is set, a resolve alone the way it refuses a Reporter while
// reporter is set. It keeps what was written.
type threadGitLab struct {
	mu        sync.Mutex
	head      string
	forbid    bool
	reporter  bool
	gone      bool
	resolved  bool
	replies   []string
	general   []string
	generalOK bool
	wrote     []string
}

func (g *threadGitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	note := func(id int, author, body string) string {
		return fmt.Sprintf(`{"id":%d,"type":"DiffNote","body":%q,"author":{"username":%q},"created_at":"2026-10-29T21:30:56.368Z","system":false,"position":{"base_sha":"b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0","start_sha":"b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0","head_sha":%q,"old_path":"src/login.go","new_path":"src/login.go","position_type":"text","old_line":null,"new_line":4},"resolvable":true,"resolved":%v}`, id, body, author, g.head, g.resolved)
	}
	switch path := r.URL.EscapedPath(); {
	case path == project+"/merge_requests":
		fmt.Fprintf(w, `[{"iid":4,"title":"Fix the login","state":"opened","draft":false,"web_url":"https://gitlab.example.com/group/sub/app/-/merge_requests/4","target_branch":"master","source_project_id":2,"target_project_id":2,"sha":%q}]`, g.head)
	case path == project+"/pipelines":
		fmt.Fprint(w, `[]`)
	case path == mr:
		fmt.Fprintf(w, `{"iid":4,"web_url":"https://gitlab.example.com/group/sub/app/-/merge_requests/4","diff_refs":{"base_sha":"b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0","start_sha":"b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0","head_sha":%q}}`, g.head)
	case path == mr+"/discussions" && r.Method == http.MethodGet:
		notes := []string{note(29, "alice", "New name?")}
		for i, body := range g.replies {
			notes = append(notes, note(400+i, "root", body))
		}
		general := func(id int, author, body string) string {
			return fmt.Sprintf(`{"id":%d,"body":%q,"author":{"username":%q},"system":false,"resolvable":true,"resolved":%v}`, id, body, author, g.generalOK)
		}
		opened := []string{general(34, "alice", "General")}
		for i, body := range g.general {
			opened = append(opened, general(500+i, "root", body))
		}
		discussions := []string{
			`{"id":"g1","individual_note":false,"notes":[` + strings.Join(opened, ",") + `]}`,
			`{"id":"g2","individual_note":true,"notes":[{"id":35,"body":"Just a comment","author":{"username":"bob"},"system":false,"resolvable":false}]}`,
		}
		if !g.gone {
			discussions = append(discussions, `{"id":"d1","individual_note":false,"notes":[`+strings.Join(notes, ",")+`]}`)
		}
		fmt.Fprint(w, "["+strings.Join(discussions, ",")+"]")
	case g.forbid && strings.HasPrefix(path, mr+"/discussions/d1"), g.reporter && path == mr+"/discussions/d1" && r.Method == http.MethodPut:
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"403 Forbidden"}`)
	case path == mr+"/discussions/d1/notes" && r.Method == http.MethodPost:
		var sent map[string]string
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &sent)
		g.wrote = append(g.wrote, "reply: "+sent["body"])
		g.replies = append(g.replies, sent["body"])
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, note(400+len(g.replies)-1, "root", sent["body"]))
	case path == mr+"/discussions/g1/notes" && r.Method == http.MethodPost:
		var sent map[string]string
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &sent)
		g.wrote = append(g.wrote, "g1 reply: "+sent["body"])
		g.general = append(g.general, sent["body"])
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":%d,"body":%q,"author":{"username":"root"},"system":false,"resolvable":true,"resolved":false}`, 500+len(g.general)-1, sent["body"])
	case path == mr+"/discussions/g1" && r.Method == http.MethodPut && r.URL.Query().Get("resolved") == "true":
		g.wrote = append(g.wrote, "g1 resolve")
		g.generalOK = true
		fmt.Fprint(w, `{"id":"g1","individual_note":false,"notes":[]}`)
	case path == mr+"/discussions/d1" && r.Method == http.MethodPut && r.URL.Query().Get("resolved") == "true":
		g.wrote = append(g.wrote, "resolve")
		g.resolved = true
		fmt.Fprintf(w, `{"id":"d1","individual_note":false,"resolvable":true,"resolved":true,"notes":[%s]}`, note(29, "alice", "New name?"))
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 Discussion Not Found"}`)
	}
}

func (g *threadGitLab) written() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.wrote)
}

type forgeThreadFixture struct {
	*composeFixture
	gitlab *threadGitLab
}

func newForgeThreadFixture(t *testing.T) *forgeThreadFixture {
	t.Helper()
	f := &forgeThreadFixture{}
	f.composeFixture, _, _ = newForgeLoginFixture(t, func(pushed string) http.Handler {
		f.gitlab = &threadGitLab{head: pushed}
		return f.gitlab
	})
	f.router.POST("/projects/:name/forge/thread", f.s.handleProjectForgeThread)
	return f
}

func (f *forgeThreadFixture) write(t *testing.T, local bool, form url.Values) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/projects/app/forge/thread", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if local {
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		req.Header.Set(localapi.AssistantHeader, f.owner)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec.Code, decodeJSON(t, rec)
}

// A click answers the thread and resolves it after the reply, or resolves it
// alone, and says what it did; a thread resolved already is not resolved
// again.
func TestAClickAnswersAndResolvesAThread(t *testing.T) {
	f := newForgeThreadFixture(t)
	code, answer := f.write(t, false, url.Values{"thread": {" d1 "}, "body": {" fixed in abc123\n"}, "resolve": {"1"}})
	if code != http.StatusOK || answer["message"] != "The reply is posted on merge request !4, the thread is resolved." {
		t.Fatalf("the reply answered %d %v", code, answer)
	}
	if got := f.gitlab.written(); !slices.Equal(got, []string{"reply: fixed in abc123", "resolve"}) {
		t.Fatalf("the forge was asked %q", got)
	}
	if code, answer := f.write(t, false, url.Values{"thread": {"d1"}, "body": {"one more"}}); code != http.StatusOK || answer["message"] != "The reply is posted on merge request !4." {
		t.Fatalf("a reply on the resolved thread answered %d %v", code, answer)
	}
	if code, answer := f.write(t, false, url.Values{"thread": {"d1"}, "resolve": {"1"}}); code != http.StatusConflict || answer["error"] != `The thread is resolved already.` {
		t.Fatalf("a resolve of the resolved thread answered %d %v", code, answer)
	}
	if got := f.gitlab.written(); len(got) != 3 {
		t.Fatalf("the forge was asked %q", got)
	}
}

// Nothing is written for a request that names no thread or nothing to write,
// a reply too long, a thread the change request does not have, a resolve of a
// comment GitLab does not resolve, or a branch the cockpit's rule refuses; a
// refusal of the forge says what it means.
func TestAWriteOnAThreadIsRefusedBeforeTheForgeIsAsked(t *testing.T) {
	f := newForgeThreadFixture(t)
	for _, c := range []struct {
		form url.Values
		code int
		says string
	}{
		{url.Values{"body": {"x"}}, http.StatusBadRequest, "Name the review thread by its id."},
		{url.Values{"thread": {"d1\u202e"}, "body": {"x"}}, http.StatusBadRequest, "The id of a review thread holds no spaces, no control characters and no invisible format characters."},
		{url.Values{"thread": {strings.Repeat("d", 256)}, "body": {"x"}}, http.StatusBadRequest, "The id of a review thread is text of at most 255 characters."},
		{url.Values{"thread": {"d1"}, "body": {" \n "}}, http.StatusBadRequest, "Write a reply, or resolve the thread."},
		{url.Values{"thread": {"d1"}, "body": {strings.Repeat("x", 16<<10+1)}}, http.StatusBadRequest, "A reply has at most 16 KiB."},
		{url.Values{"thread": {"d9"}, "body": {"x"}}, http.StatusConflict, `The merge request !4 has no such review thread.`},
		{url.Values{"thread": {"g2"}, "body": {"x"}, "resolve": {"1"}}, http.StatusConflict, `GitLab does not resolve this thread.`},
		{url.Values{"thread": {"d1"}, "body": {"x"}, "branch": {"feature\u202e"}}, http.StatusBadRequest, "A branch name holds no spaces, no control characters and no invisible characters, its character 8 is U+202E."},
	} {
		if code, answer := f.write(t, false, c.form); code != c.code || answer["error"] != c.says {
			t.Errorf("%v answered %d %v", c.form, code, answer)
		}
	}
	if got := f.gitlab.written(); len(got) != 0 {
		t.Fatalf("a refused write asked %q", got)
	}
	f.gitlab.mu.Lock()
	f.gitlab.forbid = true
	f.gitlab.mu.Unlock()
	if code, answer := f.write(t, false, url.Values{"thread": {"d1"}, "body": {"x"}}); code != http.StatusBadGateway || answer["replied"] != false || !strings.HasPrefix(answer["error"].(string), "The token's account may not comment on merge request !4 in ") {
		t.Fatalf("a reply the forge refused answered %d %v", code, answer)
	}
}

// A resolve the forge refuses after the reply went says that the reply is
// posted, so the reply dialog closes instead of offering to post it again.
func TestAResolveRefusedAfterTheReplySaysTheReplyIsPosted(t *testing.T) {
	f := newForgeThreadFixture(t)
	f.gitlab.mu.Lock()
	f.gitlab.reporter = true
	f.gitlab.mu.Unlock()
	code, answer := f.write(t, false, url.Values{"thread": {"d1"}, "body": {"x"}, "resolve": {"1"}})
	said, _ := answer["error"].(string)
	if code != http.StatusBadGateway || answer["replied"] != true || !strings.HasPrefix(said, "The reply is posted, the thread is not resolved. The token's account may not resolve threads of merge request !4 in ") {
		t.Fatalf("a reply whose resolve the forge refused answered %d %v", code, answer)
	}
	if got := f.gitlab.written(); !slices.Equal(got, []string{"reply: x"}) {
		t.Fatalf("the forge was asked %q", got)
	}
}

// An assistant's write waits for the user, who sees the thread with its
// opening note, the reply, the merge request, branch, forge and project.
// Approved, the reply is posted and the thread resolved, and the note links
// the reply; a thread gone meanwhile fails the approval.
func TestAnAssistantsWriteOnAThreadWaitsForTheApproval(t *testing.T) {
	f := newForgeThreadFixture(t)
	code, answer := f.write(t, true, url.Values{"thread": {"d1"}, "body": {"fixed in abc123"}, "resolve": {"1"}})
	what := "Reply and resolve the thread at src/login.go:4 on merge request !4 of feature in app"
	if code != http.StatusOK || answer["pending"] != true || answer["what"] != what {
		t.Fatalf("the write answered %d %v", code, answer)
	}
	q := f.waitQuestion(t)
	address := f.s.forgeByProject(f.s.projects.List())["app"].Address()
	want := []askpass.Detail{
		{Label: "Action", Value: "Reply and resolve the thread"},
		{Label: "Thread", Value: "src/login.go:4 · alice: New name?"},
		{Label: "Reply", Value: "fixed in abc123"},
		{Label: "On", Value: "merge request !4 · Fix the login · https://gitlab.example.com/group/sub/app/-/merge_requests/4"},
		{Label: "Branch", Value: "feature"},
		{Label: "Forge", Value: "GitLab · " + address},
		{Label: "Project", Value: "app"},
	}
	if q.Action != what || !reflect.DeepEqual(q.Details, want) || len(f.gitlab.written()) != 0 {
		t.Fatalf("the question reads %+v, the forge was asked %q", q, f.gitlab.written())
	}
	if code, answer := f.write(t, true, url.Values{"thread": {"d1"}, "body": {"again"}}); code != http.StatusConflict || !strings.Contains(answer["error"].(string), "already waits") {
		t.Fatalf("a second write on the thread answered %d %v", code, answer)
	}
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	says := "On GitLab " + address + ` the review thread on src/login.go:4 of the merge request !4 "Fix the login" of feature in app holds your reply and is resolved: https://gitlab.example.com/group/sub/app/-/merge_requests/4#note_400.`
	if note.Note.Verdict != approval.Done || !strings.Contains(note.Content, says) || !slices.Equal(f.gitlab.written(), []string{"reply: fixed in abc123", "resolve"}) {
		t.Fatalf("the approval asked %q, note %s", f.gitlab.written(), note.Content)
	}
	f.waitNoQuestion(t)
	f.write(t, true, url.Values{"thread": {"d1"}, "body": {"late"}})
	q = f.waitQuestion(t)
	f.gitlab.mu.Lock()
	f.gitlab.gone = true
	f.gitlab.mu.Unlock()
	f.decide(t, q, true, false)
	note = f.waitApprovalNotes(t, f.owner, 2)[1]
	if note.Note.Verdict != approval.Failed || !strings.Contains(note.Content, `The merge request !4 has no such review thread.`) || len(f.gitlab.written()) != 2 {
		t.Fatalf("the approval of a thread gone asked %q, note %s", f.gitlab.written(), note.Content)
	}
}

// A general thread GitLab keeps as a thread takes a click's reply and resolve
// and an assistant's after the approval, which names it by the conversation,
// and the note says the general thread holds the reply.
func TestAGeneralThreadIsAnsweredAndResolved(t *testing.T) {
	f := newForgeThreadFixture(t)
	if code, answer := f.write(t, false, url.Values{"thread": {"g1"}, "body": {"yes"}, "resolve": {"1"}}); code != http.StatusOK || answer["message"] != "The reply is posted on merge request !4, the thread is resolved." {
		t.Fatalf("the click answered %d %v", code, answer)
	}
	f.gitlab.mu.Lock()
	f.gitlab.generalOK = false
	f.gitlab.mu.Unlock()
	code, answer := f.write(t, true, url.Values{"thread": {"g1"}, "body": {"done"}, "resolve": {"1"}})
	what := "Reply and resolve the thread in the conversation on merge request !4 of feature in app"
	if code != http.StatusOK || answer["pending"] != true || answer["what"] != what {
		t.Fatalf("the assistant's write answered %d %v", code, answer)
	}
	q := f.waitQuestion(t)
	if q.Details[1] != (askpass.Detail{Label: "Thread", Value: "the conversation · alice: General"}) {
		t.Fatalf("the question reads %+v", q)
	}
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	says := ` the general thread of the merge request !4 "Fix the login" of feature in app holds your reply and is resolved: https://gitlab.example.com/group/sub/app/-/merge_requests/4#note_501.`
	if note.Note.Verdict != approval.Done || !strings.Contains(note.Content, says) || !slices.Equal(f.gitlab.written(), []string{"g1 reply: yes", "g1 resolve", "g1 reply: done", "g1 resolve"}) {
		t.Fatalf("the approval asked %q, note %s", f.gitlab.written(), note.Content)
	}
}

// With the approval off an assistant's write goes at once and answers what
// its note would say.
func TestAnAssistantsWriteOnAThreadWithTheApprovalOffGoesAtOnce(t *testing.T) {
	f := newForgeThreadFixture(t)
	setApprovalAsks(f.s.settings, approvalForgeThread, false)
	code, answer := f.write(t, true, url.Values{"thread": {"d1"}, "resolve": {"1"}})
	written, _ := answer["written"].(string)
	if code != http.StatusOK || !strings.HasSuffix(written, ` the review thread on src/login.go:4 of the merge request !4 "Fix the login" of feature in app is resolved.`) {
		t.Fatalf("the resolve answered %d %v", code, answer)
	}
	if len(f.broker.Questions()) != 0 || !slices.Equal(f.gitlab.written(), []string{"resolve"}) {
		t.Fatalf("with the approval off the resolve asked %v, the forge %q", f.broker.Questions(), f.gitlab.written())
	}
}
