package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

const pullPage = "https://github.example.com/octo/app/pull/7"

// The answers of GitHub as its APIs send them: a review thread of GraphQL's
// reviewThreads with its comments, a null line or original line left out,
// and of REST a comment of the pull request's conversation and a review.
type threadAt struct {
	path, side         string
	line, originalLine int
	resolved, outdated bool
	moreComments       bool
	comments           []string
	hunk               string
}

func threadAnswer(id string, at threadAt) string {
	line := func(n int) string {
		if n == 0 {
			return "null"
		}
		return fmt.Sprint(n)
	}
	return fmt.Sprintf(`{"id":%q,"isResolved":%t,"isOutdated":%t,"path":%q,"line":%s,"originalLine":%s,"diffSide":%q,"comments":{"pageInfo":{"hasNextPage":%t},"nodes":[%s]},"opening":{"nodes":[{"diffHunk":%q}]}}`,
		id, at.resolved, at.outdated, at.path, line(at.line), line(at.originalLine), at.side, at.moreComments, strings.Join(at.comments, ","), at.hunk)
}

func reviewCommentAnswer(id int, state, author, body string) string {
	who := "null"
	if author != "" {
		who = fmt.Sprintf(`{"login":%q}`, author)
	}
	return fmt.Sprintf(`{"id":"PRRC_kwDOAbc%d","state":%q,"body":%q,"url":"%s#discussion_r%d","createdAt":"2026-10-28T10:%02d:00Z","author":%s}`, id, state, body, pullPage, id, id%60, who)
}

func issueCommentAnswer(id int, minute int, body string) string {
	return fmt.Sprintf(`{"url":"https://github.example.com/api/v3/repos/octo/app/issues/comments/%d","html_url":"%s#issuecomment-%d","issue_url":"https://github.example.com/api/v3/repos/octo/app/issues/7","id":%d,"node_id":"IC_kwDOAbc%d","user":{"login":"hubot","id":2,"type":"User"},"created_at":"2026-10-28T10:%02d:00Z","updated_at":"2026-10-28T10:%02d:00Z","author_association":"COLLABORATOR","body":%q}`,
		id, pullPage, id, id, id, minute, minute, body)
}

func reviewAnswer(id int, minute int, state, body string) string {
	submitted := ""
	if state != "PENDING" {
		submitted = fmt.Sprintf(`"submitted_at":"2026-10-28T10:%02d:00Z",`, minute)
	}
	return fmt.Sprintf(`{"id":%d,"node_id":"PRR_kwDOAbc%d","user":{"login":"octocat","id":1},"body":%q,"state":%q,"html_url":"%s#pullrequestreview-%d","pull_request_url":"https://github.example.com/api/v3/repos/octo/app/pulls/7",%s"commit_id":%q,"author_association":"OWNER"}`,
		id, id, body, state, pullPage, id, submitted, head)
}

// fakeReview answers the pull request 7 of octo/app on an Enterprise Server
// for the token alone: its review threads over GraphQL, a page for each
// cursor, and its conversation's comments and reviews over REST in pages
// that name the next in Link. It keeps what it was asked.
type fakeReview struct {
	threadPages [][]string
	comments    [][]string
	reviews     [][]string
	graphql     func(w http.ResponseWriter) bool

	asks      []string
	variables []map[string]any
}

func (f *fakeReview) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.asks = append(f.asks, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials","documentation_url":"https://docs.github.com/rest","status":"401"}`)
		return
	}
	paged := func(pages [][]string) {
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		if page < len(pages) {
			w.Header().Set("Link", fmt.Sprintf(`<https://elsewhere.example.com%s?page=%d>; rel="next", <https://elsewhere.example.com%s?page=%d>; rel="last"`, r.URL.Path, page+1, r.URL.Path, len(pages)))
		}
		if page > len(pages) {
			fmt.Fprint(w, "[]")
			return
		}
		fmt.Fprint(w, "["+items(pages[page-1]...)+"]")
	}
	switch r.URL.EscapedPath() {
	case "/api/graphql":
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		f.variables = append(f.variables, body.Variables)
		if f.graphql != nil && f.graphql(w) {
			return
		}
		page := 0
		if after, _ := body.Variables["after"].(string); after != "" {
			fmt.Sscanf(after, "cursor%d", &page)
		}
		next := page+1 < len(f.threadPages)
		fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":%t,"endCursor":"cursor%d"},"nodes":[%s]}}}}}`, next, page+1, items(f.threadPages[page]...))
	case repoAt + "/issues/7/comments":
		paged(f.comments)
	case repoAt + "/pulls/7/reviews":
		paged(f.reviews)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found","documentation_url":"https://docs.github.com/rest","status":"404"}`)
	}
}

func readThreads(t *testing.T, webURL, tok string) (forge.Review, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	review, err := Provider{}.Threads(ctx, forge.NewClient(), forge.Binding{Host: host(webURL), Path: "octo/app"}, tok, 7)
	if err != nil && strings.Contains(err.Error(), tok) {
		t.Fatalf("the error names the token: %v", err)
	}
	return review, err
}

func githubNote(id string, author, body, page string, minute int) forge.Note {
	return forge.Note{ID: id, Author: author, Body: body, Created: time.Date(2026, 10, 28, 10, minute, 0, 0, time.UTC), URL: page}
}

func reviewNote(id int, author, body string) forge.Note {
	return githubNote(fmt.Sprintf("PRRC_kwDOAbc%d", id), author, body, fmt.Sprintf("%s#discussion_r%d", pullPage, id), id%60)
}

// GitHub's GraphQL and REST API live below an Enterprise Server's web
// address, github.com's GraphQL on the host of its REST API.
func TestGitHubComHasItsGraphQLBesideItsAPI(t *testing.T) {
	if got := graphql(forge.NewClient(), Builtin(), token).Base(); got != "https://api.github.com/graphql" {
		t.Fatalf("github.com reads %s", got)
	}
	if got := graphql(forge.NewClient(), host("https://github.example.com/"), token).Base(); got != "https://github.example.com/api/graphql" {
		t.Fatalf("an Enterprise Server reads %s", got)
	}
}

// The review threads come over every page of GraphQL with whether they are
// resolved, at their line on the right side as the new one and the left as
// the old one, an outdated one at the line it was written at, one on a
// whole file without a line; each on a line with the text of that line, the
// last line of its first comment's diff hunk without its mark, on either
// side, and nothing on a whole file; a comment of a review still pending stays out,
// and a thread of nothing else; a thread with more comments than a read
// takes makes the review partial. The conversation's comments over every
// page and the submitted reviews with a body are the general threads, in
// the order they were written, and the next page is asked for at the list's
// own path.
func TestTheThreadsAreTheReviewThreadsAndTheConversation(t *testing.T) {
	f := &fakeReview{
		threadPages: [][]string{
			{
				threadAnswer("PRRT_1", threadAt{path: "src/login.go", side: "RIGHT", line: 4, originalLine: 4, hunk: "@@ -1,4 +1,4 @@\n a := 1\n-b := 2\n+bb := 2\n c := 3\n+name := bb\n", comments: []string{
					reviewCommentAnswer(1, "SUBMITTED", "octocat", "New name bb?"),
					reviewCommentAnswer(5, "SUBMITTED", "", "Reply of a deleted user"),
					reviewCommentAnswer(9, "PENDING", "hubot", "Not sent yet"),
				}}),
				threadAnswer("PRRT_2", threadAt{path: "src/login.go", side: "LEFT", line: 4, originalLine: 4, resolved: true, hunk: "@@ -1,4 +1,3 @@\n a := 1\n-b := 2", comments: []string{reviewCommentAnswer(2, "SUBMITTED", "octocat", "Why drop b?")}}),
			},
			{
				threadAnswer("PRRT_3", threadAt{path: "src/login.go", side: "RIGHT", originalLine: 7, outdated: true, moreComments: true, hunk: "@@ -5,2 +5,3 @@\n \td := 4\r\n \te := 5\r\n", comments: []string{reviewCommentAnswer(3, "SUBMITTED", "octocat", "Added e")}}),
				threadAnswer("PRRT_4", threadAt{path: "README.md", side: "RIGHT", hunk: "@@ -1 +1 @@\n-old\n+new", comments: []string{reviewCommentAnswer(4, "SUBMITTED", "octocat", "File note")}}),
				threadAnswer("PRRT_5", threadAt{path: "src/login.go", side: "RIGHT", line: 2, originalLine: 2, comments: []string{reviewCommentAnswer(6, "PENDING", "octocat", "Mine alone")}}),
			},
		},
		comments: [][]string{{issueCommentAnswer(11, 20, "First\r\nwith two lines")}, {issueCommentAnswer(12, 40, "Later")}},
		reviews: [][]string{{
			reviewAnswer(80, 30, "CHANGES_REQUESTED", "Please rename"),
			reviewAnswer(81, 35, "APPROVED", ""),
			reviewAnswer(82, 50, "PENDING", "Draft summary"),
			reviewAnswer(83, 10, "COMMENTED", "Early summary"),
		}},
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	review, err := readThreads(t, srv.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	want := forge.Review{
		Diff: []forge.Thread{
			{ID: "PRRT_1", Path: "src/login.go", Line: 4, Side: forge.NewSide, Quote: "name := bb", Resolvable: true, Notes: []forge.Note{reviewNote(1, "octocat", "New name bb?"), reviewNote(5, "", "Reply of a deleted user")}},
			{ID: "PRRT_2", Path: "src/login.go", Line: 4, Side: forge.OldSide, Quote: "b := 2", Resolvable: true, Resolved: true, Notes: []forge.Note{reviewNote(2, "octocat", "Why drop b?")}},
			{ID: "PRRT_3", Path: "src/login.go", Line: 7, Side: forge.NewSide, Quote: "\te := 5\r", Outdated: true, Resolvable: true, Notes: []forge.Note{reviewNote(3, "octocat", "Added e")}},
			{ID: "PRRT_4", Path: "README.md", Resolvable: true, Notes: []forge.Note{reviewNote(4, "octocat", "File note")}},
		},
		General: []forge.Thread{
			{ID: "PRR_kwDOAbc83", Single: true, Notes: []forge.Note{githubNote("PRR_kwDOAbc83", "octocat", "Early summary", pullPage+"#pullrequestreview-83", 10)}},
			{ID: "IC_kwDOAbc11", Single: true, Notes: []forge.Note{githubNote("IC_kwDOAbc11", "hubot", "First\r\nwith two lines", pullPage+"#issuecomment-11", 20)}},
			{ID: "PRR_kwDOAbc80", Single: true, Notes: []forge.Note{githubNote("PRR_kwDOAbc80", "octocat", "Please rename", pullPage+"#pullrequestreview-80", 30)}},
			{ID: "IC_kwDOAbc12", Single: true, Notes: []forge.Note{githubNote("IC_kwDOAbc12", "hubot", "Later", pullPage+"#issuecomment-12", 40)}},
		},
		Partial: true,
	}
	if !reflect.DeepEqual(review, want) {
		t.Fatalf("the review reads\n%+v\nwant\n%+v", review, want)
	}
	asked := strings.Join(f.asks, " ")
	wantAsked := "POST /api/graphql? POST /api/graphql? " +
		"GET " + repoAt + "/issues/7/comments?per_page=100&page=1 GET " + repoAt + "/issues/7/comments?per_page=100&page=2 " +
		"GET " + repoAt + "/pulls/7/reviews?per_page=100&page=1"
	if asked != wantAsked {
		t.Fatalf("asked %s\nwant %s", asked, wantAsked)
	}
	first, second := fmt.Sprint(f.variables[0]), fmt.Sprint(f.variables[1])
	if first != "map[after:<nil> first:50 name:app notes:50 number:7 owner:octo]" || second != "map[after:cursor1 first:50 name:app notes:50 number:7 owner:octo]" {
		t.Fatalf("the variables were %s and %s", first, second)
	}
}

// A pull request without notes is an answer, and one whose review threads
// fill every page the read takes is partial.
func TestThreadsWithoutNotesAndPastTheBound(t *testing.T) {
	srv := httptest.NewServer(&fakeReview{threadPages: [][]string{{}}})
	defer srv.Close()
	if review, err := readThreads(t, srv.URL, token); err != nil || review.Partial || review.Diff != nil || review.General != nil {
		t.Fatalf("no notes: %+v, %v", review, err)
	}
	pages := make([][]string, maxThreadPages+2)
	for i := range pages {
		pages[i] = []string{threadAnswer(fmt.Sprintf("PRRT_%d", i), threadAt{path: "a.go", side: "RIGHT", line: 1, comments: []string{reviewCommentAnswer(i, "SUBMITTED", "octocat", "x")}})}
	}
	f := &fakeReview{threadPages: pages}
	bound := httptest.NewServer(f)
	defer bound.Close()
	review, err := readThreads(t, bound.URL, token)
	if err != nil || !review.Partial || len(review.Diff) != maxThreadPages || len(f.variables) != maxThreadPages {
		t.Fatalf("past the bound: %d threads, partial %t, %v, %d queries", len(review.Diff), review.Partial, err, len(f.variables))
	}
}

// A refused read says what it means: GraphQL's errors by their type, a pull
// request it does not answer, a token refused or not allowed, and a server
// without GraphQL.
func TestThreadFailuresAreSaid(t *testing.T) {
	answer := func(code int, body string) func(w http.ResponseWriter) bool {
		return func(w http.ResponseWriter) bool {
			w.WriteHeader(code)
			fmt.Fprint(w, body)
			return true
		}
	}
	for _, c := range []struct {
		graphql func(w http.ResponseWriter) bool
		tok     string
		want    string
	}{
		{answer(200, `{"data":{"repository":{"pullRequest":null}},"errors":[{"type":"NOT_FOUND","path":["repository","pullRequest"],"locations":[{"line":3,"column":5}],"message":"Could not resolve to a PullRequest with the number of 7."}]}`), token,
			"GitHub knows no pull request #7 in " + "%s/octo/app, or the token may not read it."},
		{answer(200, `{"data":{"repository":null},"errors":[{"type":"FORBIDDEN","path":["repository"],"message":"Resource not accessible by personal access token"}]}`), token,
			"The token may not read the review threads of %s/octo/app."},
		{answer(200, `{"errors":[{"message":"Something went wrong while executing your query."}]}`), token,
			"GitHub refused the read of the review threads of #7: Something went wrong while executing your query."},
		{answer(200, `{"data":{"repository":{"pullRequest":null}}}`), token, "GitHub knows no pull request #7 in %s/octo/app, or the token may not read it."},
		{answer(404, `{"message":"Not Found","documentation_url":"https://docs.github.com/graphql"}`), token, "No GitHub GraphQL API answers at http://%s/api/graphql: check the kind and the web address."},
		{nil, "github_pat_n0t_the_t0ken", "GitHub refused the token: it is wrong, expired or revoked."},
	} {
		srv := httptest.NewServer(&fakeReview{threadPages: [][]string{{}}, graphql: c.graphql})
		address := strings.TrimPrefix(srv.URL, "http://")
		want := c.want
		if strings.Contains(want, "%s") {
			want = fmt.Sprintf(want, address)
		}
		if _, err := readThreads(t, srv.URL, c.tok); err == nil || err.Error() != want {
			t.Errorf("got %v\nwant %s", err, want)
		}
		srv.Close()
	}
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.URL.Path == "/api/graphql" {
			fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Resource not accessible by personal access token","documentation_url":"https://docs.github.com/rest","status":"403"}`)
	}))
	defer forbidden.Close()
	if _, err := readThreads(t, forbidden.URL, token); err == nil || err.Error() != "The token may not read the review threads of "+strings.TrimPrefix(forbidden.URL, "http://")+"/octo/app." {
		t.Fatalf("a conversation the token may not read: %v", err)
	}
}
