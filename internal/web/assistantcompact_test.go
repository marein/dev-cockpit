package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
)

// compactTurnCoder is a claude whose compaction prints one line its parser
// reads as applied.
type compactTurnCoder struct{}

func (compactTurnCoder) Available() []assistant.CoderInfo {
	return []assistant.CoderInfo{{ID: "claude", Label: "Claude", Runner: compactTurnRunner{}}}
}

type compactTurnRunner struct{}

func (compactTurnRunner) Command(assistant.TurnRequest) (assistant.Command, error) {
	return assistant.Command{Name: "/bin/sh", Args: []string{"-c", "true"}}, nil
}

func (compactTurnRunner) CompactCommand(assistant.TurnRequest) (assistant.Command, error) {
	return assistant.Command{Name: "/bin/sh", Args: []string{"-c", "echo compacted"}}, nil
}

func (compactTurnRunner) Parse(_ string, events chan<- assistant.Event) assistant.Parser {
	return compactParser{events: events}
}
func (compactTurnRunner) SessionExists(string) bool  { return true }
func (compactTurnRunner) DeleteSession(string) error { return nil }

type compactParser struct{ events chan<- assistant.Event }

func (p compactParser) Line(line []byte) error {
	if string(line) == "compacted" {
		p.events <- assistant.Event{Kind: assistant.EventCompact, Compact: &assistant.CompactReport{Outcome: assistant.CompactApplied}}
	}
	return nil
}
func (compactParser) Finish() error                      { return nil }
func (compactParser) Diagnose(err error, _ string) error { return err }

// The page offers the compaction in the ring's menu for a coder that has one,
// off before the first answer, and never to a guest or a local process: a
// guest has no route to post it to, a process on the socket is refused. The
// line it writes reads the same to the owner and to the guest.
func TestTheCompactionIsTheOwnersOnTheAssistantsPage(t *testing.T) {
	sa := newSharedAssistant(t)
	if page := sa.get("/assistants/" + sa.id); strings.Contains(page, "Compact conversation") {
		t.Fatal("a coder without compaction offers one")
	}

	stateDir := sa.s.cfg.StateDir
	assistants, workspace, err := assistant.New(stateDir, compactTurnCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	sa.s.assistants, sa.s.workspace = assistants, workspace
	page := sa.get("/assistants/" + sa.id)
	if !strings.Contains(page, "Compact conversation") || !strings.Contains(page, "data-assistant-compact data-assistant-compact-ready") {
		t.Fatal("the ring's menu offers no compaction after an answer")
	}
	if fresh := sa.get("/assistants/" + sa.other); !strings.Contains(fresh, "data-assistant-compact disabled") || !strings.Contains(fresh, "Possible after the first answer.") {
		t.Fatal("an assistant without an answer offers the compaction")
	}

	local := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(local)
	c.Request = httptest.NewRequest(http.MethodPost, "/assistants/"+sa.id, strings.NewReader(url.Values{"form": {"compact"}}.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), localCallKey, true))
	c.Params = gin.Params{{Key: "id", Value: sa.id}}
	sa.s.handleAssistantAction(c)
	if local.Code != http.StatusForbidden {
		t.Fatalf("the local socket compacted: %d", local.Code)
	}

	if rec := sa.post("/assistants/"+sa.id, url.Values{"form": {"compact"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("the owner's compaction answered %d %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for !assistants.RunningSince(sa.id).IsZero() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	thread, _ := assistants.Get(sa.id)
	last := thread.Messages[len(thread.Messages)-1]
	if last.Compact == nil || last.Compact.Outcome != assistant.CompactApplied {
		t.Fatalf("the compaction ended as %+v", last)
	}
	if page := sa.get("/assistants/" + sa.id); !strings.Contains(page, `data-assistant-compaction="compacted"`) || !strings.Contains(page, "Conversation compacted.") {
		t.Fatal("the owner's thread does not show the compaction")
	}

	if guestMayRead(http.MethodPost, "/assistants/:id") {
		t.Fatal("a guest may post to an assistant")
	}
	base := sa.link(t, []string{"shop"}, sa.id)
	guest := sa.guest(base + "/assistants/" + sa.id)
	if guest.Code != http.StatusOK || !strings.Contains(guest.Body.String(), "Conversation compacted.") || strings.Contains(guest.Body.String(), "Compact conversation") {
		t.Fatalf("the guest's page reads %d, compaction line %v, control %v", guest.Code, strings.Contains(guest.Body.String(), "Conversation compacted."), strings.Contains(guest.Body.String(), "Compact conversation"))
	}
}

func TestTheCompactionLineSaysHowItWent(t *testing.T) {
	for _, tc := range []struct {
		state   assistant.State
		outcome string
		line    string
		mark    string
	}{
		{assistant.StateStreaming, "", "Compacting the conversation…", "streaming"},
		{assistant.StateComplete, assistant.CompactApplied, "Conversation compacted.", "compacted"},
		{assistant.StateComplete, assistant.CompactNothing, "Nothing to compact.", "nothing"},
		{assistant.StateFailed, "", "The conversation was not compacted.", "failed"},
		{assistant.StateCancelled, "", "You stopped the compaction.", "cancelled"},
		{assistant.StateInterrupted, "", "The compaction was interrupted.", "interrupted"},
	} {
		view := compactView(assistant.Message{Role: assistant.RoleCockpit, State: tc.state, Compact: &assistant.Compaction{Outcome: tc.outcome}})
		if view == nil || view.Line != tc.line || view.Mark != tc.mark {
			t.Fatalf("%s %s reads %+v", tc.state, tc.outcome, view)
		}
	}
	if compactView(assistant.Message{Role: assistant.RoleCockpit}) != nil || compactHeadline(assistant.Message{}) != "" {
		t.Fatal("a message without a compaction reads as one")
	}
}
