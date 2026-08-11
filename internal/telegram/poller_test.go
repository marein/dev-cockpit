package telegram

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/dev-cockpit/internal/assistant"
)

// captureLog collects what the package logs, so the tests can prove that a
// stranger writes one line and that no token is ever in one.
func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(defaultLogOutput) })
	return buf
}

var defaultLogOutput = log.Writer()

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestPollerCarriesMessagesAndAdvancesTheOffsetOverSeveralRounds(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(tc.message(100, 42, "first"))
	tc.Start()

	waitFor(t, "the first message", func() bool { return len(tc.convs.prompts()) == 1 })
	waitFor(t, "the offset of the first round", func() bool { return tc.state().Offset == 101 })

	tc.api.push(tc.message(101, 42, "second"), tc.message(102, 42, "third"))
	waitFor(t, "the second round", func() bool { return len(tc.convs.prompts()) == 3 })
	waitFor(t, "the offset of the second round", func() bool { return tc.state().Offset == 103 })

	prompts := tc.convs.prompts()
	if prompts[0].Text != "first" || prompts[2].Text != "third" {
		t.Fatalf("messages arrived out of order: %+v", prompts)
	}
	for _, p := range prompts {
		if p.Conversation != "conv-1" {
			t.Fatalf("message went into %q, want the live conversation", p.Conversation)
		}
	}
}

func TestOffsetIsWrittenAfterTheMessageReachedTheConversation(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	var offsetInsideSend int64
	tc.convs.onSend = func() { offsetInsideSend = tc.state().Offset }
	tc.api.push(tc.message(500, 42, "hello"))
	tc.Start()

	waitFor(t, "the message", func() bool { return len(tc.convs.prompts()) == 1 })
	waitFor(t, "the offset", func() bool { return tc.state().Offset == 501 })

	if offsetInsideSend != 0 {
		t.Fatalf("the offset was written before the message was delivered: %d", offsetInsideSend)
	}
	// No answer ever finished here, and the offset stands anyway: it must not
	// wait for the turn, otherwise every restart mid turn replays the prompt.
	if _, ok := tc.convs.LastAnswer("conv-1"); ok {
		t.Fatal("the fake produced an answer, the check is about the offset without one")
	}
}

func TestMessagesFromAnotherChatAreDroppedAndLoggedOnce(t *testing.T) {
	logs := captureLog(t)
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(tc.message(1, 99, "let me in"), tc.message(2, 99, "hello?"), tc.message(3, 42, "mine"))
	tc.Start()

	waitFor(t, "the message of the connected chat", func() bool { return len(tc.convs.prompts()) == 1 })
	if prompts := tc.convs.prompts(); prompts[0].Text != "mine" {
		t.Fatalf("a foreign message reached the conversation: %+v", prompts)
	}
	if sent := tc.api.messages(); len(sent) != 0 {
		t.Fatalf("the stranger got an answer: %q", sent)
	}
	if count := strings.Count(logs.String(), "chat 99"); count != 1 {
		t.Fatalf("chat 99 was logged %d times, want exactly one line", count)
	}
}

func TestTheRightPairingCodeConnectsTheChat(t *testing.T) {
	tc := newTestChannel(t)
	tc.store.Update(func(s *State) {
		s.BotToken = testToken
		s.Enabled = true
	})
	code, err := tc.NewCode()
	if err != nil {
		t.Fatalf("create a code: %v", err)
	}
	tc.api.push(tc.message(1, 77, strings.ToLower(code.Value)))
	tc.Start()

	waitFor(t, "the chat to be connected", func() bool { return tc.state().ChatID == 77 })
	if name := tc.state().ChatName; name != "marein" {
		t.Fatalf("the chat name is %q", name)
	}
	waitFor(t, "the confirmation", func() bool { return len(tc.api.messages()) == 1 })

	// One use only: the same code from another chat does nothing.
	tc.api.push(Update{UpdateID: 2, Message: &Message{Date: tc.clock.Unix(), Chat: Chat{ID: 78}, Text: code.Value}})
	waitFor(t, "the second attempt to be handled", func() bool { return tc.state().Offset == 3 })
	if id := tc.state().ChatID; id != 77 {
		t.Fatalf("the connected chat moved to %d", id)
	}
}

func TestAWrongPairingCodeConnectsNothing(t *testing.T) {
	tc := newTestChannel(t)
	tc.store.Update(func(s *State) {
		s.BotToken = testToken
		s.Enabled = true
	})
	if _, err := tc.NewCode(); err != nil {
		t.Fatalf("create a code: %v", err)
	}
	tc.api.push(tc.message(1, 77, "ABCDEFGH"))
	tc.Start()

	waitFor(t, "the message to be handled", func() bool { return tc.state().Offset == 2 })
	if id := tc.state().ChatID; id != 0 {
		t.Fatalf("a wrong code connected chat %d", id)
	}
	if sent := tc.api.messages(); len(sent) != 0 {
		t.Fatalf("a wrong code got an answer: %q", sent)
	}
}

func TestAPairingCodeRunsOut(t *testing.T) {
	tc := newTestChannel(t)
	tc.store.Update(func(s *State) {
		s.BotToken = testToken
		s.Enabled = true
	})
	code, err := tc.NewCode()
	if err != nil {
		t.Fatalf("create a code: %v", err)
	}
	tc.clock = tc.clock.Add(codeLifetime + time.Second)
	if _, ok := tc.Code(); ok {
		t.Fatal("an expired code is still offered on the settings page")
	}
	tc.api.push(tc.message(1, 77, code.Value))
	tc.Start()

	waitFor(t, "the message to be handled", func() bool { return tc.state().Offset == 2 })
	if id := tc.state().ChatID; id != 0 {
		t.Fatalf("an expired code connected chat %d", id)
	}
}

func TestNoPairingCodeWhileAChatIsConnected(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	if _, err := tc.NewCode(); err == nil {
		t.Fatal("a code was created although a chat is connected")
	}
}

func TestARejectedTokenStopsThePoller(t *testing.T) {
	captureLog(t)
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.refuse(http.StatusUnauthorized)
	tc.Start()

	waitFor(t, "the poller to stop", func() bool { return tc.Status().State == StateStopped })
	if reason := tc.Status().Reason; reason == "" {
		t.Fatal("the stopped poller says nothing about why")
	}
	polls := tc.api.pollCount()
	time.Sleep(200 * time.Millisecond)
	if now := tc.api.pollCount(); now != polls {
		t.Fatalf("the poller kept asking after the token was rejected: %d -> %d", polls, now)
	}
}

func TestAConflictKeepsThePollerRunning(t *testing.T) {
	captureLog(t)
	tc := newTestChannel(t)
	tc.pair(42)
	// The old process of a restart still holds the one long poll this bot is
	// allowed, so the new one is answered with 409 for a while.
	tc.api.refuse(http.StatusConflict, http.StatusConflict)
	tc.api.push(tc.message(7, 42, "after the restart"))
	tc.Start()

	waitFor(t, "the message after the conflicts", func() bool { return len(tc.convs.prompts()) == 1 })
	if state := tc.Status().State; state != StateRunning {
		t.Fatalf("a conflict left the poller in state %q", state)
	}
}

func TestAFailingPollBacksOffInsteadOfSpinning(t *testing.T) {
	captureLog(t)
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.refuse(http.StatusInternalServerError, http.StatusInternalServerError, http.StatusInternalServerError)
	tc.Start()

	time.Sleep(500 * time.Millisecond)
	if polls := tc.api.pollCount(); polls > 2 {
		t.Fatalf("the poller asked %d times in half a second, it is not waiting between failures", polls)
	}
}

func TestBackoffGrowsToTheCeiling(t *testing.T) {
	current := minBackoff
	for i := 0; i < 20; i++ {
		next := nextBackoff(current)
		if next < current {
			t.Fatalf("the backoff shrank: %s -> %s", current, next)
		}
		current = next
	}
	if current != maxBackoff {
		t.Fatalf("the backoff settled at %s, want %s", current, maxBackoff)
	}
}

func TestMessagesFromBeforeAServerPauseAreDropped(t *testing.T) {
	logs := captureLog(t)
	tc := newTestChannel(t)
	tc.pair(42)
	old := tc.message(1, 42, "from the night before last")
	old.Message.Date = tc.clock.Add(-2 * staleAfter).Unix()
	tc.api.push(old, tc.message(2, 42, "from now"))
	tc.Start()

	waitFor(t, "the fresh message", func() bool { return len(tc.convs.prompts()) == 1 })
	waitFor(t, "the offset", func() bool { return tc.state().Offset == 3 })
	if prompts := tc.convs.prompts(); prompts[0].Text != "from now" {
		t.Fatalf("the stale message was worked on: %+v", prompts)
	}
	waitFor(t, "the dropped messages to be logged", func() bool {
		return strings.Contains(logs.String(), "dropped 1 message(s)")
	})
}

func TestTheChannelStaysAsleepWithoutAToken(t *testing.T) {
	tc := newTestChannel(t)
	tc.Start()
	if state := tc.Status().State; state != StateOff {
		t.Fatalf("a channel without a token is in state %q", state)
	}
	time.Sleep(100 * time.Millisecond)
	if polls := tc.api.pollCount(); polls != 0 {
		t.Fatalf("a channel without a token asked Telegram %d times", polls)
	}
}

func TestASwitchedOffChannelDoesNotPoll(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.SetEnabled(false)
	if state := tc.Status().State; state != StateOff {
		t.Fatalf("the switched off channel is in state %q", state)
	}
	polls := tc.api.pollCount()
	time.Sleep(100 * time.Millisecond)
	if now := tc.api.pollCount(); now != polls {
		t.Fatalf("the switched off channel kept polling: %d -> %d", polls, now)
	}
	if tc.state().BotToken == "" {
		t.Fatal("switching the channel off lost the token")
	}
}

func TestTheLineAboutATornOffTurnComesOnce(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setLast(assistant.Message{
		ID:        "msg-1",
		Role:      assistant.RoleUser,
		Content:   "what is the release coder doing",
		State:     assistant.StateComplete,
		CreatedAt: tc.clock.Add(-time.Minute),
	})

	tc.Start()
	waitFor(t, "the line about the restart", func() bool { return len(tc.api.messages()) == 1 })
	if id := tc.state().LastNoticeMessageID; id != "msg-1" {
		t.Fatalf("the notice was not written down, LastNoticeMessageID is %q", id)
	}
	tc.Stop()

	for i := 0; i < 2; i++ {
		tc.Start()
		time.Sleep(150 * time.Millisecond)
		tc.Stop()
	}
	if sent := tc.api.messages(); len(sent) != 1 {
		t.Fatalf("the restart line was sent %d times: %q", len(sent), sent)
	}
}

func TestNoLineWhenTheLastTurnFinished(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setLast(assistant.Message{
		ID:        "msg-1",
		Role:      assistant.RoleAssistant,
		Content:   "done",
		State:     assistant.StateComplete,
		CreatedAt: tc.clock.Add(-time.Minute),
	})
	tc.Start()
	time.Sleep(200 * time.Millisecond)
	if sent := tc.api.messages(); len(sent) != 0 {
		t.Fatalf("a finished turn produced a restart line: %q", sent)
	}
}

func TestNoLineAboutAnOldTornOffTurn(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setLast(assistant.Message{
		ID:        "msg-1",
		Role:      assistant.RoleUser,
		Content:   "yesterday",
		State:     assistant.StateComplete,
		CreatedAt: tc.clock.Add(-2 * noticeMaxAge),
	})
	tc.Start()
	time.Sleep(200 * time.Millisecond)
	if sent := tc.api.messages(); len(sent) != 0 {
		t.Fatalf("an old torn off turn produced a line: %q", sent)
	}
}
