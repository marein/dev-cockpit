package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
)

// ownerComments reads the owner's answer of the project's comments, narrowed
// by the query.
func (f *commentGuests) ownerComments(t *testing.T, query string) []lineCommentView {
	t.Helper()
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, shopComments+query, nil))
	var answer struct {
		Comments []lineCommentView `json:"comments"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil {
		t.Fatalf("reading the comments = %d %s", rec.Code, rec.Body.String())
	}
	return answer.Comments
}

// A project comment is told explicitly and checked where it enters: it names
// no path, no line and no quote, a line comment still needs a path and a
// line, and an edit never moves a thread between the two. A guest's lands
// under its link and is news on the project, not on a file.
func TestAProjectCommentIsCheckedWhereItEnters(t *testing.T) {
	f := newCommentGuests(t)
	f.r.GET("/projects/:name/editor/comments", f.s.handleEditorComments)
	for _, body := range []map[string]any{
		{"project": true, "path": "a.txt", "text": "x"},
		{"project": true, "line": 2, "text": "x"},
		{"project": true, "lineText": "", "text": "x"},
		{"project": true, "path": " ", "text": "x"},
	} {
		if rec := f.send("", shopComments, body); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no file and no line") {
			t.Fatalf("%v = %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := f.send("", shopComments, map[string]any{"line": 1, "text": "x"}); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "A file path is required.") {
		t.Fatalf("a line comment without a path = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.send("", shopComments, map[string]any{"project": true, "text": "  "}); rec.Code != http.StatusBadRequest {
		t.Fatalf("an empty project comment = %d %s", rec.Code, rec.Body.String())
	}
	whole := savedComment(t, f.send("", shopComments, map[string]any{"project": true, "text": "rename the shop"}))
	if !whole.Project || whole.Path != "" || whole.Line != 0 || whole.LineText != "" || whole.ID == "" {
		t.Fatalf("the project comment answered %+v", whole)
	}
	if rec := f.send("", shopComments, map[string]any{"id": whole.ID, "path": "a.txt", "line": 1, "text": "onto a line"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("moving the project comment onto a line = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.send("", shopComments, map[string]any{"id": f.owner.ID, "project": true, "text": "onto the project"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("moving a line comment onto the project = %d %s", rec.Code, rec.Body.String())
	}
	if edited := savedComment(t, f.send("", shopComments, map[string]any{"id": whole.ID, "project": true, "text": "rename the shop, soon"})); !edited.Project || edited.Text != "rename the shop, soon" {
		t.Fatalf("the edit answered %+v", edited)
	}
	guest := savedComment(t, f.send(f.anna, shopComments, map[string]any{"project": true, "text": "a guest's"}))
	if !guest.Project || !guest.Mine || guest.Author != "Anna" {
		t.Fatalf("the guest's project comment answered %+v", guest)
	}
	if len(f.news) != 1 || !f.news[0].OnProject || f.news[0].Path != "" || f.news[0].Guest != "Anna" {
		t.Fatalf("the guest's news is %+v", f.news)
	}
	list := f.ownerComments(t, "")
	if len(list) != 3 || !list[0].Project || !list[1].Project || list[2].ID != f.owner.ID {
		t.Fatalf("the project comments do not lead the list: %+v", list)
	}
}

// Replies, resolve and delete by id work on a project comment the way they
// work on a line comment, while the path filter of the list and the deletes
// by path and of the outdated ones never reach it.
func TestAProjectCommentStandsApartFromThePathFilters(t *testing.T) {
	f := newCommentGuests(t)
	f.r.GET("/projects/:name/editor/comments", f.s.handleEditorComments)
	whole := savedComment(t, f.send("", shopComments, map[string]any{"project": true, "text": "rename the shop"}))
	if got := f.ownerComments(t, "?path=**"); len(got) != 1 || got[0].ID != f.owner.ID {
		t.Fatalf("the path filter answered %+v", got)
	}
	reply := f.send(f.anna, shopComments+"/reply", map[string]any{"thread": whole.ID, "text": "agreed"})
	if replied := savedComment(t, reply); len(replied.Replies) != 1 || !replied.Project {
		t.Fatalf("the reply answered %+v", replied)
	}
	if resolved := savedComment(t, f.send("", shopComments+"/resolve", map[string]any{"id": whole.ID, "resolved": true})); !resolved.Resolved {
		t.Fatalf("the resolve answered %+v", resolved)
	}
	for _, body := range []map[string]any{{"paths": []string{"**"}}, {"paths": []string{""}}, {"outdated": true}, {"outdated": true, "paths": []string{"**"}}} {
		if rec := f.send("", shopComments+"/delete", body); rec.Code != http.StatusOK {
			t.Fatalf("%v = %d %s", body, rec.Code, rec.Body.String())
		}
		if _, ok := f.note(t, whole.ID); !ok {
			t.Fatalf("%v took the project comment", body)
		}
	}
	if rec := f.send("", shopComments+"/delete", map[string]any{"ids": []string{whole.ID}}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"removed":1`) {
		t.Fatalf("the delete by id = %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := f.note(t, whole.ID); ok {
		t.Fatal("the delete by id left the project comment")
	}
}

// The text lists the project comments first, each as Project comment with
// its author, its state and its replies like a line comment's entry.
func TestCommentsTextListsTheProjectCommentsFirst(t *testing.T) {
	f := newCommentGuests(t)
	f.r.GET("/projects/:name/editor/comments", f.s.handleEditorComments)
	savedComment(t, f.send(f.anna, shopComments, map[string]any{"project": true, "text": "rename\nthe shop"}))
	text := commentsText("shop", f.ownerComments(t, ""))
	heads, notes := textEntries(t, text)
	if len(heads) != 2 || heads[0] != "- Guest Anna: Project comment" || heads[1] != "- a.txt:1, code: one" {
		t.Fatalf("the heads read %q:\n%s", heads, text)
	}
	if strings.Join(notes[0], "|") != "rename|the shop" {
		t.Fatalf("the note reads %q", notes[0])
	}
	resolved := commentsText("shop", []lineCommentView{{Project: true, Text: "done", Resolved: true}})
	if !strings.Contains(resolved, "\n- Project comment (resolved)\n  > done") {
		t.Fatalf("the resolved project comment reads:\n%s", resolved)
	}
}

// The owner's project comment publishes like a line comment, as a general
// draft that reads no file: named by its id and with all, before the line
// comments; a guest's is refused by its id.
func TestAProjectCommentPublishesAsAGeneralComment(t *testing.T) {
	f := newCommentGuests(t)
	whole := savedComment(t, f.send("", shopComments, map[string]any{"project": true, "text": "rename the shop"}))
	theirs := savedComment(t, f.send(f.anna, shopComments, map[string]any{"project": true, "text": "a guest's"}))
	p, err := f.s.projects.FindByName("shop")
	if err != nil {
		t.Fatal(err)
	}
	picked, err := f.s.forgePublishPick(p, []string{whole.ID}, false)
	if err != nil || len(picked) != 1 || picked[0].ID != whole.ID {
		t.Fatalf("publishing the project comment picked %+v, %v", picked, err)
	}
	var refusal *forge.PublishRefusal
	if _, err := f.s.forgePublishPick(p, []string{theirs.ID}, false); !errors.As(err, &refusal) || refusal.Reason != fmt.Sprintf("The project comment %q is a guest's, only your own publish.", theirs.ID) {
		t.Fatalf("publishing a guest's project comment = %v", err)
	}
	picked, err = f.s.forgePublishPick(p, nil, true)
	if err != nil || len(picked) != 2 || picked[0].ID != whole.ID || picked[1].ID != f.owner.ID {
		t.Fatalf("publishing all picked %+v, %v", picked, err)
	}
	drafts, err := forgeDrafts(context.Background(), p, forge.Watch{Project: "shop", Branch: "feature", Head: "a1"}, picked[:1])
	if err != nil || len(drafts) != 1 || drafts[0].draft != (forge.Draft{General: true, Body: "rename the shop"}) {
		t.Fatalf("the project comment drafts as %+v, %v", drafts, err)
	}
}
