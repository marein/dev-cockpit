package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

// fakeThreadWrites is an Enterprise Server's GraphQL API answering the
// mutations on a review thread the way GitHub answers them, a reply in the
// state reply names, or the answer refusal holds.
type fakeThreadWrites struct {
	mu      sync.Mutex
	sent    []map[string]any
	reply   string
	refusal *refusalAnswer
}

func (f *fakeThreadWrites) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	body, _ := io.ReadAll(r.Body)
	var sent map[string]any
	_ = json.Unmarshal(body, &sent)
	f.sent = append(f.sent, sent)
	query, _ := sent["query"].(string)
	variables, _ := sent["variables"].(map[string]any)
	switch {
	case r.URL.Path != "/api/graphql" || r.Method != http.MethodPost:
		http.NotFound(w, r)
	case r.Header.Get("Authorization") != "Bearer "+token:
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials","documentation_url":"https://docs.github.com/graphql"}`)
	case f.refusal != nil:
		w.WriteHeader(f.refusal.code)
		fmt.Fprint(w, f.refusal.body)
	case strings.Contains(query, "addPullRequestReviewThreadReply"):
		fmt.Fprintf(w, `{"data":{"addPullRequestReviewThreadReply":{"comment":{"id":"PRRC_kwDOe2e12","state":%q,"body":%q,"url":"%s#discussion_r12","createdAt":"2026-10-29T21:40:00Z","author":{"login":"octocat"}}}}}`, f.reply, variables["body"], pullPage)
	case strings.Contains(query, "resolveReviewThread"):
		fmt.Fprintf(w, `{"data":{"resolveReviewThread":{"thread":{"id":%q,"isResolved":true}}}}`, variables["thread"])
	default:
		http.NotFound(w, r)
	}
}

func threadWritesFixture(t *testing.T) (*fakeThreadWrites, forge.Binding, context.Context) {
	t.Helper()
	fake := &fakeThreadWrites{reply: "SUBMITTED"}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return fake, forge.Binding{Host: host(srv.URL), Path: "octo/app"}, ctx
}

var writtenThread = forge.Thread{ID: "PRRT_kwDOe2e1", Path: "src/login.go", Line: 4, Side: forge.NewSide, Resolvable: true}

// A reply is a comment of the review thread by the thread's id, answered as
// the comment GitHub made with its page.
func TestAReplyIsACommentOfTheReviewThread(t *testing.T) {
	fake, b, ctx := threadWritesFixture(t)
	n, err := Provider{}.Reply(ctx, forge.NewClient(), b, token, pullSeven, writtenThread, "fixed in abc123")
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "PRRC_kwDOe2e12" || n.Author != "octocat" || n.Body != "fixed in abc123" || n.URL != pullPage+"#discussion_r12" || !n.Created.Equal(time.Date(2026, 10, 29, 21, 40, 0, 0, time.UTC)) {
		t.Fatalf("answered %+v", n)
	}
	variables, _ := fake.sent[0]["variables"].(map[string]any)
	if len(fake.sent) != 1 || variables["thread"] != "PRRT_kwDOe2e1" || variables["body"] != "fixed in abc123" {
		t.Fatalf("sent %v", fake.sent)
	}
}

// A reply GitHub files under a pending review of the token's account is
// said, it is no reply anybody sees yet.
func TestAReplyIntoAPendingReviewIsSaid(t *testing.T) {
	fake, b, ctx := threadWritesFixture(t)
	fake.reply = "PENDING"
	_, err := Provider{}.Reply(ctx, forge.NewClient(), b, token, pullSeven, writtenThread, "x")
	if err == nil || !strings.Contains(err.Error(), "into the token's pending review") {
		t.Fatalf("a pending reply answered %v", err)
	}
}

// A resolve asks the mutation by the thread's id and holds the thread
// resolved only where GitHub answers it so.
func TestAResolveResolvesTheReviewThread(t *testing.T) {
	fake, b, ctx := threadWritesFixture(t)
	if err := (Provider{}).Resolve(ctx, forge.NewClient(), b, token, pullSeven, writtenThread); err != nil {
		t.Fatal(err)
	}
	query, _ := fake.sent[0]["query"].(string)
	variables, _ := fake.sent[0]["variables"].(map[string]any)
	if len(fake.sent) != 1 || !strings.Contains(query, "resolveReviewThread(input: {threadId: $thread})") || variables["thread"] != "PRRT_kwDOe2e1" {
		t.Fatalf("sent %v", fake.sent)
	}
	fake.refusal = &refusalAnswer{http.StatusOK, `{"data":{"resolveReviewThread":{"thread":{"id":"PRRT_kwDOe2e1","isResolved":false}}}}`}
	if err := (Provider{}).Resolve(ctx, forge.NewClient(), b, token, pullSeven, writtenThread); err == nil || !strings.Contains(err.Error(), "did not answer the thread on pull request #7 as resolved") {
		t.Fatalf("a thread left unresolved answered %v", err)
	}
}

// A token that may not, a thread GitHub does not know, a refusal in GitHub's
// words and a host without a GraphQL API each say what they mean, for the
// reply and the resolve alike.
func TestTheWritesOnAReviewThreadSayWhyGitHubRefusedThem(t *testing.T) {
	scope := "a fine grained token needs the permission Pull requests read and write, a classic one the scope repo."
	for _, c := range []struct {
		answer refusalAnswer
		said   string
	}{
		{refusalAnswer{http.StatusOK, `{"data":{"addPullRequestReviewThreadReply":null,"resolveReviewThread":null},"errors":[{"type":"FORBIDDEN","path":["x"],"extensions":{"saml_failure":false},"locations":[{"line":2,"column":3}],"message":"Resource not accessible by personal access token"}]}`}, "The token may not %[1]s review threads in %[2]s: " + scope},
		{refusalAnswer{http.StatusOK, `{"errors":[{"type":"INSUFFICIENT_SCOPES","locations":[{"line":2,"column":3}],"message":"Your token has not been granted the required scopes to execute this query."}]}`}, "The token may not %[1]s review threads in %[2]s: " + scope},
		{refusalAnswer{http.StatusOK, `{"data":{"addPullRequestReviewThreadReply":null,"resolveReviewThread":null},"errors":[{"type":"NOT_FOUND","path":["x"],"locations":[{"line":2,"column":3}],"message":"Could not resolve to a node with the global id of 'PRRT_kwDOe2e1'"}]}`}, "GitHub knows no such review thread on pull request #7 in %[2]s, or the token may not %[1]s it: " + scope},
		{refusalAnswer{http.StatusOK, `{"data":{"addPullRequestReviewThreadReply":null,"resolveReviewThread":null},"errors":[{"type":"UNPROCESSABLE","path":["x"],"locations":[{"line":2,"column":3}],"message":"Pull request is locked."}]}`}, "GitHub refused to %[1]s the thread on pull request #7: Pull request is locked."},
		{refusalAnswer{http.StatusNotFound, `{"message":"Not Found","documentation_url":"https://docs.github.com/graphql"}`}, "No GitHub GraphQL API answers at %[3]s/api/graphql: check the kind and the web address."},
	} {
		for _, act := range []string{"answer", "resolve"} {
			fake, b, ctx := threadWritesFixture(t)
			fake.refusal = &c.answer
			var err error
			if act == "answer" {
				_, err = Provider{}.Reply(ctx, forge.NewClient(), b, token, pullSeven, writtenThread, "x")
			} else {
				err = Provider{}.Resolve(ctx, forge.NewClient(), b, token, pullSeven, writtenThread)
			}
			want := fmt.Sprintf(c.said, act, b.Address(), b.Host.WebURL)
			if err == nil || err.Error() != want {
				t.Fatalf("%s answered %s: %v, want %q", act, c.answer.body, err, want)
			}
		}
	}
}
