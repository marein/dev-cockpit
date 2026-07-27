package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/terminalstate"
)

// The one thing the wording must never mix up: the assistant is told which
// command brings the session back, the browser is not. A person reading a toast
// on a phone cannot run a command, so a command in there is a dead end.
func TestNotRunningMessageNamesTheCommandOnlyToTheLocalCaller(t *testing.T) {
	for _, state := range []terminalstate.State{
		terminalstate.Running, terminalstate.Resumable, terminalstate.ShellGone, terminalstate.Unknown,
	} {
		browser := notRunningMessage("abc", state, false)
		if strings.Contains(browser, "coder-resume") || strings.Contains(browser, "`") {
			t.Fatalf("browser wording of state %v carries a command: %q", state, browser)
		}
		if strings.Contains(browser, "abc") {
			t.Fatalf("browser wording of state %v carries an id: %q", state, browser)
		}
		if local := notRunningMessage("abc", state, true); !strings.Contains(local, "abc") {
			t.Fatalf("local wording of state %v does not name the terminal: %q", state, local)
		}
	}
}

func TestNotRunningMessageOffersTheResumeOnlyToAResumableCoder(t *testing.T) {
	resumable := notRunningMessage("abc", terminalstate.Resumable, true)
	if !strings.Contains(resumable, "`coder-resume abc`") {
		t.Fatalf("a resumable coder is not offered the resume: %q", resumable)
	}
	// A shell cannot be resumed and an id nothing knows has nothing to resume,
	// so neither may be sent after a command that would fail.
	for _, state := range []terminalstate.State{terminalstate.Running, terminalstate.ShellGone, terminalstate.Unknown} {
		if got := notRunningMessage("abc", state, true); strings.Contains(got, "coder-resume") {
			t.Fatalf("state %v offers a resume it cannot keep: %q", state, got)
		}
	}
	if shell := notRunningMessage("abc", terminalstate.ShellGone, true); !strings.Contains(shell, "cannot be resumed") {
		t.Fatalf("a shell that is gone does not say why nothing is offered: %q", shell)
	}
}

// The two surfaces share the handler, so which wording a caller gets hangs on
// the one mark the local socket sets.
func TestInputAnswerFollowsWhoAsked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{}

	local := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(local)
	c.Request = httptest.NewRequest(http.MethodPost, "/coders/abc/input", nil)
	c.Request.Header.Set("Accept", "application/json")
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), localCallKey, true))
	s.answerNotRunning(c, "abc", terminalstate.Resumable)

	if local.Code != http.StatusGone {
		t.Fatalf("local answer status = %d, want %d", local.Code, http.StatusGone)
	}
	// The command's own client reads the JSON error field, so a sentence in a
	// plain body would reach the assistant as a cockpit that refused something.
	var answer map[string]string
	if err := json.Unmarshal(local.Body.Bytes(), &answer); err != nil {
		t.Fatalf("local answer is not JSON: %v (%s)", err, local.Body.String())
	}
	if !strings.Contains(answer["error"], "`coder-resume abc`") {
		t.Fatalf("local answer does not name the resume: %q", answer["error"])
	}

	page := httptest.NewRecorder()
	c, _ = gin.CreateTestContext(page)
	c.Request = httptest.NewRequest(http.MethodPost, "/coders/abc/input", nil)
	s.answerNotRunning(c, "abc", terminalstate.Resumable)

	if page.Code != http.StatusGone {
		t.Fatalf("browser answer status = %d, want %d", page.Code, http.StatusGone)
	}
	if strings.Contains(page.Body.String(), "coder-resume") {
		t.Fatalf("browser answer carries a command: %q", page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "not running") {
		t.Fatalf("browser answer does not say what is wrong: %q", page.Body.String())
	}
}
