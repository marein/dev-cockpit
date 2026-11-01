package gitlab_test

import (
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
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

const replyDiscussion = "f7a73e78f83c1f1a6906214815a12e84dcdb6aa7"

// fakeReply answers the notes and the resolve of a merge request's discussion
// the way GitLab 19.5 does, refusing what refusals names by method and path.
type fakeReply struct {
	mu       sync.Mutex
	requests []string
	bodies   []map[string]any
	refusals map[string]refusal
}

func (f *fakeReply) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.EscapedPath()
	f.requests = append(f.requests, r.Method+" "+path+"?"+r.URL.RawQuery)
	if answer, refused := f.refusals[r.Method+" "+path]; refused {
		w.WriteHeader(answer.code)
		fmt.Fprint(w, answer.body)
		return
	}
	discussion := publishMR + "/discussions/" + replyDiscussion
	note := `{"id":%d,"type":"DiffNote","body":%q,"author":{"id":1,"username":"root","public_email":null,"name":"Administrator","state":"active","locked":false,"web_url":"http://127.0.0.1:3180/root"},"created_at":"2026-10-29T21:30:56.368Z","updated_at":"2026-10-29T21:30:56.368Z","system":false,"noteable_id":145,"noteable_type":"MergeRequest","project_id":83,"commit_id":null,"position":{"base_sha":"187ef498050d33d750de25caf48a70398619c4a1","start_sha":"187ef498050d33d750de25caf48a70398619c4a1","head_sha":"8210c0d21a20884427bef7ec81862e55190b3d77","old_path":"a.txt","new_path":"a.txt","position_type":"text","old_line":null,"new_line":2,"line_range":null},"resolvable":true,"resolved":%v,"resolved_by":null,"resolved_at":null,"suggestions":[],"confidential":false,"internal":false,"imported":false,"imported_from":"none","noteable_iid":1,"commands_changes":{}}`
	switch {
	case r.Header.Get("Authorization") != "Bearer "+token:
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
	case path == discussion+"/notes" && r.Method == http.MethodPost:
		var sent map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &sent)
		f.bodies = append(f.bodies, sent)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, note, 416, sent["body"], false)
	case path == discussion && r.Method == http.MethodPut && r.URL.Query().Get("resolved") == "true":
		fmt.Fprintf(w, `{"id":%q,"individual_note":false,"resolvable":true,"resolved":true,"notes":[`+note+`,`+note+`]}`, replyDiscussion, 415, "New name?", true, 416, "fixed in abc123", true)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 Discussion Not Found"}`)
	}
}

func replyFixture(t *testing.T) (*fakeReply, forge.Binding) {
	t.Helper()
	fake := &fakeReply{refusals: map[string]refusal{}}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return fake, forge.Binding{Host: host(srv.URL), Path: "group/sub/app"}
}

var replyThread = forge.Thread{ID: replyDiscussion, Path: "a.txt", Line: 2, Side: forge.NewSide, Resolvable: true}

// A reply is a note of the discussion with the text as it came, answered as
// the note GitLab made, its page the merge request's with the note's anchor.
func TestAReplyIsANoteOfTheDiscussion(t *testing.T) {
	fake, b := replyFixture(t)
	n, err := gitlab.Provider{}.Reply(publishContext(t), forge.NewClient(), b, token, publishChange, replyThread, "fixed in abc123\n\n`code`")
	if err != nil {
		t.Fatal(err)
	}
	want := forge.Note{ID: "416", Author: "root", Body: "fixed in abc123\n\n`code`", Created: time.Date(2026, 10, 29, 21, 30, 56, 368000000, time.UTC), URL: publishPage + "#note_416"}
	if !n.Created.Equal(want.Created) || n.ID != want.ID || n.Author != want.Author || n.Body != want.Body || n.URL != want.URL {
		t.Fatalf("answered %+v, want %+v", n, want)
	}
	if len(fake.bodies) != 1 || fake.bodies[0]["body"] != "fixed in abc123\n\n`code`" || len(fake.bodies[0]) != 1 {
		t.Fatalf("posted %v", fake.bodies)
	}
}

// A resolve puts the discussion resolved and reads no more of the answer
// than that it went through.
func TestAResolvePutsTheDiscussionResolved(t *testing.T) {
	fake, b := replyFixture(t)
	if err := (gitlab.Provider{}).Resolve(publishContext(t), forge.NewClient(), b, token, publishChange, replyThread); err != nil {
		t.Fatal(err)
	}
	if want := "PUT " + publishMR + "/discussions/" + replyDiscussion + "?resolved=true"; len(fake.requests) != 1 || fake.requests[0] != want {
		t.Fatalf("asked %v, want %s", fake.requests, want)
	}
}

// A token without the scope api, an account that may not, a discussion gone
// and a refusal in GitLab's own words each say what they mean.
func TestTheWritesOnADiscussionSayWhyGitLabRefusedThem(t *testing.T) {
	notes := "POST " + publishMR + "/discussions/" + replyDiscussion + "/notes"
	resolve := "PUT " + publishMR + "/discussions/" + replyDiscussion
	for _, c := range []struct {
		at      string
		answer  refusal
		resolve bool
		said    string
	}{
		{notes, refusal{http.StatusForbidden, scopeAnswer}, false, "The token may not write to GitLab: give it the scope api."},
		{resolve, refusal{http.StatusForbidden, scopeAnswer}, true, "The token may not write to GitLab: give it the scope api."},
		{notes, refusal{http.StatusForbidden, `{"message":"403 Forbidden"}`}, false, "The token's account may not comment on merge request !1 in"},
		{resolve, refusal{http.StatusForbidden, `{"message":"403 Forbidden"}`}, true, "may not resolve threads of merge request !1 in"},
		{notes, refusal{http.StatusNotFound, `{"message":"404 Discussion Not Found"}`}, false, "GitLab knows no thread of merge request !1 in"},
		{resolve, refusal{http.StatusNotFound, `{"message":"404 Discussion Not Found"}`}, true, "GitLab knows no thread of merge request !1 in"},
		{notes, refusal{http.StatusBadRequest, `{"message":"400 Bad request - Note {:note=>[\"is too long (maximum is 1000000 characters)\"]}"}`}, false, "GitLab refused to answer the thread on merge request !1: 400 Bad request - Note"},
	} {
		fake, b := replyFixture(t)
		fake.refusals[c.at] = c.answer
		var err error
		if c.resolve {
			err = gitlab.Provider{}.Resolve(publishContext(t), forge.NewClient(), b, token, publishChange, replyThread)
		} else {
			_, err = gitlab.Provider{}.Reply(publishContext(t), forge.NewClient(), b, token, publishChange, replyThread, "x")
		}
		if err == nil || !strings.Contains(err.Error(), c.said) || strings.Contains(err.Error(), token) {
			t.Fatalf("%s answered %d: %v, want it to say %q", c.at, c.answer.code, err, c.said)
		}
	}
}

// A line opening with a slash goes behind a backslash in a reply, a published
// comment and a description, so GitLab posts it as text instead of running it
// as a quick action and answering without a note: also in a code fence, which
// a quote or an HTML block before it can keep GitLab from reading as one, and
// after carriage returns, which GitLab deletes first. An indented line stays
// as it is, GitLab runs none.
func TestGitLabRunsNoQuickActionOfWhatIsPosted(t *testing.T) {
	sent := "/award :tada:\nfixed in abc123\n/merge\n```\n/usr/bin/env\n```\n /title x\n>>>\n```\n>>>\n/title quote\n```\n<div>\n```\n</div>\n\n/title html\n\r/close\n```sh\n/close"
	want := "\\/award :tada:\nfixed in abc123\n\\/merge\n```\n\\/usr/bin/env\n```\n /title x\n>>>\n```\n>>>\n\\/title quote\n```\n<div>\n```\n</div>\n\n\\/title html\n\\\r/close\n```sh\n\\/close"
	fake, b := replyFixture(t)
	if _, err := (gitlab.Provider{}).Reply(publishContext(t), forge.NewClient(), b, token, publishChange, replyThread, sent); err != nil || fake.bodies[0]["body"] != want {
		t.Fatalf("the reply posted %q, %v", fake.bodies, err)
	}
	published, b := publishFixture(t)
	p := forge.Placed{Draft: forge.Draft{Path: "a.txt", Line: 16, Body: sent}, OldPath: "a.txt"}
	if _, err := (gitlab.Provider{}).PublishNote(publishContext(t), forge.NewClient(), b, token, publishChange, forge.Diff{Head: publishHead, Base: publishBase, Start: publishBase}, p); err != nil || published.bodies[0]["body"] != want {
		t.Fatalf("the comment posted %q, %v", published.bodies, err)
	}
	opened := &fakeOpen{}
	srv := httptest.NewServer(opened)
	defer srv.Close()
	if _, err := open(t, srv.URL, forge.NewChange{Target: "main", Title: "Fix", Body: sent}); err != nil || opened.sent[0]["description"] != want {
		t.Fatalf("the description posted %q, %v", opened.sent, err)
	}
}
