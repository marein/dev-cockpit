package gitlab_test

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
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

const (
	publishBase  = "49cfa377130a8d11d03ac310ff161d41760fe5e8"
	publishHead  = "d81fe9fda11ca9aaac272bd8cbbfbdeeb5e731b7"
	publishMR    = "/api/v4/projects/group%2Fsub%2Fapp/merge_requests/1"
	publishPage  = "https://gitlab.example.com/group/sub/app/-/merge_requests/1"
	publishPatch = "@@ -7,12 +7,13 @@ line6\n line7\n line8\n line9\n-line10\n+line10 changed\n line11\n line12\n line13\n line14\n line15\n+added after 15\n line16\n line17\n line18\n"
)

// fakePublish answers a merge request the way GitLab 19.5 does: its diff refs,
// null while GitLab makes the version, its diffs over two pages, a changed
// file, a renamed one and a deleted one, and a new discussion on a position
// with its note, refusing what refusals names by path.
type fakePublish struct {
	mu       sync.Mutex
	refs     bool
	bodies   []map[string]any
	refusals map[string]refusal
}

func (f *fakePublish) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.EscapedPath()
	if answer, refused := f.refusals[r.Method+" "+path]; refused {
		w.WriteHeader(answer.code)
		fmt.Fprint(w, answer.body)
		return
	}
	switch {
	case r.Header.Get("Authorization") != "Bearer "+token:
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
	case path == publishMR && r.Method == http.MethodGet:
		refs := "null"
		if f.refs {
			refs = fmt.Sprintf(`{"base_sha":%q,"head_sha":%q,"start_sha":%q}`, publishBase, publishHead, publishBase)
		}
		fmt.Fprintf(w, `{"id":139,"iid":1,"project_id":2,"title":"probe","state":"opened","sha":%q,"web_url":%q,"diff_refs":%s}`, publishHead, publishPage, refs)
	case path == publishMR+"/diffs" && r.URL.Query().Get("page") == "1":
		w.Header().Set("X-Next-Page", "2")
		fmt.Fprintf(w, `[{"diff":%q,"collapsed":false,"too_large":false,"new_path":"a.txt","old_path":"a.txt","a_mode":"100644","b_mode":"100644","new_file":false,"renamed_file":false,"deleted_file":false,"generated_file":false}]`, publishPatch)
	case path == publishMR+"/diffs":
		w.Header().Set("X-Next-Page", "")
		fmt.Fprint(w, `[{"diff":"@@ -1,2 +1,2 @@\n x\n-y\n+z\n","new_path":"src/new.go","old_path":"src/old.go","new_file":false,"renamed_file":true,"deleted_file":false},{"diff":"@@ -1 +0,0 @@\n-gone\n","new_path":"gone.txt","old_path":"gone.txt","new_file":false,"renamed_file":false,"deleted_file":true},{"diff":"","new_path":"big.json","old_path":"big.json","collapsed":true,"too_large":true,"deleted_file":false}]`)
	case path == publishMR+"/discussions" && r.Method == http.MethodPost:
		var sent map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &sent)
		f.bodies = append(f.bodies, sent)
		position, _ := sent["position"].(map[string]any)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":"911e4861dcb1eb17548d492248368aeee08a78e7","individual_note":false,"resolvable":true,"resolved":false,"notes":[{"id":368,"type":"DiffNote","body":%q,"author":{"id":1,"username":"root","name":"Administrator","state":"active","web_url":"http://127.0.0.1:3180/root"},"created_at":"2026-10-29T18:32:29.871Z","updated_at":"2026-10-29T18:32:29.871Z","system":false,"noteable_id":139,"noteable_type":"MergeRequest","project_id":78,"commit_id":null,"position":{"base_sha":%q,"start_sha":%q,"head_sha":%q,"old_path":%q,"new_path":%q,"position_type":"text","old_line":%v,"new_line":%v,"line_range":null},"resolvable":true,"resolved":false,"resolved_by":null,"resolved_at":null,"suggestions":[],"confidential":false,"internal":false,"imported":false,"imported_from":"none","noteable_iid":1,"commands_changes":{}}]}`,
			sent["body"], publishBase, publishBase, publishHead, position["old_path"], position["new_path"], orNull(position["old_line"]), position["new_line"])
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 Not found"}`)
	}
}

func orNull(v any) any {
	if v == nil {
		return "null"
	}
	return v
}

func publishFixture(t *testing.T) (*fakePublish, forge.Binding) {
	t.Helper()
	fake := &fakePublish{refs: true, refusals: map[string]refusal{}}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return fake, forge.Binding{Host: host(srv.URL), Path: "group/sub/app"}
}

func publishContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

var publishChange = forge.Change{Number: 1, State: forge.Open, URL: publishPage, Head: publishHead}

// The diff is the latest version's: its diff refs and the lines of the new
// side each file's hunks show, read over every page, a renamed file under its
// new path with its old one, without a deleted file, a collapsed one with no
// line.
func TestTheDiffOfAMergeRequestIsItsLatestVersion(t *testing.T) {
	_, b := publishFixture(t)
	d, err := gitlab.Provider{}.ChangeDiff(publishContext(t), forge.NewClient(), b, token, publishChange)
	if err != nil {
		t.Fatal(err)
	}
	if d.Head != publishHead || d.Base != publishBase || d.Start != publishBase || d.Partial || len(d.Files) != 3 {
		t.Fatalf("the diff reads %+v", d)
	}
	if f := d.Files["a.txt"]; f.OldPath != "a.txt" || f.Lines[10] != 0 || f.Lines[12] != 12 || f.Lines[17] != 16 || len(f.Lines) != 13 {
		t.Fatalf("the changed file reads %+v", f)
	}
	if f := d.Files["src/new.go"]; f.OldPath != "src/old.go" || !maps.Equal(f.Lines, map[int]int{1: 1, 2: 0}) {
		t.Fatalf("the renamed file reads %+v", f)
	}
	if f := d.Files["big.json"]; len(f.Lines) != 0 {
		t.Fatalf("the collapsed file reads %+v", f)
	}
}

// A merge request GitLab is still making the diff of says so, and so does a
// GitLab without the list of diffs, older than 15.7.
func TestADiffGitLabCannotListYetIsSaidPlainly(t *testing.T) {
	fake, b := publishFixture(t)
	fake.refs = false
	_, err := gitlab.Provider{}.ChangeDiff(publishContext(t), forge.NewClient(), b, token, publishChange)
	if err == nil || err.Error() != "GitLab is still making the diff of merge request !1, publish again in a moment." {
		t.Fatalf("a merge request without diff refs answered %v", err)
	}
	fake.refs = true
	fake.refusals["GET "+publishMR+"/diffs"] = refusal{http.StatusNotFound, `{"error":"404 Not Found"}`}
	_, err = gitlab.Provider{}.ChangeDiff(publishContext(t), forge.NewClient(), b, token, publishChange)
	if err == nil || err.Error() != "GitLab lists no diff of merge request !1, publishing review comments needs GitLab 15.7 or later." {
		t.Fatalf("a GitLab without the diffs answered %v", err)
	}
}

// A note on a line the change adds names its new line alone, one on a line
// both sides share its old one too, which GitLab otherwise refuses, both on
// the latest version of the diff; the note links its place on the page.
func TestANoteStandsOnItsLineOfTheLatestVersion(t *testing.T) {
	fake, b := publishFixture(t)
	d := forge.Diff{Head: publishHead, Base: publishBase, Start: publishBase}
	for _, p := range []forge.Placed{
		{Draft: forge.Draft{Path: "a.txt", Line: 16, Body: "added"}, OldPath: "a.txt"},
		{Draft: forge.Draft{Path: "a.txt", Line: 12, Body: "around"}, OldPath: "a.txt", OldLine: 12},
	} {
		note, err := gitlab.Provider{}.PublishNote(publishContext(t), forge.NewClient(), b, token, publishChange, d, p)
		if err != nil || note.ID != "368" || note.Author != "root" || note.Body != p.Body || note.URL != publishPage+"#note_368" || note.Created.IsZero() {
			t.Fatalf("the note of %+v reads %+v, %v", p, note, err)
		}
	}
	want := []string{
		`{"body":"added","position":{"base_sha":"` + publishBase + `","head_sha":"` + publishHead + `","new_line":16,"new_path":"a.txt","old_path":"a.txt","position_type":"text","start_sha":"` + publishBase + `"}}`,
		`{"body":"around","position":{"base_sha":"` + publishBase + `","head_sha":"` + publishHead + `","new_line":12,"new_path":"a.txt","old_line":12,"old_path":"a.txt","position_type":"text","start_sha":"` + publishBase + `"}}`,
	}
	for i, sent := range fake.bodies {
		raw, _ := json.Marshal(sent)
		if string(raw) != want[i] {
			t.Fatalf("note %d was sent as %s", i, raw)
		}
	}
}

// A note GitLab refuses says why in a sentence: a token without the scope
// api, an account that may not comment, a position GitLab refuses in its own
// words, a merge request it does not know.
func TestARefusedNoteIsSaidPlainly(t *testing.T) {
	p := forge.Placed{Draft: forge.Draft{Path: "a.txt", Line: 12, Body: "x"}, OldPath: "a.txt"}
	discussions := "POST " + publishMR + "/discussions"
	for _, c := range []struct {
		code int
		body string
		want string
	}{
		{http.StatusForbidden, scopeAnswer, "The token may not write to GitLab: give it the scope api."},
		{http.StatusForbidden, `{"message":"403 Forbidden"}`, "The token's account may not comment on merge request !1 in "},
		{http.StatusBadRequest, `{"message":"400 Bad request - Note {:line_code=>[\"can't be blank\", \"must be a valid line code\"]}"}`, `GitLab refused to comment on merge request !1: 400 Bad request - Note {:line_code=>["can't be blank", "must be a valid line code"]}.`},
		{http.StatusNotFound, `{"message":"404 Not found"}`, "GitLab knows no merge request !1 in "},
	} {
		fake, b := publishFixture(t)
		fake.refusals[discussions] = refusal{c.code, c.body}
		_, err := gitlab.Provider{}.PublishNote(publishContext(t), forge.NewClient(), b, token, publishChange, forge.Diff{Head: publishHead}, p)
		if err == nil || !strings.HasPrefix(err.Error(), c.want) || strings.Contains(err.Error(), token) {
			t.Errorf("%d %s answered %v", c.code, c.body, err)
		}
	}
}
