package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

// fakeComments is an Enterprise Server holding the pull request 7 of
// octo/app at head, its files over two pages, a changed one, a renamed one,
// a removed one and a binary one, and posting a review comment the way
// GitHub answers it, or refusing what refusals names by its path.
type fakeComments struct {
	mu       sync.Mutex
	sent     []string
	refusals map[string]refusalAnswer
}

type refusalAnswer struct {
	code int
	body string
}

func (f *fakeComments) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if answer, refused := f.refusals[r.Method+" "+r.URL.Path]; refused {
		w.WriteHeader(answer.code)
		fmt.Fprint(w, answer.body)
		return
	}
	switch {
	case r.Header.Get("Authorization") != "Bearer "+token:
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials","documentation_url":"https://docs.github.com/rest","status":"401"}`)
	case r.URL.Path == repoAt+"/pulls/7" && r.Method == http.MethodGet:
		fmt.Fprintf(w, `{"number":7,"title":"Fix the login","state":"open","draft":false,"html_url":%q,"base":{"ref":"main","sha":%q},"head":{"label":"octo:feature","ref":"feature","sha":%q,"repo":{"full_name":"octo/app"}}}`, pullPage, older, head)
	case r.URL.Path == repoAt+"/pulls/7/files" && r.URL.Query().Get("page") == "1":
		w.Header().Set("Link", `<https://github.example.com/api/v3/repositories/2/pulls/7/files?per_page=100&page=2>; rel="next", <https://github.example.com/api/v3/repositories/2/pulls/7/files?per_page=100&page=2>; rel="last"`)
		fmt.Fprint(w, `[{"sha":"bbcd538c8e72b8c175046e27cc8f907076331401","filename":"src/login.go","status":"modified","additions":1,"deletions":1,"changes":2,"patch":"@@ -1,4 +1,4 @@\n package login\n \n func login() {\n-\tname := b\n+\tname := bb"}]`)
	case r.URL.Path == repoAt+"/pulls/7/files":
		fmt.Fprint(w, `[{"filename":"src/new.go","previous_filename":"src/old.go","status":"renamed","patch":"@@ -3,2 +3,3 @@\n a\n+b\n c"},{"filename":"gone.txt","status":"removed","patch":"@@ -1 +0,0 @@\n-gone"},{"filename":"logo.png","status":"added"}]`)
	case r.URL.Path == repoAt+"/pulls/7/comments" && r.Method == http.MethodPost:
		body, _ := io.ReadAll(r.Body)
		f.sent = append(f.sent, string(body))
		var sent struct {
			Body string `json:"body"`
			Path string `json:"path"`
			Line int    `json:"line"`
		}
		_ = json.Unmarshal(body, &sent)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"url":"https://github.example.com/api/v3/repos/octo/app/pulls/comments/1","pull_request_review_id":42,"id":10,"node_id":"PRRC_kwDOe2e10","diff_hunk":"@@ -1,4 +1,4 @@","path":%q,"commit_id":%q,"original_commit_id":%q,"user":{"login":"octocat","id":1},"body":%q,"created_at":"2026-10-29T18:40:00Z","updated_at":"2026-10-29T18:40:00Z","html_url":"%s#discussion_r10","line":%d,"side":"RIGHT","original_line":%d,"author_association":"OWNER"}`,
			sent.Path, head, head, sent.Body, pullPage, sent.Line, sent.Line)
	default:
		http.NotFound(w, r)
	}
}

func commentsFixture(t *testing.T) (*fakeComments, forge.Binding, context.Context) {
	t.Helper()
	fake := &fakeComments{refusals: map[string]refusalAnswer{}}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return fake, forge.Binding{Host: host(srv.URL), Path: "octo/app"}, ctx
}

var pullSeven = forge.Change{Number: 7, State: forge.Open, URL: pullPage, Head: head}

// The diff is the pull request's head commit and the lines each file's patch
// shows on the new side, read over every page, a renamed file under its new
// path with its old one, without a removed file, a binary one with no line.
func TestTheDiffOfAPullRequestIsItsHeadsFiles(t *testing.T) {
	_, b, ctx := commentsFixture(t)
	d, err := Provider{}.ChangeDiff(ctx, forge.NewClient(), b, token, pullSeven)
	if err != nil {
		t.Fatal(err)
	}
	if d.Head != head || d.Base != "" || d.Partial || len(d.Files) != 3 {
		t.Fatalf("the diff reads %+v", d)
	}
	if f := d.Files["src/login.go"]; f.OldPath != "src/login.go" || !maps.Equal(f.Lines, map[int]int{1: 1, 2: 2, 3: 3, 4: 0}) {
		t.Fatalf("the changed file reads %+v", f)
	}
	if f := d.Files["src/new.go"]; f.OldPath != "src/old.go" || !maps.Equal(f.Lines, map[int]int{3: 3, 4: 0, 5: 4}) {
		t.Fatalf("the renamed file reads %+v", f)
	}
	if f := d.Files["logo.png"]; len(f.Lines) != 0 {
		t.Fatalf("the binary file reads %+v", f)
	}
}

// A review comment stands at its line on the right side of the diff at the
// head commit, and its note is the comment's node id, the one the review
// threads name it by, with its page.
func TestAReviewCommentStandsOnTheRightSideAtTheHead(t *testing.T) {
	fake, b, ctx := commentsFixture(t)
	note, err := Provider{}.PublishNote(ctx, forge.NewClient(), b, token, pullSeven, forge.Diff{Head: head}, forge.Placed{Draft: forge.Draft{Path: "src/login.go", Line: 4, Body: "Why bb?"}, OldPath: "src/login.go"})
	if err != nil || note.ID != "PRRC_kwDOe2e10" || note.Author != "octocat" || note.Body != "Why bb?" || note.URL != pullPage+"#discussion_r10" || note.Created.IsZero() {
		t.Fatalf("the note reads %+v, %v", note, err)
	}
	if want := `{"body":"Why bb?","commit_id":"` + head + `","line":4,"path":"src/login.go","side":"RIGHT"}`; len(fake.sent) != 1 || fake.sent[0] != want {
		t.Fatalf("the comment was sent as %q", fake.sent)
	}
}

// A comment GitHub refuses says why in a sentence: a token that may not, a
// pull request hidden from it, a line GitHub refuses in its own words.
func TestARefusedReviewCommentIsSaidPlainly(t *testing.T) {
	p := forge.Placed{Draft: forge.Draft{Path: "src/login.go", Line: 9, Body: "x"}, OldPath: "src/login.go"}
	comments := "POST " + repoAt + "/pulls/7/comments"
	for _, c := range []struct {
		code int
		body string
		want string
	}{
		{http.StatusForbidden, `{"message":"Resource not accessible by personal access token","documentation_url":"https://docs.github.com/rest/pulls/comments","status":"403"}`, "The token may not comment on pull requests in "},
		{http.StatusNotFound, `{"message":"Not Found","documentation_url":"https://docs.github.com/rest","status":"404"}`, "GitHub knows no pull request #7 in "},
		{http.StatusUnprocessableEntity, `{"message":"Validation Failed","errors":[{"resource":"PullRequestReviewComment","code":"custom","field":"pull_request_review_thread.line","message":"could not be resolved"}],"documentation_url":"https://docs.github.com/rest/pulls/comments","status":"422"}`, "GitHub refused the comment on pull request #7: could not be resolved."},
	} {
		fake, b, ctx := commentsFixture(t)
		fake.refusals[comments] = refusalAnswer{c.code, c.body}
		_, err := Provider{}.PublishNote(ctx, forge.NewClient(), b, token, pullSeven, forge.Diff{Head: head}, p)
		if err == nil || !strings.HasPrefix(err.Error(), c.want) || strings.Contains(err.Error(), token) {
			t.Errorf("%d answered %v", c.code, err)
		}
	}
}
