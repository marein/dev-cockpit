package gitlab_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

const (
	latestHead = "5e6872f2d2520efbf60205f5f0249f9bb0b489d0"
	earlyHead  = "345198abe6a66583445c27c7d9b060d6b7e23419"
	mrPage     = "https://gitlab.example.com/group/sub/app/-/merge_requests/4"
)

// The answers of a GitLab 19.5 as its API sends them: the merge request
// itself with the refs of its latest diff, and a note of its discussions,
// which a resolvable one answers with its resolved state and a diff note
// with its position, the head of the version of the diff it stands on and
// its line on either side; null leaves a line out.
func singleMergeRequestAnswer() string {
	return fmt.Sprintf(`{"id":25,"iid":4,"project_id":2,"title":"Review me","state":"opened","source_branch":"feature/login","target_branch":"main","sha":%q,"web_url":%q,"diff_refs":{"base_sha":"4b449edfa7d092dd25836adba8055a2e4223fbe8","head_sha":%q,"start_sha":"4b449edfa7d092dd25836adba8055a2e4223fbe8"},"source_project_id":2,"target_project_id":2,"user_notes_count":9}`,
		latestHead, mrPage, latestHead)
}

type noteAt struct {
	kind, oldPath, newPath, head string
	oldLine, newLine             int
}

func noteAnswer(id int, author, body string, system, resolvable, resolved bool, at *noteAt) string {
	kind, position := "null", ""
	if at != nil {
		line := func(n int) string {
			if n == 0 {
				return "null"
			}
			return fmt.Sprint(n)
		}
		kind = `"DiffNote"`
		position = fmt.Sprintf(`"commit_id":null,"position":{"base_sha":"4b449edfa7d092dd25836adba8055a2e4223fbe8","start_sha":"4b449edfa7d092dd25836adba8055a2e4223fbe8","head_sha":%q,"old_path":%q,"new_path":%q,"position_type":%q,"old_line":%s,"new_line":%s,"line_range":null},`,
			at.head, at.oldPath, at.newPath, at.kind, line(at.oldLine), line(at.newLine))
	} else if resolvable {
		kind = `"DiscussionNote"`
	}
	resolution := `"resolvable":false,`
	if resolvable {
		resolution = fmt.Sprintf(`"resolvable":true,"resolved":%t,"resolved_by":null,"resolved_at":null,`, resolved)
	}
	return fmt.Sprintf(`{"id":%d,"type":%s,"body":%q,"author":{"id":1,"username":%q,"public_email":null,"name":"Administrator","state":"active","locked":false,"avatar_url":"https://www.gravatar.com/avatar/b604?s=80&d=identicon","web_url":"https://gitlab.example.com/%s"},"created_at":"2026-10-28T23:03:%02d.551Z","updated_at":"2026-10-28T23:03:31.551Z","system":%t,"noteable_id":25,"noteable_type":"MergeRequest","project_id":2,%s%s"suggestions":[],"confidential":false,"internal":false,"imported":false,"imported_from":"none","noteable_iid":4,"commands_changes":{}}`,
		id, kind, body, author, author, id%60, system, position, resolution)
}

func discussionAnswer(id string, individual bool, notes ...string) string {
	return fmt.Sprintf(`{"id":%q,"individual_note":%t,"resolvable":false,"notes":[%s]}`, id, individual, strings.Join(notes, ","))
}

// fakeDiscussions answers the merge request 4 of group/sub/app and its
// discussions in pages, for the token alone, and keeps what it was asked.
type fakeDiscussions struct {
	pages []string
	more  bool
	asks  []string
}

func (f *fakeDiscussions) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.asks = append(f.asks, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
		return
	}
	switch r.URL.EscapedPath() {
	case projectAt + "/merge_requests/4":
		fmt.Fprint(w, singleMergeRequestAnswer())
	case projectAt + "/merge_requests/4/discussions":
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		if page < len(f.pages) || f.more {
			w.Header().Set("X-Next-Page", fmt.Sprint(page+1))
		}
		if page <= len(f.pages) {
			fmt.Fprint(w, f.pages[page-1])
			return
		}
		fmt.Fprint(w, list(discussionAnswer(fmt.Sprintf("more%d", page), true, noteAnswer(900+page, "root", "more", false, false, false, nil))))
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 Project Not Found"}`)
	}
}

func threads(t *testing.T, webURL, tok string) (forge.Review, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	review, err := gitlab.Provider{}.Threads(ctx, forge.NewClient(), forge.Binding{Host: host(webURL), Path: "group/sub/app"}, tok, 4)
	if err != nil && strings.Contains(err.Error(), tok) {
		t.Fatalf("the error names the token: %v", err)
	}
	return review, err
}

func written(second int) time.Time {
	return time.Date(2026, 10, 28, 23, 3, second, 551_000_000, time.UTC)
}

func gitlabNote(id int, author, body string) forge.Note {
	return forge.Note{ID: fmt.Sprint(id), Author: author, Body: body, Created: written(id), URL: fmt.Sprintf("%s#note_%d", mrPage, id)}
}

// The discussions over every page become threads: the diff notes on the
// diff, at their line on the new side, on the old side, or on the new side
// for a line both share, on a whole file without a line, outdated where they
// stand on an earlier version of the diff than the latest; the rest general,
// a thread with replies and resolvable as much as a single comment that is
// not. GitLab's system notes stay out, inside a thread and as one of their
// own, and a thread is resolved when every note that can be is. A note links
// its place on the merge request's page, and the project path travels as
// one encoded segment.
func TestTheThreadsAreTheDiscussionsOfTheMergeRequest(t *testing.T) {
	src := func(head string, oldLine, newLine int) *noteAt {
		return &noteAt{kind: "text", oldPath: "src/login.go", newPath: "src/login.go", head: head, oldLine: oldLine, newLine: newLine}
	}
	f := &fakeDiscussions{pages: []string{
		list(
			discussionAnswer("d1", false,
				noteAnswer(29, "root", "New name bb?", false, true, false, src(earlyHead, 0, 4)),
				noteAnswer(33, "alice", "Reply: yes, bb.", false, true, false, src(earlyHead, 0, 4)),
				noteAnswer(38, "root", "changed this line in [version 2 of the diff](/group/sub/app/-/merge_requests/4/diffs?diff_id=78)", true, false, false, src(earlyHead, 0, 4))),
			discussionAnswer("d2", false, noteAnswer(30, "root", "Why drop b?", false, true, false, &noteAt{kind: "text", oldPath: "src/old.go", newPath: "src/login.go", head: latestHead, oldLine: 4})),
			discussionAnswer("d3", false, noteAnswer(31, "root", "Context c", false, true, true, src(latestHead, 5, 5))),
		),
		list(
			discussionAnswer("d4", false, noteAnswer(37, "root", "File note", false, true, false, &noteAt{kind: "file", oldPath: "src/login.go", newPath: "src/login.go", head: latestHead})),
			discussionAnswer("d5", false,
				noteAnswer(34, "root", "General thread", false, true, true, nil),
				noteAnswer(35, "alice", "General reply", false, true, false, nil)),
			discussionAnswer("d6", true, noteAnswer(36, "root", "Individual note\nwith a second line", false, false, false, nil)),
			discussionAnswer("d7", true, noteAnswer(40, "root", "added 1 commit", true, false, false, nil)),
			discussionAnswer("d8", true, noteAnswer(41, "root", "approved this merge request", true, false, false, nil)),
		),
	}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	review, err := threads(t, srv.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	want := forge.Review{
		Diff: []forge.Thread{
			{ID: "d1", Path: "src/login.go", Line: 4, Side: forge.NewSide, Commit: earlyHead, Outdated: true, Resolvable: true, Notes: []forge.Note{gitlabNote(29, "root", "New name bb?"), gitlabNote(33, "alice", "Reply: yes, bb.")}},
			{ID: "d2", Path: "src/old.go", Line: 4, Side: forge.OldSide, Commit: "4b449edfa7d092dd25836adba8055a2e4223fbe8", Resolvable: true, Notes: []forge.Note{gitlabNote(30, "root", "Why drop b?")}},
			{ID: "d3", Path: "src/login.go", Line: 5, Side: forge.NewSide, Commit: latestHead, Resolvable: true, Resolved: true, Notes: []forge.Note{gitlabNote(31, "root", "Context c")}},
			{ID: "d4", Path: "src/login.go", Resolvable: true, Notes: []forge.Note{gitlabNote(37, "root", "File note")}},
		},
		General: []forge.Thread{
			{ID: "d5", Resolvable: true, Notes: []forge.Note{gitlabNote(34, "root", "General thread"), gitlabNote(35, "alice", "General reply")}},
			{ID: "d6", Notes: []forge.Note{gitlabNote(36, "root", "Individual note\nwith a second line")}},
		},
	}
	if !reflect.DeepEqual(review, want) {
		t.Fatalf("the review reads\n%+v\nwant\n%+v", review, want)
	}
	asked := strings.Join(f.asks, " ")
	wantAsked := projectAt + "/merge_requests/4? " + projectAt + "/merge_requests/4/discussions?page=1&per_page=100 " + projectAt + "/merge_requests/4/discussions?page=2&per_page=100"
	if asked != wantAsked {
		t.Fatalf("asked %s\nwant %s", asked, wantAsked)
	}
}

// The discussions stop at the bound of pages, which says so; a merge
// request without discussions is an answer.
func TestTheThreadsStopAtTheirBound(t *testing.T) {
	f := &fakeDiscussions{more: true}
	srv := httptest.NewServer(f)
	defer srv.Close()
	review, err := threads(t, srv.URL, token)
	if err != nil || !review.Partial || len(review.General) != 10 || len(f.asks) != 11 {
		t.Fatalf("got %d general threads, partial %t, %v, asked %d times", len(review.General), review.Partial, err, len(f.asks))
	}
	f = &fakeDiscussions{pages: []string{list()}}
	srv2 := httptest.NewServer(f)
	defer srv2.Close()
	if review, err := threads(t, srv2.URL, token); err != nil || review.Partial || review.Diff != nil || review.General != nil {
		t.Fatalf("no discussions: %+v, %v", review, err)
	}
}

// A refused read says what it means for the token and the project.
func TestThreadFailuresAreSaid(t *testing.T) {
	srv := httptest.NewServer(&fakeDiscussions{})
	defer srv.Close()
	if _, err := threads(t, srv.URL, "glpat-n0t-the-t0ken"); err == nil || err.Error() != "GitLab refused the token: it is wrong, expired or revoked." {
		t.Fatalf("a wrong token: %v", err)
	}
	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"403 Forbidden"}`)
	}))
	defer gone.Close()
	if _, err := threads(t, gone.URL, token); err == nil || !strings.HasPrefix(err.Error(), "The token may not read ") {
		t.Fatalf("a project the token may not read: %v", err)
	}
	deleted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 Not found"}`)
	}))
	defer deleted.Close()
	if _, err := threads(t, deleted.URL, token); err == nil || !strings.HasPrefix(err.Error(), "GitLab knows no merge request !4 in ") {
		t.Fatalf("a merge request deleted after the status named it: %v", err)
	}
}
