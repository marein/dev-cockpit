package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/notify"
)

// An assistant opens as an editor tab through its own fragment: the surface
// of its page with the tab's address to pull, without the page around it.
func TestAnAssistantTabIsItsSurfaceAlone(t *testing.T) {
	sa := newSharedAssistant(t)
	body := sa.get("/assistants/" + sa.id + "/tab")
	for _, want := range []string{`<dc-assistant`, ` tab page-url="/assistants/` + sa.id + `/tab"`, `id="assistant-` + sa.id + `-aside"`, "Shop helper"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the tab misses %s", want)
		}
	}
	for _, never := range []string{"<html", "data-assistant-rows", "dc-rail"} {
		if strings.Contains(body, never) {
			t.Fatalf("the tab carries %s", never)
		}
	}
	all := sa.get("/assistants/" + sa.id + "/tab?all=1")
	if !strings.Contains(all, `page-url="/assistants/`+sa.id+`/tab?all=1"`) {
		t.Fatal("the whole transcript does not keep pulling itself whole")
	}
	send, _ := ownerSession(t, sa.s)
	if rec := send(httptest.NewRequest(http.MethodGet, "/assistants/nope/tab", nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("a gone assistant's tab = %d", rec.Code)
	}
}

// The editor lists every assistant, the one whose unread news failed in red.
func TestTheEditorListMarksAnUnreadFailureRed(t *testing.T) {
	sa := newSharedAssistant(t)
	sa.s.notifier.AddResolved(sa.other, notify.TargetInfo{Title: "Answer broke off.", Failed: true})
	body := sa.get("/assistants/editor")
	entry := func(id string) string {
		_, rest, ok := strings.Cut(body, `data-assistant-entry="`+id+`"`)
		if !ok {
			t.Fatalf("the list misses %s: %s", id, body)
		}
		before, _, _ := strings.Cut(rest, "</div>")
		return before
	}
	if strings.Contains(entry(sa.id), "text-danger") || !strings.Contains(entry(sa.other), "news text-danger") {
		t.Fatal("the red mark is not on the failed assistant alone")
	}
	sa.s.notifier.MarkTargetRead(sa.other)
	if strings.Contains(sa.get("/assistants/editor"), "text-danger") {
		t.Fatal("a read failure stays red")
	}
}

// No link reaches an assistant in the editor, not even one that shares it.
func TestNoLinkReachesTheEditorsAssistants(t *testing.T) {
	sa := newSharedAssistant(t)
	for _, base := range []string{sa.link(t, []string{"shop"}), sa.link(t, []string{"shop"}, sa.id)} {
		sa.notFound(t, "the editor's list", base+"/assistants/editor")
		sa.notFound(t, "an assistant's tab", base+"/assistants/"+sa.id+"/tab")
	}
}
