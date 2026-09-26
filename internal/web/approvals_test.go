package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/settings"
)

var composeApprovalField = "approval-" + approvalComposeActions

// Absent asks, off asks nobody, and switching it back on removes the key
// rather than storing a copy of the default.
func TestApprovalAsksReadsAndStores(t *testing.T) {
	if !ApprovalAsks(nil, approvalComposeActions) {
		t.Fatal("no store did not ask")
	}
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	if !ApprovalAsks(store, approvalComposeActions) {
		t.Fatal("an install that never saved it does not ask")
	}
	setApprovalAsks(store, approvalComposeActions, false)
	if ApprovalAsks(store, approvalComposeActions) || store.Get("assistant-approval-compose-actions") != "off" {
		t.Fatal("off was not stored under assistant-approval-compose-actions")
	}
	setApprovalAsks(store, approvalComposeActions, true)
	if _, ok := store.Lookup("assistant-approval-compose-actions"); ok || !ApprovalAsks(store, approvalComposeActions) {
		t.Fatal("on left the key behind")
	}
}

func (f *composeFixture) postApprovals(t *testing.T, form url.Values) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/settings/assistant/approvals", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("the save answered %d", rec.Code)
	}
}

// The Compose actions approval off parks nothing for any assistant, a form
// that does not carry the field moves nothing, and switched on again every
// assistant is asked.
func TestTheComposeActionsApprovalDecidesForEveryAssistant(t *testing.T) {
	f := newComposeFixture(t)
	f.postApprovals(t, url.Values{composeApprovalField: {"0"}})
	if ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("the save did not switch the approval off")
	}
	code, answer := f.compose(t, "down-volumes", true)
	if code != http.StatusOK || answer["pending"] == true || len(f.broker.Questions()) != 0 {
		t.Fatalf("a confirm action was parked with the approval off: %d %v", code, answer)
	}
	f.waitRun(t, answer["run"].(string), func(v docker.RunView) bool { return !v.Running })

	f.postApprovals(t, url.Values{"unrelated": {"1"}})
	if ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("a form without the field moved the approval")
	}

	f.postApprovals(t, url.Values{composeApprovalField: {"0", "1"}})
	if !ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("the save did not switch the approval back on")
	}
	_, again := f.compose(t, "down-volumes", true)
	if again["pending"] != true {
		t.Fatalf("the approval did not come back: %v", again)
	}
	q := f.waitQuestion(t)
	f.router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(`{"key":"`+q.Key+`","id":"`+q.ID+`","approve":false}`)))
	f.waitNoteOf(t, again["run"].(string))
}

// The compose approval is one entry of the kinds the tab renders and the
// save reads, stored on its own key.
func TestTheComposeActionsApprovalIsAKind(t *testing.T) {
	if len(approvalKinds) == 0 || approvalKinds[0].ID != approvalComposeActions || approvalKinds[0].Label != "Compose actions approval" {
		t.Fatalf("the kinds read %+v", approvalKinds)
	}
	if approvalKey(approvalComposeActions) != "assistant-approval-compose-actions" {
		t.Fatalf("the key reads %q", approvalKey(approvalComposeActions))
	}
}

// "Approve and don't ask again" approves the run and turns the approval off
// for every assistant; a denial with the same box remembers nothing.
func TestApproveAndDontAskAgainTurnsTheApprovalOff(t *testing.T) {
	f := newComposeFixture(t)
	_, denied := f.compose(t, "down-volumes", true)
	q := f.waitQuestion(t)
	f.router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(`{"key":"`+q.Key+`","id":"`+q.ID+`","approve":false,"remember":true}`)))
	f.waitNoteOf(t, denied["run"].(string))
	if !ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("a denial switched the approval off")
	}

	_, first := f.compose(t, "down-volumes", true)
	q = f.waitQuestion(t)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(`{"key":"`+q.Key+`","id":"`+q.ID+`","approve":true,"remember":true}`)))
	if got := decodeJSON(t, rec); got["ok"] != true {
		t.Fatalf("the approval was refused: %v", got)
	}
	f.waitRun(t, first["run"].(string), func(v docker.RunView) bool { return !v.Pending && !v.Running })
	if ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("the box did not switch the approval off")
	}
	for _, id := range []string{f.owner, ""} {
		if id == "" {
			other, err := f.s.assistants.Create("claude")
			if err != nil {
				t.Fatal(err)
			}
			id = other.ID
		}
		rec = f.localPost(t, "/projects/shop/docker/compose", id, url.Values{"stack": {""}, "action": {"down-volumes"}})
		got := decodeJSON(t, rec)
		if rec.Code != http.StatusOK || got["pending"] == true {
			t.Fatalf("an assistant was still asked: %d %s", rec.Code, rec.Body.String())
		}
		f.waitRun(t, got["run"].(string), func(v docker.RunView) bool { return !v.Running })
	}
}

// An assistant cannot switch the approval off over the socket, neither on the
// settings route nor with the box on its own question.
func TestAnAssistantCannotTurnTheApprovalOff(t *testing.T) {
	f := newComposeFixture(t)
	rec := f.localPost(t, "/settings/assistant/approvals", f.owner, url.Values{composeApprovalField: {"0"}})
	if rec.Code != http.StatusForbidden || decodeJSON(t, rec)["error"] != approvalLocalRefusal {
		t.Fatalf("a local save answered %d: %s", rec.Code, rec.Body.String())
	}
	if !ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatal("a local save switched the approval off")
	}
	_, answer := f.compose(t, "down-volumes", true)
	q := f.waitQuestion(t)
	req := httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(`{"key":"`+q.Key+`","id":"`+q.ID+`","approve":true,"remember":true}`))
	req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
	req.Header.Set(localapi.AssistantHeader, f.owner)
	rec = httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !ApprovalAsks(f.s.settings, approvalComposeActions) {
		t.Fatalf("a local approval answered %d and the approval reads %v", rec.Code, ApprovalAsks(f.s.settings, approvalComposeActions))
	}
	f.router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/git/prompt", strings.NewReader(`{"key":"`+q.Key+`","id":"`+q.ID+`","approve":false}`)))
	f.waitNoteOf(t, answer["run"].(string))
}
