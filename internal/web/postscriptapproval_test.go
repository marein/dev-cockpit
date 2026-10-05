package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/project"
)

func newPostScriptFixture(t *testing.T) *composeFixture {
	t.Helper()
	f := newComposeFixture(t)
	f.s.cfg = config.Config{StateDir: f.stateDir}
	f.router.GET("/settings/projects/worktrees", f.s.handleSettingsProjectsWorktrees)
	f.router.POST("/settings/projects/worktrees", f.s.handleSettingsProjectsWorktreesSave)
	return f
}

func (f *composeFixture) postScriptAs(t *testing.T, method, id, script string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/settings/projects/worktrees", strings.NewReader(url.Values{"post_script": {script}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
	if id != "" {
		req.Header.Set(localapi.AssistantHeader, id)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *composeFixture) postScript() string {
	return project.ReadPostScript(filepath.Join(f.stateDir, project.PostScriptFile))
}

// An assistant reads the script, and its write waits for the user, who sees
// the new content: approved it is saved the way the page saves it, denied it
// stays as it was.
func TestAnAssistantsPostScriptWriteWaitsForTheApproval(t *testing.T) {
	f := newPostScriptFixture(t)
	if got := decodeJSON(t, f.postScriptAs(t, http.MethodGet, f.owner, "")); got["script"] != "" {
		t.Fatalf("no script read %v", got)
	}
	rec := f.postScriptAs(t, http.MethodPost, f.owner, "#!/bin/sh\r\necho hi")
	if answer := decodeJSON(t, rec); answer["pending"] != true || answer["what"] != "Set the worktree post script" {
		t.Fatalf("the write answered %d %v", rec.Code, answer)
	}
	q := f.waitQuestion(t)
	if !reflect.DeepEqual(q.Details, []askpass.Detail{{Label: "Script", Value: "#!/bin/sh\necho hi\n"}}) {
		t.Fatalf("the question reads %+v", q)
	}
	if f.postScript() != "" {
		t.Fatal("a waiting write saved the script")
	}
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Done || !strings.Contains(note.Content, "post script is saved") || f.postScript() != "#!/bin/sh\necho hi\n" {
		t.Fatalf("the approved write left %q, note %s", f.postScript(), note.Content)
	}
	if info, err := os.Stat(filepath.Join(f.stateDir, project.PostScriptFile)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the script is %v %v", info, err)
	}
	if got := decodeJSON(t, f.postScriptAs(t, http.MethodGet, f.owner, "")); got["script"] != "#!/bin/sh\necho hi\n" {
		t.Fatalf("the script read %v", got)
	}
	f.waitNoQuestion(t)
	f.postScriptAs(t, http.MethodPost, f.owner, "")
	f.decide(t, f.waitQuestion(t), false, false)
	if note := f.waitApprovalNotes(t, f.owner, 2)[1]; note.Note.Verdict != approval.Declined || f.postScript() == "" {
		t.Fatalf("the denied delete left %q, note %s", f.postScript(), note.Content)
	}
}

// A script without an interpreter is refused before anybody is asked, a call
// without an assistant is refused, and with the approval off the write and
// an empty delete happen at once.
func TestAPostScriptWriteIsCheckedAndTheApprovalCanBeOff(t *testing.T) {
	f := newPostScriptFixture(t)
	if rec := f.postScriptAs(t, http.MethodPost, f.owner, "echo hi"); rec.Code != http.StatusBadRequest || len(f.broker.Questions()) != 0 {
		t.Fatalf("a script without an interpreter answered %d", rec.Code)
	}
	if rec := f.postScriptAs(t, http.MethodPost, "", "#!/bin/sh\n"); rec.Code != http.StatusForbidden {
		t.Fatalf("a call without an assistant answered %d", rec.Code)
	}
	setApprovalAsks(f.s.settings, approvalWorktreePostScript, false)
	rec := f.postScriptAs(t, http.MethodPost, f.owner, "#!/bin/sh\n")
	if answer := decodeJSON(t, rec); answer["ok"] != true || f.postScript() != "#!/bin/sh\n" || len(f.broker.Questions()) != 0 {
		t.Fatalf("the write with the approval off answered %v, left %q", answer, f.postScript())
	}
	f.postScriptAs(t, http.MethodPost, f.owner, "")
	if _, err := os.Stat(filepath.Join(f.stateDir, project.PostScriptFile)); !os.IsNotExist(err) {
		t.Fatalf("an empty write left the script: %v", err)
	}
}
