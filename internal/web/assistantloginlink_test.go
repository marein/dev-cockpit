package web

import (
	"testing"

	"github.com/local/dev-cockpit/internal/assistant"
	"github.com/local/dev-cockpit/internal/coderlogin"
)

type stubWebLogin struct{}

func (stubWebLogin) Command() (string, []string)         { return "true", nil }
func (stubWebLogin) TakesCode() bool                     { return false }
func (stubWebLogin) Read(_, _ string) coderlogin.Reading { return coderlogin.Reading{} }
func (stubWebLogin) Probe() coderlogin.State             { return coderlogin.State{} }

// The one failure a login fixes carries the login itself; every other failure
// and every coder without a web login stays a bare sentence.
func TestAFailedLoginTurnCarriesTheLogin(t *testing.T) {
	s := &Server{coderLogin: coderlogin.NewService(map[string]coderlogin.Login{"claude": stubWebLogin{}})}
	failed := assistant.Message{ID: "m1", Role: assistant.RoleAssistant, State: assistant.StateFailed, Error: assistant.ErrNotLoggedIn.Error()}
	view := s.assistantMessageView("conv", failed, false, false, "claude")
	if view.Login == nil {
		t.Fatal("the login failure must carry the login")
	}
	if view.Login.URL != "/settings/coders/claude/login" {
		t.Fatalf("login url = %q", view.Login.URL)
	}
	if view.Login.Label != "Claude" {
		t.Fatalf("login label = %q", view.Login.Label)
	}
	other := failed
	other.Error = "The coder could not finish this answer."
	if got := s.assistantMessageView("conv", other, false, false, "claude").Login; got != nil {
		t.Fatalf("a generic failure must carry no login, got %+v", got)
	}
	if got := s.assistantMessageView("conv", failed, false, false, "someday").Login; got != nil {
		t.Fatalf("a coder without a web login must carry no login, got %+v", got)
	}
}
