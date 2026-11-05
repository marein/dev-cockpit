package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/activity"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/linecomment"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// commentGuests is a server with the real guest routes and the owner's
// comment routes over the projects shop and blog, a note of the owner's in
// shop, and two links: Anna's to shop and blog granting Comment, Bert's to
// shop read only.
type commentGuests struct {
	s     *Server
	r     *gin.Engine
	anna  string
	bert  string
	links map[string]share.Share
	owner linecomment.Comment
	state string
	news  []GuestComment
}

func newCommentGuests(t *testing.T) *commentGuests {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	state := t.TempDir()
	for _, name := range []string{"shop", "blog"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{
		projects:     project.NewRepository(root, nil),
		shares:       share.NewStore(t.TempDir()),
		activity:     activity.NewTracker(),
		lineComments: linecomment.New(state),
		bus:          eventbus.New(),
		quickOpen:    filesystem.NewQuickOpenCache(),
	}
	r := gin.New()
	r.SetHTMLTemplate(render.HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil))
	r.NoRoute(s.handleNotFound)
	s.registerShareRoutes(r)
	r.POST("/projects/:name/editor/comments", s.handleEditorCommentSave)
	r.POST("/projects/:name/editor/comments/delete", s.handleEditorCommentsDelete)
	r.POST("/projects/:name/editor/comments/move", s.handleEditorCommentsMove)
	r.POST("/projects/:name/editor/comments/reply", s.handleEditorCommentReply)
	r.POST("/projects/:name/editor/comments/reply/delete", s.handleEditorCommentReplyDelete)
	r.POST("/projects/:name/editor/comments/resolve", s.handleEditorCommentResolve)
	f := &commentGuests{s: s, r: r, links: map[string]share.Share{}, state: state}
	s.SetGuestComment(func(note GuestComment) { f.news = append(f.news, note) })
	for name, rs := range map[string][]share.Resource{
		"Anna": {
			{Kind: share.KindProject, Target: "shop", Permissions: []share.Permission{share.Comments}},
			{Kind: share.KindProject, Target: "blog", Permissions: []share.Permission{share.Comments}},
		},
		"Bert": linkTo("shop"),
	} {
		sh, token, err := s.shares.Create(name, rs, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		f.links[name] = sh
		if name == "Anna" {
			f.anna = token
		} else {
			f.bert = token
		}
	}
	owner, _, err := s.lineComments.Save("shop", linecomment.Comment{Path: "a.txt", Line: 1, LineText: "one", Text: "the owner's"}, linecomment.Writer{})
	if err != nil {
		t.Fatal(err)
	}
	f.owner = owner
	return f
}

// send posts JSON to a path of the owner's under a link, empty for the owner,
// with the link's CSRF token.
func (f *commentGuests) send(token, path string, body any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	target := path
	if token != "" {
		target = sharePathPrefix + token + path
	}
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-CSRF-Token", guestCSRF(token))
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

func (f *commentGuests) read(t *testing.T, token string) (string, []lineCommentView) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, sharePathPrefix+token+"/projects/shop/editor/comments", nil)
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	var answer struct {
		Comments []lineCommentView `json:"comments"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil {
		t.Fatalf("reading the comments = %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String(), answer.Comments
}

func (f *commentGuests) note(t *testing.T, id string) (linecomment.Comment, bool) {
	t.Helper()
	for _, c := range f.s.lineComments.List("shop") {
		if c.ID == id {
			return c, true
		}
	}
	return linecomment.Comment{}, false
}

func savedComment(t *testing.T, rec *httptest.ResponseRecorder) lineCommentView {
	t.Helper()
	var answer struct {
		Comment lineCommentView `json:"comment"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil || answer.Comment.ID == "" {
		t.Fatalf("the save = %d %s", rec.Code, rec.Body.String())
	}
	return answer.Comment
}

const shopComments = "/projects/shop/editor/comments"

// A guest whose link grants Comment adds a note under the link's name, edits,
// moves and deletes it, and reads it as its own beside the owner's, never
// the id of the link. A link that grants nothing writes nothing.
func TestAGuestCommentsWhereItsLinkGrantsComment(t *testing.T) {
	f := newCommentGuests(t)
	for path, body := range map[string]any{
		"":        map[string]any{"path": "a.txt", "line": 2, "text": "bert's"},
		"/delete": map[string]any{"ids": []string{f.owner.ID}},
		"/move":   map[string]any{"path": "a.txt", "comments": []map[string]any{{"id": f.owner.ID, "line": 3, "lineText": "three"}}},
	} {
		if rec := f.send(f.bert, shopComments+path, body); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "The link does not allow that.") {
			t.Fatalf("a read only link's %q = %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if list := f.s.lineComments.List("shop"); len(list) != 1 || list[0].Line != 1 {
		t.Fatalf("a read only link changed the notes: %+v", list)
	}
	rec := f.send(f.anna, shopComments, map[string]any{"path": "a.txt", "line": 2, "lineText": "two", "text": "anna's"})
	added := savedComment(t, rec)
	if added.Author != "Anna" || !added.Mine || strings.Contains(rec.Body.String(), f.links["Anna"].ID) {
		t.Fatalf("the guest's note reads %s", rec.Body.String())
	}
	if stored, _ := f.note(t, added.ID); stored.Link != f.links["Anna"].ID || stored.Author != "Anna" {
		t.Fatalf("the guest's note is stored as %+v", stored)
	}
	edited := savedComment(t, f.send(f.anna, shopComments, map[string]any{"id": added.ID, "path": "a.txt", "line": 2, "lineText": "two", "text": "anna's, edited"}))
	if edited.Text != "anna's, edited" || edited.Author != "Anna" || !edited.Mine {
		t.Fatalf("the edit reads %+v", edited)
	}
	if rec := f.send(f.anna, shopComments+"/move", map[string]any{"path": "a.txt", "comments": []map[string]any{{"id": added.ID, "line": 3, "lineText": "three"}}}); rec.Code != http.StatusOK {
		t.Fatalf("the move = %d %s", rec.Code, rec.Body.String())
	}
	if moved, _ := f.note(t, added.ID); moved.Line != 3 {
		t.Fatalf("the move left %+v", moved)
	}
	for name, token := range map[string]string{"Anna": f.anna, "Bert": f.bert} {
		raw, list := f.read(t, token)
		if strings.Contains(raw, f.links["Anna"].ID) || strings.Contains(raw, `"link"`) || len(list) != 2 {
			t.Fatalf("%s reads %s", name, raw)
		}
		for _, c := range list {
			wantMine := name == "Anna" && c.ID == added.ID
			if c.Mine != wantMine || (c.ID == added.ID) != (c.Author == "Anna") {
				t.Fatalf("%s reads %+v", name, c)
			}
		}
	}
	if rec := f.send(f.anna, shopComments+"/delete", map[string]any{"ids": []string{added.ID}}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"removed":1`) {
		t.Fatalf("the delete = %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := f.note(t, added.ID); ok {
		t.Fatal("the guest's note stands after its delete")
	}
}

// Whatever a hand built request names, a guest changes no note of the
// owner's and none of another link, and the owner changes every note, a
// guest's keeping its author.
func TestAGuestChangesOnlyTheNotesOfItsOwnLink(t *testing.T) {
	f := newCommentGuests(t)
	if err := f.s.shares.Apply(f.links["Bert"].ID, share.Change{Grants: []share.Resource{{Kind: share.KindProject, Target: "shop", Permissions: []share.Permission{share.Comments}}}}); err != nil {
		t.Fatal(err)
	}
	bert := savedComment(t, f.send(f.bert, shopComments, map[string]any{"path": "a.txt", "line": 2, "text": "bert's"}))
	anna := savedComment(t, f.send(f.anna, shopComments, map[string]any{"path": "a.txt", "line": 3, "text": "anna's"}))
	for _, id := range []string{f.owner.ID, bert.ID} {
		if rec := f.send(f.anna, shopComments, map[string]any{"id": id, "path": "a.txt", "line": 1, "text": "taken"}); rec.Code != http.StatusForbidden {
			t.Fatalf("editing %s = %d %s", id, rec.Code, rec.Body.String())
		}
	}
	f.send(f.anna, shopComments+"/move", map[string]any{"path": "a.txt", "comments": []map[string]any{{"id": f.owner.ID, "line": 3, "lineText": "x"}, {"id": bert.ID, "line": 1, "lineText": "x"}}})
	for _, body := range []map[string]any{
		{"ids": []string{f.owner.ID, bert.ID}},
		{"paths": []string{"a.txt"}},
		{"outdated": true},
		{"all": true},
	} {
		f.send(f.anna, shopComments+"/delete", body)
	}
	for _, want := range []linecomment.Comment{f.owner, {ID: bert.ID, Line: 2, Text: "bert's", Author: "Bert"}} {
		got, ok := f.note(t, want.ID)
		if !ok || got.Line != want.Line || got.Text != want.Text || got.Author != want.Author {
			t.Fatalf("Anna reached %+v: %+v, %v", want, got, ok)
		}
	}
	if _, ok := f.note(t, anna.ID); ok {
		t.Fatal("Anna's delete of all left her own note")
	}
	edited := savedComment(t, f.send("", shopComments, map[string]any{"id": bert.ID, "path": "a.txt", "line": 2, "text": "the owner edited it"}))
	if edited.Author != "Bert" || edited.Mine {
		t.Fatalf("the owner's edit of a guest's note reads %+v", edited)
	}
	if rec := f.send("", shopComments+"/delete", map[string]any{"ids": []string{bert.ID}}); !strings.Contains(rec.Body.String(), `"removed":1`) {
		t.Fatalf("the owner's delete of a guest's note = %s", rec.Body.String())
	}
}

// A guest's write needs the link's CSRF token, never comes over the local
// socket, and is refused the moment Comment is taken away, the project goes
// off the link or the link ends.
func TestAGuestsCommentEndsWithItsGrant(t *testing.T) {
	f := newCommentGuests(t)
	add := map[string]any{"path": "a.txt", "line": 2, "text": "anna's"}
	plain := httptest.NewRequest(http.MethodPost, sharePathPrefix+f.anna+shopComments, strings.NewReader(`{"path":"a.txt","line":2,"text":"x"}`))
	plain.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, plain)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("without the CSRF token = %d", rec.Code)
	}
	local := httptest.NewRequest(http.MethodPost, sharePathPrefix+f.anna+shopComments, strings.NewReader(`{"path":"a.txt","line":2,"text":"x"}`))
	local.Header.Set("Content-Type", "application/json")
	local.Header.Set("X-CSRF-Token", guestCSRF(f.anna))
	local = local.WithContext(context.WithValue(local.Context(), localCallKey, true))
	rec = httptest.NewRecorder()
	f.r.ServeHTTP(rec, local)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "local socket") {
		t.Fatalf("over the local socket = %d %s", rec.Code, rec.Body.String())
	}
	if list := f.s.lineComments.List("shop"); len(list) != 1 {
		t.Fatalf("a refused write landed: %+v", list)
	}
	id := f.links["Anna"].ID
	if err := f.s.shares.Apply(id, share.Change{Grants: linkTo("shop")}); err != nil {
		t.Fatal(err)
	}
	if rec := f.send(f.anna, shopComments, add); rec.Code != http.StatusForbidden {
		t.Fatalf("after taking Comment away = %d", rec.Code)
	}
	if rec := f.send(f.anna, "/projects/blog/editor/comments", add); rec.Code != http.StatusOK {
		t.Fatalf("blog lost its grant with shop's: %d %s", rec.Code, rec.Body.String())
	}
	if err := f.s.shares.Apply(id, share.Change{Grants: []share.Resource{{Kind: share.KindProject, Target: "shop", Permissions: []share.Permission{share.Comments}}}}); err != nil {
		t.Fatal(err)
	}
	savedComment(t, f.send(f.anna, shopComments, add))
	if err := f.s.shares.Apply(id, share.Change{Remove: linkTo("shop")}); err != nil {
		t.Fatal(err)
	}
	if rec := f.send(f.anna, shopComments, add); rec.Code != http.StatusNotFound {
		t.Fatalf("after shop left the link = %d", rec.Code)
	}
	if err := f.s.shares.Revoke(id); err != nil {
		t.Fatal(err)
	}
	if rec := f.send(f.anna, "/projects/blog/editor/comments", add); rec.Code != http.StatusNotFound {
		t.Fatalf("after the revoke = %d", rec.Code)
	}
}

// A guest whose link grants Comment replies to every thread under the link's
// name, edits and deletes its own replies alone, resolves only a thread it
// opened and deletes one only while every note of it is its own; each new
// thread and reply of a guest is news for the owner, an edit, a resolve and
// the owner's own notes are none. A read only link replies to nothing, and a
// reply to a thread gone answers gone.
func TestAGuestRepliesWhereItsLinkGrantsComment(t *testing.T) {
	f := newCommentGuests(t)
	reply := shopComments + "/reply"
	if rec := f.send(f.bert, reply, map[string]any{"thread": f.owner.ID, "text": "bert's"}); rec.Code != http.StatusForbidden {
		t.Fatalf("a read only link's reply = %d %s", rec.Code, rec.Body.String())
	}
	answered := savedComment(t, f.send(f.anna, reply, map[string]any{"thread": f.owner.ID, "text": "anna's reply"}))
	if len(answered.Replies) != 1 || answered.Replies[0].Author != "Anna" || !answered.Replies[0].Mine || answered.Mine {
		t.Fatalf("the reply reads %+v", answered)
	}
	annasReply := answered.Replies[0].ID
	ownerReply := savedComment(t, f.send("", reply, map[string]any{"thread": f.owner.ID, "text": "the owner's answer"})).Replies[1].ID
	if rec := f.send(f.anna, reply, map[string]any{"thread": f.owner.ID, "id": ownerReply, "text": "taken"}); rec.Code != http.StatusForbidden {
		t.Fatalf("Anna's edit of the owner's reply = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.send(f.anna, reply+"/delete", map[string]any{"thread": f.owner.ID, "id": ownerReply}); rec.Code != http.StatusForbidden {
		t.Fatalf("Anna's delete of the owner's reply = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.send(f.anna, shopComments+"/resolve", map[string]any{"id": f.owner.ID, "resolved": true}); rec.Code != http.StatusForbidden {
		t.Fatalf("Anna's resolve of the owner's thread = %d %s", rec.Code, rec.Body.String())
	}
	edited := savedComment(t, f.send(f.anna, reply, map[string]any{"thread": f.owner.ID, "id": annasReply, "text": "anna's reply, edited"}))
	if edited.Replies[0].Text != "anna's reply, edited" || edited.Replies[0].Author != "Anna" {
		t.Fatalf("Anna's edit reads %+v", edited.Replies)
	}
	opened := savedComment(t, f.send(f.anna, shopComments, map[string]any{"path": "a.txt", "line": 2, "text": "anna's thread"}))
	resolved := savedComment(t, f.send(f.anna, shopComments+"/resolve", map[string]any{"id": opened.ID, "resolved": true}))
	if !resolved.Resolved || !resolved.Deletes {
		t.Fatalf("Anna's resolve of her thread reads %+v", resolved)
	}
	if joined := savedComment(t, f.send("", reply, map[string]any{"thread": opened.ID, "text": "the owner joins"})); !joined.Deletes {
		t.Fatalf("the owner may not delete Anna's thread: %+v", joined)
	}
	for name, token := range map[string]string{"Anna": f.anna, "Bert": f.bert} {
		if _, list := f.read(t, token); len(list) != 2 || list[0].Deletes || list[1].Deletes {
			t.Fatalf("%s may delete a thread with a note of somebody else: %+v", name, list)
		}
	}
	if rec := f.send(f.anna, shopComments+"/delete", map[string]any{"ids": []string{opened.ID}}); !strings.Contains(rec.Body.String(), `"removed":0`) {
		t.Fatalf("Anna's delete of a thread the owner answered = %s", rec.Body.String())
	}
	if after := savedComment(t, f.send(f.anna, reply+"/delete", map[string]any{"thread": f.owner.ID, "id": annasReply})); len(after.Replies) != 1 || after.Replies[0].ID != ownerReply {
		t.Fatalf("Anna's delete of her reply leaves %+v", after.Replies)
	}
	want := []GuestComment{
		{Project: "shop", Guest: "Anna", Path: "a.txt", Line: 1, Text: "anna's reply", Reply: true},
		{Project: "shop", Guest: "Anna", Path: "a.txt", Line: 2, Text: "anna's thread"},
	}
	if len(f.news) != len(want) || f.news[0] != want[0] || f.news[1] != want[1] {
		t.Fatalf("the news reads %+v", f.news)
	}
	raw, list := f.read(t, f.bert)
	if strings.Contains(raw, f.links["Anna"].ID) || strings.Contains(raw, `"link"`) || len(list) != 2 {
		t.Fatalf("Bert reads %s", raw)
	}
	for _, c := range list {
		for _, r := range c.Replies {
			if r.Mine {
				t.Fatalf("Bert reads a reply as his own: %+v", r)
			}
		}
	}
	for name, body := range map[string]map[string]any{
		"an empty reply":       {"thread": f.owner.ID, "text": "  "},
		"a reply too long":     {"thread": f.owner.ID, "text": strings.Repeat("x", linecomment.MaxText+1)},
		"a reply to no thread": {"text": "x"},
	} {
		if rec := f.send(f.anna, reply, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := f.send(f.anna, reply, map[string]any{"thread": "gone", "text": "x"}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"gone":true`) {
		t.Fatalf("a reply to a thread gone = %d %s", rec.Code, rec.Body.String())
	}
	if len(f.news) != 2 {
		t.Fatalf("a refused or owner's write rang: %+v", f.news)
	}
}
